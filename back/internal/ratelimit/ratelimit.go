// Package ratelimit 提供与 HTTP 框架无关的按来源限流原语（令牌桶 + 登录退避）。
//
// 为什么单独一个包、而不是复用 router/middleware.go 里那个 RateLimit：
//
//  1. 那个是 **gin** 中间件（依赖 *gin.Context），而 reg-server 的入口是
//     标准 net/http 的 http.ServeMux。`peerdrive all` 模式下 /auth/* 与
//     /p2p/relay/* 由 services.UnifiedMux 直接接管，**根本不经过 gin engine**，
//     所以挂在 gin 上的那个中间件对它们完全无效——这正是 reg-server 公开端点
//     无限流的根因。要在这里生效，就得有一个不依赖 gin 的实现。
//  2. 独立进程 `peerdrive reg` 更彻底：它连 router 包都不 import。
//
// 所以这里是「纯 net/http 版本」的同一套算法，语义与 router/middleware.go 的
// limiter 保持一致（令牌桶、恒定速率回填、不超过桶容量、懒清扫空闲桶）。
package ratelimit

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// bucket 是单个来源的令牌桶状态。
type bucket struct {
	tokens float64
	last   time.Time
}

// Limiter 是一个按字符串来源键计数的令牌桶。
//
// 并发安全。零值不可用，请用 New 构造。
type Limiter struct {
	mu      sync.Mutex
	rps     float64
	burst   float64
	buckets map[string]*bucket

	// now 可在测试里替换以避免 sleep；生产为 nil 时用 time.Now。
	now func() time.Time
}

// New 构造一个令牌桶。rps <= 0 表示不限流（Allow 恒真）。
// burst <= 0 时按「2 秒的量」取值，且至少为 1——
// 桶太小会让正常的一次页面加载（并发请求多个资源）互相踩到。
func New(rps float64, burst int) *Limiter {
	if burst <= 0 {
		burst = int(rps * 2)
		if burst < 1 {
			burst = 1
		}
	}
	l := &Limiter{rps: rps, burst: float64(burst), buckets: map[string]*bucket{}}
	if l.rps <= 0 {
		l.buckets = nil // 不限流：一个桶都不用建
	}
	return l
}

// Enabled 报告该限流器是否真的生效。
func (l *Limiter) Enabled() bool { return l != nil && l.rps > 0 }

// Allow 取一个令牌；不足则返回 false。
//
// 回填是「恒定速率但不超过桶容量」：长时间不请求不会攒出一个巨大突发，
// 否则一个安静一小时的客户端回来时能一口气打满 burst+∞ 个请求。
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

// sweep 清掉长时间不活跃的来源，避免 map 无界增长（跑一年就是个内存泄漏）。
// 只在桶数量到量级时才真去遍历。
//
// ⚠️ 调用方必须已持有 l.mu：Allow 里在持锁状态下调用它，
// sync.Mutex 不可重入，这里不能再 Lock 一次。
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

// ---------------------------------------------------------------------------
// 中间件

// TooManyRequests 写 429 + Retry-After。分离出来是因为 WS 升级与普通 HTTP
// 都要用，但前者不能直接写 JSON（响应头一旦发出就不能再改状态码）。
func TooManyRequests(w http.ResponseWriter, retryAfter time.Duration) {
	secs := int(retryAfter.Seconds())
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
}

// ClientIP 取请求的来源 IP，**只认 RemoteAddr**。
//
// 刻意不读 X-Forwarded-For：这个头是客户端可伪造的，一旦采信，
// 攻击者每次换一个随机值就能让每个请求都落进「一个新桶」，
// 限流等于没加——比不限流更糟，因为它还给人虚假的安全感。
//
// 代价：部署在**同机** nginx 反代后面时，所有请求的 RemoteAddr 都是 127.0.0.1，
// 于是全网用户共用一个桶（整体被限）。这是刻意的取舍——
// 「限得偏紧」远好于「限不住」。真需要按真实客户端分桶时，
// 应当在反代层做限流，而不是在这里信任一个可伪造的头。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// RemoteAddr 没有端口（测试里的 httptest.NewRequest 就是这样）
		host = r.RemoteAddr
	}
	return strings.TrimSpace(host)
}

// ---------------------------------------------------------------------------
// 登录退避

// Backoff 按**账号**记录连续失败次数，超过阈值后指数退避。
//
// 为什么按账号而不是按 IP：口令爆破的判据是「这个账号被猜了多少次」，
// 攻击者换 IP 不改变它在猜同一个账号。按 IP 记，攻击者只要轮换出口
// 就能一直试下去（分布式爆破）而计数器永不增长。
//
// 已知的取舍（明说，不藏）：按账号退避意味着攻击者可以**故意锁死**某个
// 真实用户的登录（拒绝服务）。这是「账号级防爆破」的固有代价，
// 业界通行做法同样如此；缓解手段是把上限压住（见 MaxBackoff），
// 让受害者最多等几分钟就能自愈，而不是被永久锁在门外。
type Backoff struct {
	mu      sync.Mutex
	entries map[string]*backoffEntry
	// 阈值以下不计入退避，只计数——避免一次手滑就把人挡在门外。
	threshold int
	base      time.Duration
	max       time.Duration
	now       func() time.Time
}

type backoffEntry struct {
	fails int
	until time.Time // 退避窗口结束时间；零值 = 未在退避
	last  time.Time // 最近一次被碰的时间（Fail/RetryAfter 都更新），清扫用
}

// BackoffParams 是退避策略参数。
type BackoffParams struct {
	// Threshold：连续失败多少次后开始退避。默认 5。
	Threshold int
	// Base：首次退避的时长，每次失败翻倍。默认 15s。
	Base time.Duration
	// Max：退避上限。默认 5 分钟。
	Max time.Duration
}

// DefaultBackoff 返回项目默认的登录退避策略。
func DefaultBackoff() *Backoff {
	return NewBackoff(BackoffParams{})
}

// NewBackoff 构造退避器；零值参数走 DefaultBackoffParams。
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

// RetryAfter 返回该账号还需等待多久；0 表示现在可以试。
//
// 已过期的退避窗口顺带清零：等待结束后应当**重新给满额度**，
// 否则攻击者只要一直打，计数只增不减，等待时长会一路顶到 max 不下来。
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
	// 窗口已过：重置为可正常尝试，但**保留计数**，
	// 这样「每隔一会儿试一次」的攻击者仍会累积并再次进入退避。
	e.until = time.Time{}
	return 0
}

// Fail 记一次失败，返回此后应等待多久。
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
	b.sweep(now) // 持锁调用：Fail 已持有 b.mu
	return d
}

// Success 在登录成功时清空该账号的计数与退避窗口。
func (b *Backoff) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.entries, key)
}

// sweep 清理长期不活跃的账号条目：既不在退避窗口里、又超过一小时
// 没人碰过的账号，从 map 里删掉，避免无界增长。
//
// ⚠️ 调用方必须已持有 b.mu（与 Limiter.sweep 同理，不可重入）。
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
