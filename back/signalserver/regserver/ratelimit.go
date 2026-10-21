// Package regserver rate limiting and login backoff primitives.
package regserver

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// bucket is the token bucket state for a single source key.
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter is a thread-safe token bucket rate limiter keyed by string (typically IP).
type Limiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*bucket

	now func() time.Time
}

// NewLimiter creates a new Limiter. rps <= 0 means unlimited (Allow always true).
func NewLimiter(rps float64, burst int) *Limiter {
	if burst <= 0 {
		burst = int(rps * 2)
		if burst < 1 {
			burst = 1
		}
	}
	l := &Limiter{rps: rps, burst: float64(burst), buckets: map[string]*bucket{}}
	if l.rps <= 0 {
		l.buckets = nil
	}
	return l
}

// Enabled reports whether the limiter actually restricts traffic.
func (l *Limiter) Enabled() bool { return l != nil && l.rps > 0 }

// Allow takes a token for key; returns false if bucket is empty.
func (l *Limiter) Allow(key string) bool {
	if !l.Enabled() {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rps)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	l.sweep(now)
	return true
}

func (l *Limiter) sweep(now time.Time) {
	if len(l.buckets) < 4096 {
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

func (l *Limiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// TooManyRequests writes HTTP 429 and Retry-After header.
func TooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}

// ClientIP extracts client IP from RemoteAddr only (avoids spoofable X-Forwarded-For).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.TrimSpace(host)
}

// Backoff tracks consecutive login failures per account and enforces exponential backoff.
type Backoff struct {
	mu        sync.Mutex
	entries   map[string]*backoffEntry
	threshold int
	base      time.Duration
	max       time.Duration
	now       func() time.Time
}

type backoffEntry struct {
	fails int
	until time.Time
	last  time.Time
}

// BackoffParams defines parameters for login backoff.
type BackoffParams struct {
	Threshold int
	Base      time.Duration
	Max       time.Duration
}

// DefaultBackoff returns the default login backoff strategy.
func DefaultBackoff() *Backoff {
	return NewBackoff(BackoffParams{})
}

// NewBackoff creates a new Backoff tracker with specified params.
func NewBackoff(p BackoffParams) *Backoff {
	if p.Threshold <= 0 {
		p.Threshold = 5
	}
	if p.Base <= 0 {
		p.Base = 15 * time.Second
	}
	if p.Max <= 0 {
		p.Max = 5 * time.Minute
	}
	return &Backoff{
		entries:   map[string]*backoffEntry{},
		threshold: p.Threshold,
		base:      p.Base,
		max:       p.Max,
	}
}

func (b *Backoff) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// RetryAfter returns how long the account must wait before retrying (0 = allowed).
func (b *Backoff) RetryAfter(key string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	e, ok := b.entries[key]
	if !ok {
		return 0
	}
	now := b.clock()
	e.last = now
	if now.Before(e.until) {
		return e.until.Sub(now)
	}
	e.until = time.Time{}
	return 0
}

// Fail records a failure for key and returns the required wait duration.
func (b *Backoff) Fail(key string) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clock()
	e, ok := b.entries[key]
	if !ok {
		e = &backoffEntry{}
		b.entries[key] = e
	}
	e.fails++
	e.last = now
	if e.fails < b.threshold {
		return 0
	}
	d := b.base
	for i := b.threshold; i < e.fails; i++ {
		d *= 2
		if d >= b.max {
			d = b.max
			break
		}
	}
	e.until = now.Add(d)
	b.sweep(now)
	return d
}

// Success resets the failure count and backoff for key on successful login.
func (b *Backoff) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

func (b *Backoff) sweep(now time.Time) {
	if len(b.entries) < 4096 {
		return
	}
	for k, e := range b.entries {
		if e.until.IsZero() && now.Sub(e.last) > time.Hour {
			delete(b.entries, k)
		}
	}
}
