package signalserver

// N3 回归护栏：announce 与 WS 升级此前**完全没有速率限制**。
//
// 发现背景（2026-10-06）：本文件里已有的两类保护都容易被误当成限流——
//   · HandleWS 的 40KB 读上限 + 60s 读超时：**每连接**的上限，
//     不约束「有多少条连接」；每个被接受的升级都要一个 goroutine +
//     readLoop + clients 表项。断线重连还会让升级次数远大于在线连接数。
//   · announce 的 8KB body 上限 + 64 collection 上限：**每次请求**的上限，
//     不约束「有多少次请求」；而 announce 无认证（按设计），每次被接受
//     都往名册（disc / peerStats / peerColls / peerLinks）里写。
//
// 「每次请求有上限」不等于「有限流」：攻击者发 N 次小请求，每次都合法，
// 就能换到 N 条名册条目 / N 条连接。这条文件锁住「限流确实接上了」，
// 防止后来的重构把它悄悄摘掉。

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// limitedTestServer 起一个带限流的测试信令服务。
// rps 给得很大，burst 小：这样不用 sleep 就能打满桶。
func limitedTestServer(t *testing.T, burst int) (*Server, *httptest.Server) {
	t.Helper()
	return testServerWithOpts(t, WithRateLimit(RateLimitConfig{
		AnnounceRPS: 1000, AnnounceBurst: burst,
		WSRPS: 1000, WSBurst: burst,
		IDRPS: 1000, IDBurst: burst,
	}))
}

// TestAnnounceIsRateLimited 锁住 announce 的限流。
//
// 修复前这里会是一串 200 —— 任意调用方可以无限往名册里塞条目。
func TestAnnounceIsRateLimited(t *testing.T) {
	_, hs := limitedTestServer(t, 3)

	post := func(peerID string) int {
		resp, err := http.Post(hs.URL+"/announce", "application/json",
			strings.NewReader(`{"peerId":"`+peerID+`","collections":["coll"]}`))
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	// 前 3 次在 burst 内，应当成功
	for i := 0; i < 3; i++ {
		assert.Equal(t, http.StatusOK, post("node-"+string(rune('a'+i))),
			"announce within burst should succeed")
	}
	// 第 4 次必须被限
	assert.Equal(t, http.StatusTooManyRequests, post("node-z"),
		"announce beyond burst must be 429 — unlimited announce = unbounded roster growth")
}

// TestWSUpgradeIsRateLimited 锁住 WS 升级的限流。
//
// 这里只断言 HTTP 状态码：被限流时升级尚未发生，所以返回的是真正的
// 429（而不是「连上了再被关掉」）。这是有意选的失败模式——
// 连接后再关，客户端只看到「莫名断开」，排查成本高得多。
func TestWSUpgradeIsRateLimited(t *testing.T) {
	_, hs := limitedTestServer(t, 3)

	// 直接打 upgrade 端点（不真的完成 WS 握手就能拿到状态码）：
	// 每次请求都是一次升级尝试。
	tryUpgrade := func() int {
		resp, err := http.Get(hs.URL + "/peerjs?id=n1&token=t1&key=testkey")
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	for i := 0; i < 3; i++ {
		tryUpgrade() // burst 内，具体状态码（400/101）不重要
	}
	assert.Equal(t, http.StatusTooManyRequests, tryUpgrade(),
		"WS upgrade beyond burst must be 429")
}

// TestHandleIDIsRateLimitedSeparately 锁住 /id 有**独立的**桶。
//
// 为什么要分桶：一次页面加载是「先 GET /id，再开 WS」。共用一个桶的话，
// 面板刷新时可能因为自己的 socket 正在重连而被 /id 拒掉——
// 两个本该互不相干的路径互相拖累。
func TestHandleIDIsRateLimitedSeparately(t *testing.T) {
	// id 桶设为 2，ws 桶故意设很大
	srv := NewServer("testkey", WithRateLimit(RateLimitConfig{
		IDRPS: 1000, IDBurst: 2,
		WSRPS: 1000, WSBurst: 100,
	}))
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.HandleID(w, r)
	}))
	defer hs.Close()

	get := func() int {
		resp, err := http.Get(hs.URL)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}
	assert.Equal(t, http.StatusOK, get())
	assert.Equal(t, http.StatusOK, get())
	assert.Equal(t, http.StatusTooManyRequests, get(), "id bucket exhausted")
}

// TestNoRateLimitByDefault 锁住「默认不限流」。
//
// 这是**兼容性**护栏，不是安全护栏：本模块是独立发布的
// （github.com/Hana-ame/go-peerserver），不少使用方已经在 nginx 层限流。
// 静默地给它们套一个默认限额会是破坏性变更，所以必须由调用方显式开启。
func TestNoRateLimitByDefault(t *testing.T) {
	_, hs := testServer(t)

	// 打远超任何默认 burst 的次数，全部应当通过
	for i := 0; i < 25; i++ {
		resp, err := http.Post(hs.URL+"/announce", "application/json",
			strings.NewReader(`{"peerId":"n","collections":["c"]}`))
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode,
			"without WithRateLimit the server must stay unlimited (opt-in compatibility)")
	}
}

// TestNilBucketAllows 锁住 nil 桶的语义。
//
// WithRateLimit 里每个字段独立判定，所以未配置的桶就是 nil，
// handler 直接调 .allow(...) 而不判空——nil 接收者必须放行。
func TestNilBucketAllows(t *testing.T) {
	var b *tokenBucket
	assert.True(t, b.allow("1.2.3.4"), "nil bucket must allow (that bucket is not rate limited)")
}

// TestRateLimitIgnoresForwardedFor 锁住 ClientIP 的安全前提。
//
// clientIP 只读 RemoteAddr。若改去采信 X-Forwarded-For，攻击者每次换一个
// 值就能进一个新桶——那比不限流更糟，因为它看起来像有防护。
func TestRateLimitIgnoresForwardedFor(t *testing.T) {
	_, hs := limitedTestServer(t, 2)

	post := func(hdr string) int {
		req, err := http.NewRequest("POST", hs.URL+"/announce",
			strings.NewReader(`{"peerId":"n","collections":["c"]}`))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		if hdr != "" {
			req.Header.Set("X-Forwarded-For", hdr)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()
		return resp.StatusCode
	}

	post("")
	post("") // 桶已满（burst=2）
	// 即使带上伪造的 XFF，也**必须**继续被限——说明它没被采信
	assert.Equal(t, http.StatusTooManyRequests, post("9.9.9.9"),
		"X-Forwarded-For must not create a fresh bucket — it is client-controlled")
}

// TestTokenBucketRefill 对 tokenBucket 本身做单元测试
// （serve 路径上的时间不等人，这里用可注入的时钟）。
func TestTokenBucketRefill(t *testing.T) {
	now := int64(0)
	b := newTokenBucket(1, 2) // 1 rps, burst 2
	b.now = func() time.Time { return time.Unix(now, 0) }

	assert.True(t, b.allow("ip"))
	assert.True(t, b.allow("ip"))
	assert.False(t, b.allow("ip"), "bucket should be empty")

	now += 1 // 1 秒后回填 1 个令牌
	assert.True(t, b.allow("ip"), "one token should have refilled after 1s at 1rps")
	assert.False(t, b.allow("ip"), "only one token refilled")
}

// TestTokenBucketBurstCapped 锁住「静默很久不会攒出巨大突发」。
func TestTokenBucketBurstCapped(t *testing.T) {
	now := int64(0)
	b := newTokenBucket(1, 3)
	b.now = func() time.Time { return time.Unix(now, 0) }

	for i := 0; i < 3; i++ {
		b.allow("ip")
	}
	assert.False(t, b.allow("ip"))

	now += 10000 // 静默 10000 秒：回填被容量截断
	allowed := 0
	for i := 0; i < 20; i++ {
		if b.allow("ip") {
			allowed++
		}
	}
	assert.Equal(t, 3, allowed, "after long idle, at most burst tokens should be available")
}
