// Package signalserver — per-IP rate limiting.
//
// Three independent token buckets (announce / WS / ID) because those endpoints
// differ hugely in cost and blast radius. All code here is self-contained and
// does not depend on the Server struct; it only produces and consumes buckets
// that the Server stores and that handlers call .allow() on.
package signalserver

import (
	"math"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// tokenBucket is a per-IP token bucket: constant refill rate, capped burst.
//
// Deliberately self-contained rather than shared with the main module: this is
// a standalone module (github.com/Hana-ame/go-peerserver) and importing
// peerdrive/internal/... would create a dependency from a published library
// back into an application. Same algorithm as internal/ratelimit in the main
// module — keep the two in sync if one changes.
type tokenBucket struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*bucketState
	now     func() time.Time // overridable in tests
}

type bucketState struct {
	tokens float64
	last   time.Time
}

func newTokenBucket(rps float64, burst int) *tokenBucket {
	if burst <= 0 {
		burst = int(rps * 2)
		if burst < 1 {
			burst = 1
		}
	}
	return &tokenBucket{
		rps:     rps,
		burst:   float64(burst),
		buckets: map[string]*bucketState{},
	}
}

// allow takes one token for key; false means the caller should be rejected.
func (b *tokenBucket) allow(key string) bool {
	if b == nil {
		return true // not configured = unlimited
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	if b.now != nil {
		now = b.now()
	}
	st, ok := b.buckets[key]
	if !ok {
		st = &bucketState{tokens: b.burst, last: now}
		b.buckets[key] = st
	}
	// Refill at a constant rate but never past capacity — otherwise a client that
	// stayed quiet for an hour could return with an enormous burst allowance.
	st.tokens = math.Min(b.burst, st.tokens+now.Sub(st.last).Seconds()*b.rps)
	st.last = now
	defer b.sweep(now) // caller holds b.mu; sweep must not relock
	if st.tokens < 1 {
		return false
	}
	st.tokens--
	return true
}

// sweep drops sources idle for 10 minutes so the map cannot grow without bound.
// Caller must hold b.mu.
func (b *tokenBucket) sweep(now time.Time) {
	if len(b.buckets) < 4096 {
		return // only pay for this at scale
	}
	for k, st := range b.buckets {
		if now.Sub(st.last) > 10*time.Minute {
			delete(b.buckets, k)
		}
	}
}

// clientIP returns the request's source address from RemoteAddr only.
//
// X-Forwarded-For is deliberately ignored: it is client-supplied, so trusting it
// would let an attacker mint a fresh bucket per request — rate limiting that is
// worse than none, because it also looks like protection. The cost is that
// behind a same-host nginx every client shares 127.0.0.1's bucket; limiting too
// hard is strictly better than not limiting at all. Deployments behind a proxy
// that need per-client buckets should rate-limit at the proxy.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return strings.TrimSpace(host)
}

// rateLimited writes a 429. For the WS path the caller rejects *before* calling
// upgrader.Upgrade, because once the upgrade response headers are written the
// status code can no longer be changed.
func rateLimited(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "1")
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}
