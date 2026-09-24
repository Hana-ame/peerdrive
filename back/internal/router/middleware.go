// Package router 的横切中间件：请求 ID、结构化访问日志、安全响应头、每 IP 限流。
//
// 为什么单独一个文件：这四个都是"每个请求都要走一遍、但不带任何业务逻辑"的
// 东西。混进 router.go（已经有 380 行路由注册）会让"这条路由挂了什么中间件"
// 变得没法一眼看清，而那正是安全审计时最需要看清的东西。
//
// 挂载顺序（在 SetupRouter 里，顺序即执行顺序）：
//   RequestID → SecurityHeaders → AccessLog → RateLimit → CORS → 路由
// 请求 ID 必须最早（后面所有日志都要带上它），限流必须在业务前但要在日志后
// （被限流的请求也要留痕，否则攻击流量反而最安静）。

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

// HeaderRequestID 请求 ID 的 HTTP 头名与上下文键名。
// 上游（反向代理）传来的同名头直接沿用，这样一条请求能跨服务串起来。
const HeaderRequestID = "X-Request-ID"

// RequestID 为每个请求分配一个 ID：沿用上游的 X-Request-ID，没有才生成。
//
// 为什么需要：出问题时日志里有几十个并发请求交织，没有 ID 就只能靠时间戳猜
// 哪些行属于同一次调用。有了它，用户报一个 ID 就能捞出这一条请求的全部日志。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.GetHeader(HeaderRequestID)
		if strings.TrimSpace(id) == "" {
			id = newRequestID()
		} else if len(id) > 128 { // 防日志注入/超长头把日志行撑爆
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
		// 随机源不可用时退回时间戳：ID 只要求"这次和别次不一样"，不要求不可预测。
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(b[:])
}

// RequestIDFrom 取出中间件写入上下文的请求 ID（供 controller 记日志用）。
func RequestIDFrom(c *gin.Context) string {
	if v, ok := c.Get("request_id"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// SecurityHeaders 设置一组默认安全响应头。
//
// CSP 默认开，但 /swagger/* 例外：Swagger UI 的官方实现依赖 inline script 与
// eval，套上 CSP 就是一片白屏。管理台页面不由本进程提供（front 独立部署），
// 所以严格 CSP 对业务没有副作用。真出兼容问题可用 PEERDRIVE_CSP=off 关掉。
func SecurityHeaders(disableCSP bool) gin.HandlerFunc {
	csp := "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Cross-Origin-Opener-Policy", "same-origin")
		if !disableCSP && !strings.HasPrefix(c.Request.URL.Path, "/swagger") {
			c.Header("Content-Security-Policy", csp)
		}
		c.Next()
	}
}

// accessLogEntry 一条访问日志的结构（字段名对齐常见日志系统约定）。
//
// 刻意**不记录**的东西：Authorization 头、查询串里的 token、请求体。
// 日志会被到处复制（贴到 issue、发到群里），一旦带上凭据就等于二次泄露。
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

// AccessLog 输出一行 JSON 访问日志（级别按状态码：5xx=ERROR，4xx=WARN，其余 INFO）。
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

// ── 每 IP 限流（令牌桶）──

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

// allow 取一个令牌；桶按时间匀速补充（经典令牌桶）。
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: float64(l.burst), last: now}
		l.buckets[key] = b
	}
	// 匀速补充，但不超过桶容量（否则长时间不请求会攒出一次爆发）
	b.tokens = math.Min(float64(l.burst), b.tokens+now.Sub(b.last).Seconds()*l.rps)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// sweep 清理长期不活跃的来源，防止 map 无上限增长（跑一年下来变成内存泄漏）。
func (l *limiter) sweep(now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) < 4096 { // 只有规模上来才做，避免每次请求都遍历
		return
	}
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// RateLimit 每 IP 限流中间件。rps <= 0 表示不启用（返回透传中间件）。
//
// 为什么只按 IP：本进程没有账号体系（认证是可选的外部注册服务器），能拿到的
// 稳定标识只有 IP。它挡不住分布式扫端口，但能挡住"一个脚本刷爆接口"这类
// 最常见的滥用——尤其是文件上传和跨节点拉取这两个会真花钱（带宽/磁盘）的口子。
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
		// 预检 OPTIONS 不计数：它是浏览器发请求前的"敲门"，不是真请求；而且
		// 被 429 掉的预检不携带 CORS 头，前端只会看到一句莫名其妙的跨域错误。
		if c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		now := time.Now()
		ip := c.ClientIP()
		// 本机回环与未知来源不限流：前者是管理通道（/ws/peer 内部转发标成
		// 127.0.0.1，见 SetupRouter），后者限了也只会误伤——总不能把所有识别
		// 不出 IP 的请求当成同一个攻击者在打。
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
