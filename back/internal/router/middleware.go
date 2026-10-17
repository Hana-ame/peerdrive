// Package router cross-cutting middleware: request ID, structured access logging,
// security response headers, per-IP rate limiting.
//
// Why a separate file: all four of these are "run once per request but carry no
// business logic". Mixing them into router.go (already 380 lines of route
// registration) makes "what middleware does this route have" impossible to see
// at a glance — and that's exactly what security audits need to see most.
//
// Mounting order (in Router.Engine, order = execution order):
//   RequestID → SecurityHeaders → AccessLog → RateLimit → CORS → routes
// Request ID must be first (all subsequent logs need to carry it), rate limiting
// must be before business logic but after logging (rate-limited requests must
// also leave traces, otherwise attack traffic is the quietest).

package router

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"

	"github.com/gin-gonic/gin"
)

// HeaderRequestID HTTP header name and context key for request ID.
// Same-named header from upstream (reverse proxy) is reused, so a request can be
// correlated across services.
const HeaderRequestID = "X-Request-ID"

// RequestID assigns an ID to each request: reuses upstream X-Request-ID, generates one if absent.
//
// Why it's needed: when something goes wrong, logs have dozens of concurrent
// requests interleaved. Without IDs, you can only guess which lines belong to
// which call by timestamp. With it, a user reporting one ID can pull all logs
// for that single request.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if strings.TrimSpace(id) == "" {
			id = newRequestID()
		} else if len(id) > 128 { // prevent log injection / oversized headers blowing up log lines
			id = id[:128]
		}
		c.Set("request_id", id)
		c.Header(HeaderRequestID, id)
		c.Next()
	}
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Fall back to timestamp when random source unavailable: ID only needs
		// "this time differs from that time", not unpredictability.
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

// RequestIDFrom retrieves the request ID written to context by middleware (for controller logging).
func RequestIDFrom(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SecurityHeaders sets a set of default security response headers.
//
// CSP is on by default. /swagger/* and /panel/* get a RELAXED CSP instead of
// none: both are single-file HTML pages with inline scripts/styles (Swagger's
// official UI relies on inline scripts and eval; the panel is built as one
// self-contained file so it can be opened via file:// or any static host).
// Relaxing script-src/style-src for those two prefixes is unavoidable without
// turning the panel into a multi-file build, but the directives that are NOT
// about inline code are still enforced — so the exemption is a partial rather
// than total surrender (see inlineHTMLCSP). Admin console pages are not served
// by this process (front is separately deployed), so strict CSP has no business
// side effects. If compatibility issues arise, use PEERDRIVE_CSP=off.
func SecurityHeaders(disableCSP bool) gin.HandlerFunc {
	csp := "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	// inlineHTMLCSP: the strict policy plus only what a single-file page needs.
	// object-src 'none' still blocks Flash/Java/<object> plugins, base-uri 'self'
	// still blocks <base href> exfiltration, frame-ancestors 'self' still blocks
	// clickjacking. Only inline script/style is allowed — strictly better than
	// sending no CSP at all, which is what this branch used to do.
	inlineHTMLCSP := "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		// Permissions-Policy: this node never needs a camera, mic, geolocation,
		// payment sheet, USB handle, or fullscreen from an embedded frame. Deny by
		// default so a page served here (the panel, swagger) can never claim them.
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=(), fullscreen=(self)")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		// Strict-Transport-Security (HSTS)：进程内是纯 HTTP（TLS 由部署层终止），
		// 浏览器只在 HTTPS 响应上生效该头、且对裸 IP/局域网地址不生效——所以对
		// 纯 HTTP 或 LAN 部署是无害的，一旦挂在 TLS + 域名之后即强制 HTTPS 升级。
		// max-age=31536000（1 年，OWASP 生产建议）；**故意不带 includeSubDomains**：
		// 本二进制是 BYO 部署，运营者可能在同一域下有未上 TLS 的子域，带上会
		// 硬性锁死它们造成可用性风险；includeSubDomains 留给运营者自行决定。
		c.Header("Strict-Transport-Security", "max-age=31536000")
		// /panel 与 /swagger：单文件内联页面（4 段 <script> + style），完全不给
		// CSP 时浏览器控制台报 "Executing inline script violates ... default-src
		// 'self'"，自动连接的前置脚本根本不会跑 —— 实测踩过。但仍降级成
		// inlineHTMLCSP 而不是跳过 CSP：object-src / base-uri / frame-ancestors
		// 这三条与 inline 无关，照旧生效。放宽仅限这两个前缀，其余路径仍是 csp。
		// 注意必须按路径段匹配（"/panel" 或 "/panel/..."），不能裸用
		// strings.HasPrefix —— 那会让 /panels、/swaggerish 这类无关路径也拿到
		// 放宽策略（红队 R2 实测踩到）。
		p := c.Request.URL.Path
		inlineHTML := p == "/swagger" || strings.HasPrefix(p, "/swagger/") ||
			p == "/panel" || strings.HasPrefix(p, "/panel/")
		if !disableCSP {
			if inlineHTML {
				c.Header("Content-Security-Policy", inlineHTMLCSP)
			} else {
				c.Header("Content-Security-Policy", csp)
			}
		}
		c.Next()
	}
}

// accessLogEntry access log entry structure (field names aligned with common logging system conventions).
//
// Deliberately NOT logged: Authorization header, tokens in query strings, request body.
// Logs get copied everywhere (posted to issues, sent to groups); once credentials are
// included, it's a second leak.
type accessLogEntry struct {
	TS        string  `json:"ts"`
	Level     string  `json:"level"`
	Msg       string  `json:"msg"`
	RequestID string  `json:"request_id"`
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	Status    int     `json:"status"`
	LatencyMS float64 `json:"latency_ms"`
	ClientIP  string  `json:"client_ip"`
	UserAgent string  `json:"user_agent,omitempty"`
	Bytes     int     `json:"resp_bytes"`
}

// AccessLog outputs one JSON access log line (level by status code: 5xx=ERROR, 4xx=WARN, rest INFO).
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		ua := c.Request.UserAgent()
		if len(ua) > 200 {
			ua = ua[:200]
		}
		level := "info"
		if c.Writer.Status() >= 500 {
			level = "error"
		} else if c.Writer.Status() >= 400 {
			level = "warn"
		}
		b, err := json.Marshal(accessLogEntry{
			TS:        time.Now().UTC().Format(time.RFC3339Nano),
			Level:     level,
			Msg:       "http request",
			RequestID: RequestIDFrom(c),
			Method:    c.Request.Method,
			Path:      c.Request.URL.Path,
			Status:    c.Writer.Status(),
			LatencyMS: float64(time.Since(start).Microseconds()) / 1000,
			ClientIP:  c.ClientIP(),
			UserAgent: ua,
			Bytes:     c.Writer.Size(),
		})
		if err != nil {
			return
		}
		switch level {
		case "error":
			log.LogError("%s", b)
		case "warn":
			log.LogWarn("%s", b)
		default:
			log.LogInfo("%s", b)
		}
	}
}

// ── Per-IP rate limiting (token bucket) ──

type bucket struct {
	tokens float64
	last   time.Time
}

type limiter struct {
	mu      sync.Mutex
	rps     float64
	burst   int
	buckets map[string]*bucket
}

// allow takes a token; bucket refills at constant rate over time (classic token bucket).
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.burst), last: now}
		l.buckets[key] = b
	}
	// Refill at constant rate but not beyond bucket capacity (otherwise long periods
	// without requests would accumulate a burst)
	b.tokens = math.Min(float64(l.burst), b.tokens+now.Sub(b.last).Seconds()*l.rps)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep cleans up long-inactive sources to prevent unbounded map growth
// (becomes a memory leak after a year of running).
func (l *limiter) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) < 4096 { // only do this at scale to avoid iterating on every request
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// RateLimit per-IP rate limiting middleware. rps <= 0 means disabled (returns pass-through middleware).
//
// Why only by IP: this process has no account system (authentication is optional
// via external registration server); the only stable identifier available is IP.
// It can't block distributed port scanning, but it can block the most common abuse
// — "a script hammering the API" — especially file uploads and cross-node fetching,
// which actually cost money (bandwidth/disk).
func RateLimit(rps float64, burst int) gin.HandlerFunc {
	if rps <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	if burst <= 0 {
		burst = int(rps * 2)
		if burst < 1 {
			burst = 1
		}
	}
	l := &limiter{rps: rps, burst: burst, buckets: map[string]*bucket{}}
	return func(c *gin.Context) {
		// Preflight OPTIONS don't count: it's the browser's "knock" before sending
		// a real request; and 429'd preflights don't carry CORS headers, so the
		// frontend would just see a mysterious cross-origin error.
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		now := time.Now()
		ip := c.ClientIP()
		// Loopback and unknown sources are not rate-limited: the former is the admin
		// channel (/ws/peer internal forwarding tagged as 127.0.0.1, see Router.Engine),
		// the latter would only cause false positives — can't treat all requests where
		// IP can't be identified as the same attacker.
		if ip == "" || ip == "127.0.0.1" || ip == "::1" {
			c.Next()
			return
		}
		if !l.allow(ip, now) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":       "rate limit exceeded",
				"request_id":  RequestIDFrom(c),
				"retry_after": 1,
			})
			return
		}
		l.sweep(now)
		c.Next()
	}
}

// ── Request body guard (red-team R2) ────────────────────────────────────────

// LimitJSONBody caps the size of JSON request bodies.
//
// Why it exists: every c.ShouldBindJSON / c.GetRawData call reads the entire body
// into memory with no limit. There were 30+ such call sites, all unauthenticated
// except where AuthRequired happened to sit ahead, so any anonymous caller could
// POST a multi-gigabyte body and OOM the node. Multipart uploads already carry
// their own MaxUploadBytes cap, which is why only the JSON surface was uncapped.
//
// Only application/json-ish bodies are capped: multipart uploads, raw file
// streams, and the WebRTC/WS forwarding paths must not be truncated here, they
// have their own size handling. maxBytes <= 0 disables the cap.
func LimitJSONBody(maxBytes int64) gin.HandlerFunc {
	if maxBytes <= 0 {
		return func(c *gin.Context) { c.Next() }
	}
	return func(c *gin.Context) {
		ct := strings.ToLower(c.Request.Header.Get("Content-Type"))
		if strings.Contains(ct, "json") {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxBytes)
		}
		c.Next()
	}
}
