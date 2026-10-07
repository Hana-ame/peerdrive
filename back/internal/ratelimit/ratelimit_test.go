package ratelimit

// 本包是 N2 的限流原语。它的测试重点是三件容易写错的事：
//  1. 令牌桶的**回填语义**（限得准，而不是一律拒绝）
//  2. login 退避的**计数与窗口重置**（等完能再进，而不是永久锁死）
//  3. ClientIP **不采信 X-Forwarded-For**（这是安全前提，不是性能问题）

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLimiterAllowsBurstThenThrottles(t *testing.T) {
	l := New(1, 3)
	// burst=3：前三次应当通过（桶是满的）
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d should have been allowed (burst)", i)
		}
	}
	// 第四次应当被拒——桶已被抽干且还没到回填时间
	if l.Allow("1.2.3.4") {
		t.Fatal("4th request should be rejected: burst exhausted and no refill yet")
	}
}

func TestLimiterRefillsOverTime(t *testing.T) {
	l := New(1, 2) // 每秒回填 1 个令牌
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }

	for i := 0; i < 2; i++ {
		if !l.Allow("ip") {
			t.Fatalf("burst request %d rejected", i)
		}
	}
	if l.Allow("ip") {
		t.Fatal("should be empty right after burst")
	}

	// 前进 1 秒 → 恰好回填 1 个令牌
	now = now.Add(time.Second)
	if !l.Allow("ip") {
		t.Error("should allow exactly one request 1s after exhaustion (1 rps)")
	}
	if l.Allow("ip") {
		t.Error("only one token should have refilled, second must still be rejected")
	}
}

func TestLimiterRefillCappedAtBurst(t *testing.T) {
	// 静默很久的客户端不应攒出「无限突发」——否则一次长空闲后能一口气打满。
	l := New(1, 3)
	now := time.Unix(0, 0)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		l.Allow("ip")
	}
	if l.Allow("ip") {
		t.Fatal("should be empty")
	}
	// 静默 1000 秒：回填被桶容量截断，最多只能拿到 burst 个令牌
	now = now.Add(1000 * time.Second)
	allowed := 0
	for i := 0; i < 10; i++ {
		if l.Allow("ip") {
			allowed++
		}
	}
	if allowed != 3 {
		t.Errorf("after long idle, allowed %d requests, want exactly burst (3)", allowed)
	}
}

func TestLimiterBucketsAreIndependentPerKey(t *testing.T) {
	l := New(1, 1)
	if !l.Allow("a") {
		t.Fatal("a should be allowed")
	}
	if l.Allow("a") {
		t.Fatal("a should be throttled")
	}
	// 另一个来源必须有**自己的**桶：限流不能被一个攻击者连带影响所有人
	if !l.Allow("b") {
		t.Error("b must have its own bucket — one abusive IP must not throttle others")
	}
}

func TestLimiterZeroRPSIsDisabled(t *testing.T) {
	// rps<=0 必须「恒放行」而不是「恒拒绝」或 panic。
	l := New(0, 0)
	if l.Enabled() {
		t.Error("rps=0 must report Enabled()==false")
	}
	for i := 0; i < 100; i++ {
		if !l.Allow("ip") {
			t.Fatalf("disabled limiter rejected request %d", i)
		}
	}
}

func TestNilLimiterIsSafe(t *testing.T) {
	// s.announceLim 等字段未配置时是 nil，handler 直接调 s.announceLim.allow(...)
	// 而不判空——nil 接收者必须安全（signalserver 里的写法，这里对齐同一约定）。
	var l *Limiter
	if l.Enabled() {
		t.Error("nil limiter must report disabled")
	}
	if !l.Allow("ip") {
		t.Error("nil limiter must allow (unlimited)")
	}
}

// ---- Backoff ----

func TestBackoffThresholdThenExponential(t *testing.T) {
	b := NewBackoff(BackoffParams{Threshold: 3, Base: time.Second, Max: time.Hour})
	// 阈值以下不进入退避（手滑不该被挡在门外）
	if d := b.Fail("u"); d != 0 {
		t.Errorf("fail 1: got %v, want 0 (below threshold)", d)
	}
	if d := b.Fail("u"); d != 0 {
		t.Errorf("fail 2: got %v, want 0 (below threshold)", d)
	}
	// 第 3 次触发退避
	d := b.Fail("u")
	if d != time.Second {
		t.Errorf("fail 3: got %v, want 1s (first backoff)", d)
	}
	if got := b.RetryAfter("u"); got <= 0 {
		t.Errorf("RetryAfter after trigger = %v, want > 0", got)
	}
}

func TestBackoffDoublesEachFailure(t *testing.T) {
	b := NewBackoff(BackoffParams{Threshold: 2, Base: time.Second, Max: time.Hour})
	b.Fail("u") // 1
	if d := b.Fail("u"); d != time.Second {
		t.Fatalf("2nd fail: got %v want 1s", d)
	}
	if d := b.Fail("u"); d != 2*time.Second {
		t.Errorf("3rd fail: got %v want 2s (doubled)", d)
	}
	if d := b.Fail("u"); d != 4*time.Second {
		t.Errorf("4th fail: got %v want 4s (doubled)", d)
	}
}

func TestBackoffCappedAtMax(t *testing.T) {
	b := NewBackoff(BackoffParams{Threshold: 1, Base: time.Second, Max: 8 * time.Second})
	for i := 0; i < 20; i++ {
		if d := b.Fail("u"); d > 8*time.Second {
			t.Fatalf("fail %d: got %v, exceeds max 8s", i, d)
		}
	}
}

func TestBackoffWindowExpiresAndAllowsRetry(t *testing.T) {
	b := NewBackoff(BackoffParams{Threshold: 2, Base: time.Second, Max: time.Hour})
	now := time.Unix(0, 0)
	b.now = func() time.Time { return now }

	b.Fail("u")
	b.Fail("u") // 进入 1s 退避
	if b.RetryAfter("u") <= 0 {
		t.Fatal("should be backing off")
	}
	// 窗口过后必须重新放行，否则一次误击就永久锁死账号
	now = now.Add(2 * time.Second)
	if d := b.RetryAfter("u"); d != 0 {
		t.Errorf("after window: got %v, want 0 (must be able to retry)", d)
	}
}

func TestBackoffSuccessClears(t *testing.T) {
	b := NewBackoff(BackoffParams{Threshold: 2, Base: time.Hour, Max: time.Hour})
	b.Fail("u")
	b.Fail("u")
	if b.RetryAfter("u") <= 0 {
		t.Fatal("should be in backoff")
	}
	// 登录成功必须清零，否则用户改对密码后仍然进不来
	b.Success("u")
	if d := b.RetryAfter("u"); d != 0 {
		t.Errorf("after success: got %v, want 0", d)
	}
}

func TestBackoffIsPerAccount(t *testing.T) {
	// 攻击者狂猜 "victim" 不应影响另一个账号 —— 退避是账号级的。
	b := NewBackoff(BackoffParams{Threshold: 1, Base: time.Hour, Max: time.Hour})
	for i := 0; i < 10; i++ {
		b.Fail("victim")
	}
	if b.RetryAfter("victim") <= 0 {
		t.Fatal("victim should be in backoff")
	}
	if d := b.RetryAfter("someone-else"); d != 0 {
		t.Errorf("unrelated account: got %v, want 0", d)
	}
}

// ---- ClientIP ----

func TestClientIPIgnoresForwardedFor(t *testing.T) {
	// 安全前提：X-Forwarded-For 由客户端提供，采信它等于让攻击者每次
	// 换一个新桶 —— 那比不限流更危险，因为它看起来像有防护。
	req := httptest.NewRequest("POST", "/auth/login", nil)
	req.RemoteAddr = "203.0.113.7:5555"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	if got := ClientIP(req); got != "203.0.113.7" {
		t.Errorf("ClientIP = %q, want %q (RemoteAddr only; XFF must be ignored)",
			got, "203.0.113.7")
	}
}

func TestClientIPWithoutPort(t *testing.T) {
	// httptest.NewRequest 的 RemoteAddr 没有端口，别在这里 panic。
	req := httptest.NewRequest("GET", "/ping", nil)
	req.RemoteAddr = "192.0.2.1"
	if got := ClientIP(req); got != "192.0.2.1" {
		t.Errorf("ClientIP = %q, want %q", got, "192.0.2.1")
	}
}

// ---- TooManyRequests ----

func TestTooManyRequestsSetsRetryAfter(t *testing.T) {
	rr := httptest.NewRecorder()
	TooManyRequests(rr, 3*time.Second)
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429", rr.Code)
	}
	// Retry-After 是客户端自我节流与可观测性的依据，缺了它调用方只能盲重试。
	if got := rr.Header().Get("Retry-After"); got != "3" {
		t.Errorf("Retry-After = %q, want %q", got, "3")
	}
}

func TestTooManyRequestsRetryAfterMinimumOneSecond(t *testing.T) {
	// 子秒等待取整成 0 会让客户端当成「立即重试」，等于没退避。
	rr := httptest.NewRecorder()
	TooManyRequests(rr, 100*time.Millisecond)
	if got := rr.Header().Get("Retry-After"); got != "1" {
		t.Errorf("Retry-After = %q, want %q (never below 1)", got, "1")
	}
}
