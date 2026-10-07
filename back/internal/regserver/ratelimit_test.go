package regserver

// N2 回归护栏：register / login / relay 这三个**无认证**端点此前完全没有限流。
//
// 发现背景（2026-10-06）：项目里唯一的限流实现是 gin 中间件
// （router/middleware.go:224），只挂在 router.go:55 的 gin engine 上。
// 而这批端点根本不经过 gin：
//   · 独立 `peerdrive reg` 是纯 net/http 进程（srv.Serve → s.Handler()）
//   · `peerdrive all` 模式下由 services.UnifiedMux 直接从 reg.Handler() 搬路由
// 于是它们从未被限过：可无限刷号（register 每次都跑 bcrypt cost10）、
// 可爆破（login 无退避）、可伪造 peer_id 污染中继名录（relay 是幂等 upsert）。
//
// 这些测试存在的意义是「锁住限流确实生效」——因为限流代码最典型的失效方式
// 是被后来的重构悄悄摘掉（换 handler 装配、或在 Serve 而非 Handler 里挂）。
// 注意它们全部走 srv.Handler()，与生产走的是同一个入口。

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"peerdrive/internal/ratelimit"
)

// ratelimitForTest 造一个测试用的令牌桶。
//
// rps 取 **0.01** 而不是「一个大数」：令牌桶按真实经过的时间回填。
// 本包最贵的被放行请求是 register（真跑 bcrypt cost10，约 60-100ms），
// 若 rps 给到 2，三次 bcrypt 的耗时就能回填约半个令牌，测试会变得依赖机器快慢。
// 0.01 rps 时两次请求之间最多回填 0.001 个令牌，可确定性地认为桶不会自行恢复。
func ratelimitForTest(rps float64, burst int) *ratelimit.Limiter {
	return ratelimit.New(rps, burst)
}

// ratelimitBackoffForTest 造一个低阈值的退避器，默认参数会拖慢测试。
func ratelimitBackoffForTest(threshold int) *ratelimit.Backoff {
	return ratelimit.NewBackoff(ratelimit.BackoffParams{
		Threshold: threshold,
		Base:      time.Hour, // 一旦触发就足以观察，不真的去等
		Max:       time.Hour,
	})
}

// newLimitedServer 起一个 Server，并把三个桶都调到「测试可打满」的大小。
//
// 为什么给一个测试专用的构造函数而不是改环境变量：默认值（register 0.5rps）
// 要打过几十个请求才能触发，那会让测试要么很慢、要么依赖 sleep；
// 而**验证的是「限流是否生效」，不是「默认值是否好看」**——默认值由
// cmd/peerdrive 的文档与 flag 默认值负责。
//
// ⚠️ 桶和退避器必须在调用 srv.Handler() **之前**换好：Handler() 把
// s.loginLimiter 这类指针按值捕获进闭包，之后再改 s.* 字段不会生效。
// 之前就踩过这个：测试里替换了桶但 Handler() 还握着旧的 burst=3，
// 于是第 4 个请求被旧桶挡成 429，误判成「退避没清零」。
func newLimitedServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	srv, err := New(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	srv.registerLimiter = ratelimitForTest(0.01, 3)
	srv.loginLimiter = ratelimitForTest(0.01, 3)
	srv.relayLimiter = ratelimitForTest(0.01, 3)
	return srv
}

// withBackoff 重设 login 退避器。必须在 srv.Handler() 之前调用（见 newLimitedServer）。
func withBackoff(srv *Server, threshold int) {
	srv.loginBackoff = ratelimitBackoffForTest(threshold)
	// 同时把 IP 桶调到 burst 20 以**隔离变量**：否则第 3 次尝试会被 IP 桶
	// 挡成 429，测试就分不清这个 429 来自退避还是限流——而这两者的修复动机
	// 完全不同。同理必须在 Handler() 之前生效。
	srv.loginLimiter = ratelimitForTest(0.01, 20)
}

// doFrom 从指定 IP 发请求。限流是按来源 IP 分桶的，所以必须能控制 RemoteAddr，
// 否则同一进程里所有请求都落在 httptest 的默认地址 192.0.2.1 上，
// 测不出「不同来源互不影响」。
func doFrom(mux *http.ServeMux, ip, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = ip + ":1234"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// TestRegisterIsRateLimited 锁住 register 的限流。
//
// 突发 3 次之后必须出现 429 —— 修复前这里是 200/200/200/200…无限延续。
func TestRegisterIsRateLimited(t *testing.T) {
	srv := newLimitedServer(t)
	mux := srv.Handler().(*http.ServeMux)

	var last *httptest.ResponseRecorder
	for i := 0; i < 6; i++ {
		last = doFrom(mux, "10.0.0.1", "POST", "/auth/register",
			`{"username":"u`+string(rune('a'+i))+`","password":"pw123456"}`, "")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("after burst, register got %d, want 429 "+
			"(unlimited register = anyone can create accounts at will, each costing a bcrypt)", last.Code)
	}
	if last.Header().Get("Retry-After") == "" {
		t.Error("429 should carry Retry-After so clients can self-throttle")
	}
}

// TestLoginIsRateLimited 锁住 login 的 IP 级限流（退避是另一层，见下一个测试）。
func TestLoginIsRateLimited(t *testing.T) {
	srv := newLimitedServer(t)
	mux := srv.Handler().(*http.ServeMux)
	_ = doFrom(mux, "10.0.0.2", "POST", "/auth/register", `{"username":"carol","password":"pw123456"}`, "")

	var last *httptest.ResponseRecorder
	for i := 0; i < 8; i++ {
		last = doFrom(mux, "10.0.0.2", "POST", "/auth/login", `{"username":"carol","password":"wrong"}`, "")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("after burst, login got %d, want 429 (was: unlimited password guessing)", last.Code)
	}
}

// TestLoginBackoffKicksInAfterRepeatedFailures 锁住**账号级退避**。
//
// 这一条比 IP 限流更重要：IP 限流挡不住分布式爆破（换源 IP 就绕开），
// 而退避是按**账号**记的——攻击者换 IP 仍在撞同一个账号，计数继续涨。
//
// 用低阈值是因为默认阈值 5 次要打 5 次 bcrypt（每次 ~60-100ms），
// 测试里已经很慢了；这里直接构造低阈值的 Backoff，验证的是机制而非默认值。
func TestLoginBackoffKicksInAfterRepeatedFailures(t *testing.T) {
	srv := newLimitedServer(t)
	// 阈值 3、base 1 小时：一旦触发就回 429 而不是 401。
	// withBackoff 必须先于 Handler()——见 newLimitedServer 的说明。
	withBackoff(srv, 3)
	mux := srv.Handler().(*http.ServeMux)
	_ = doFrom(mux, "10.0.0.3", "POST", "/auth/register", `{"username":"dave","password":"pw123456"}`, "")

	// 前两次：阈值以下 → 401（凭据错）
	for i := 0; i < 2; i++ {
		if rr := doFrom(mux, "10.0.0.3", "POST", "/auth/login", `{"username":"dave","password":"wrong"}`, ""); rr.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d want 401 (below backoff threshold)", i+1, rr.Code)
		}
	}
	// 第 3 次：触发退避 → 429
	rr := doFrom(mux, "10.0.0.3", "POST", "/auth/login", `{"username":"dave","password":"wrong"}`, "")
	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("triggering attempt: got %d want 429 (backoff should engage)", rr.Code)
	}
	if rr.Header().Get("Retry-After") == "" {
		t.Error("backoff 429 must carry Retry-After")
	}

	// **关键**：退避期内，即使换 IP、即使密码正确，也必须被挡住。
	// 不挡住的话，攻击者可以用正确的密码探出「这个账号存在」，
	// 而且退避形同虚设（换个来源就重置了）。
	rr = doFrom(mux, "10.9.9.9", "POST", "/auth/login", `{"username":"dave","password":"pw123456"}`, "")
	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("during backoff, correct password from a different IP got %d, want 429 "+
			"(backoff is per-account: changing source IP must not bypass it)", rr.Code)
	}
}

// TestLoginBackoffClearedBySuccess 锁住「登录成功即清零」——
// 否则用户改对密码后仍进不来（永久锁在门外）。
func TestLoginBackoffClearedBySuccess(t *testing.T) {
	srv := newLimitedServer(t)
	withBackoff(srv, 3) // 同上：必须先于 Handler()
	mux := srv.Handler().(*http.ServeMux)
	_ = doFrom(mux, "10.0.0.4", "POST", "/auth/register", `{"username":"erin","password":"pw123456"}`, "")

	for i := 0; i < 2; i++ {
		doFrom(mux, "10.0.0.4", "POST", "/auth/login", `{"username":"erin","password":"wrong"}`, "")
	}
	// 这次用正确密码 → 应成功，且清零计数
	if rr := doFrom(mux, "10.0.0.4", "POST", "/auth/login", `{"username":"erin","password":"pw123456"}`, ""); rr.Code != http.StatusOK {
		t.Fatalf("correct password got %d, want 200", rr.Code)
	}
	// 清零后再错一次，应当只回到 401 而**不是** 429（说明计数真的清了）
	rr := doFrom(mux, "10.0.0.4", "POST", "/auth/login", `{"username":"erin","password":"wrong"}`, "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("after a successful login, one failure got %d want 401 "+
			"(backoff counter must be cleared by Success)", rr.Code)
	}
}

// TestRelayRegisterIsRateLimited 锁住 relay 登记的限流。
//
// 无认证 + 幂等 upsert + peer_id 是主键：不限流时任何人都能以任意 peer_id
// 反复写入/覆盖中继名录（伪造节点、抹掉真实节点的地址）。
func TestRelayRegisterIsRateLimited(t *testing.T) {
	srv := newLimitedServer(t)
	mux := srv.Handler().(*http.ServeMux)

	var last *httptest.ResponseRecorder
	for i := 0; i < 8; i++ {
		last = doFrom(mux, "10.0.0.5", "POST", "/p2p/relay/register",
			`{"peer_id":"fake","addrs":["6.6.6.6:1"]}`, "")
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("after burst, relay/register got %d, want 429 "+
			"(unlimited unauthenticated upsert = anyone can forge the relay roster)", last.Code)
	}
}

// TestRateLimitIsPerSourceIP 锁住「按 IP 分桶」。
//
// 单个滥用来源不能连带影响其他来源——这是限流能安全默认开启的前提。
func TestRateLimitIsPerSourceIP(t *testing.T) {
	srv := newLimitedServer(t)
	mux := srv.Handler().(*http.ServeMux)

	// 把 10.0.0.6 的 relay 桶打空
	for i := 0; i < 5; i++ {
		doFrom(mux, "10.0.0.6", "POST", "/p2p/relay/register", `{"peer_id":"p","addrs":[]}`, "")
	}
	if rr := doFrom(mux, "10.0.0.6", "POST", "/p2p/relay/register", `{"peer_id":"p","addrs":[]}`, ""); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("precondition: abusive IP should be throttled, got %d", rr.Code)
	}
	// 另一个来源必须照常放行
	if rr := doFrom(mux, "10.0.0.7", "POST", "/p2p/relay/register", `{"peer_id":"p2","addrs":[]}`, ""); rr.Code != http.StatusOK {
		t.Errorf("a different source got %d want 200 — one abusive IP must not throttle everyone", rr.Code)
	}
}

// TestPingAndReadOnlyEndpointsAreNotThrottled 锁住「限流不误伤」的一面。
//
// 探活与只读查询必须始终可用：把它们一并限住会让「限流」变成
// 「服务不可用」，且排障时最需要的正是 /ping 能通。
func TestPingAndReadOnlyEndpointsAreNotThrottled(t *testing.T) {
	srv := newLimitedServer(t)
	mux := srv.Handler().(*http.ServeMux)

	for i := 0; i < 30; i++ {
		if rr := doFrom(mux, "10.0.0.8", "GET", "/ping", "", ""); rr.Code != http.StatusOK {
			t.Fatalf("ping %d got %d, want 200 (liveness probe must never be throttled)", i, rr.Code)
		}
		if rr := doFrom(mux, "10.0.0.8", "GET", "/p2p/relay/list", "", ""); rr.Code != http.StatusOK {
			t.Fatalf("relay list %d got %d, want 200 (read-only query must not be throttled)", i, rr.Code)
		}
	}
}
