package services

// 单二进制的核心断言：三个组件在同一个进程里能各自独立跑，也能合并到一个端口。
//
// 这些用例的价值在于「防止回归」：`peerdrive all` 是新加的运行模式，一旦
// 路由前缀与主服务撞车，表现是静默的 404 或鉴权绕过——不会 crash，只是不对。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"
	"testing"
)

// TestSignalRoutesIndependent 单独跑信令时，全部 6 条路由都要在。
func TestSignalRoutesIndependent(t *testing.T) {
	mux := SignalHandler("k", "", "")
	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, p := range []string{"/peerjs", "/peerjs/id", "/discover/announce",
		"/discover/leave", "/discover/nodes", "/status"} {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", p, err)
		}
		resp.Body.Close()
		// 405/400 说明路由在（方法不匹配）；404 才是路由缺失。
		if resp.StatusCode == http.StatusNotFound {
			t.Errorf("route %s not registered (404)", p)
		}
	}
}

// TestRegPatternsComplete 路由清单不能漏——漏一条等于该功能静默消失。
func TestRegPatternsComplete(t *testing.T) {
	if len(RegPatterns) != 9 {
		t.Errorf("RegPatterns has %d entries, want 9:\n%s",
			len(RegPatterns), strings.Join(RegPatterns, "\n"))
	}
	seen := map[string]bool{}
	for _, p := range RegPatterns {
		if seen[p] {
			t.Errorf("duplicate pattern %q", p)
		}
		seen[p] = true
	}
	for _, want := range []string{
		"GET /ping", "POST /auth/register", "GET /auth/whoami",
		"POST /p2p/relay/register", "GET /p2p/relay/list",
	} {
		if !seen[want] {
			t.Errorf("missing pattern %q", want)
		}
	}
}

// TestUnifiedMuxAllThreeMounted 合并模式下，三方的路由必须各归其位。
//
// 这是「四个仓合成一个二进制」是否真的成立的判据：同一个端口上，
// 主服务、信令、注册服务都要能响应，且互不吞路由。
func TestUnifiedMuxAllThreeMounted(t *testing.T) {
	t.Setenv("JWT_SECRET", "unified-test-secret")
	t.Setenv("DB_PATH", t.TempDir()+"/reg.db")

	// 主服务用一个最小 handler：只对 /api-echo 响应，其余 404。
	mainSvc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api-echo" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("main"))
			return
		}
		http.NotFound(w, r)
	})

	mux, reg, err := UnifiedMux(nil, mainSvc)
	if err != nil {
		t.Fatalf("UnifiedMux: %v", err)
	}
	defer reg.Close()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 注册服务：无 token 的 /auth/whoami 应为 401（说明路由到了注册服务），
	// 而不是 404（路由没到）。/ping 不需要鉴权，应为 200。
	t.Run("reg-routes-reach-regserver", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/auth/whoami")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Error("/auth/whoami is 404 — the unified mux did not route it to the reg service")
		}
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("/auth/whoami without token = %d, want 401", resp.StatusCode)
		}
	})

	t.Run("reg-ping-moved-under-prefix", func(t *testing.T) {
		// 主服务自己也有 GET /ping（controller.Ping -> "pong"）。
		// 注册服务的同名路由必须改挂到 /_reg/ping，否则静默换语义。
		resp, err := http.Get(srv.URL + regPrefix + "/ping")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/ping = %d, want 200", resp.StatusCode)
		}
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["service"] != "peerdrive-registration" {
			t.Errorf("service = %q, want peerdrive-registration", body["service"])
		}
	})

	t.Run("signal-routes-reach-signalserver", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/status")
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			t.Error("/status is 404 — the unified mux did not route it to the signal service")
		}
	})

	t.Run("main-service-keeps-fallback", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/api-echo")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("/api-echo = %d, want 200 (main service must keep the fallback route)", resp.StatusCode)
		}
	})
}

// TestUnifiedMuxNoRouteShadowing 合并模式下，注册服务的路由一条都不能落到
// 主服务的兜底 "/" 上。
//
// ⚠️ 这里曾经写过一版断言「注册顺序错了就会失败」的测试，负向对照发现它
// **永远绿**——因为 Go 1.22 的 ServeMux 是按「模式具体度」而非注册顺序决定
// 优先级（实测：先注册 "/" 再注册 "GET /auth/whoami"，后者照样命中）。
// 那版测试什么都没测到，已删。
//
// 现在测的是真正会咬人的失效模式：某条注册路由被漏挂（UnifiedMux 里少了一行
// mux.Handle），请求就会落到主服务的兜底上——表现是静默 404 或 418，
// 不 crash、不报错，只是不对。
func TestUnifiedMuxNoRouteShadowing(t *testing.T) {
	t.Setenv("JWT_SECRET", "unified-test-secret")
	t.Setenv("DB_PATH", t.TempDir()+"/reg.db")

	mainSvc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot) // 主服务对一切非自己路由都返回 418
		w.Write([]byte("main-fallback"))
	})
	mux, reg, err := UnifiedMux(nil, mainSvc)
	if err != nil {
		t.Fatalf("UnifiedMux: %v", err)
	}
	defer reg.Close()

	srv := httptest.NewServer(mux)
	defer srv.Close()

	for _, tc := range []struct {
		method, path string
		want         int // 只断言「不是主服务的 418」与具体期望
	}{
		{"GET", "/auth/whoami", http.StatusUnauthorized},
		{"GET", "/auth/list", http.StatusUnauthorized},
		{"GET", "/p2p/relay/list", http.StatusOK},
		{"GET", regPrefix + "/ping", http.StatusOK},
	} {
		req, _ := http.NewRequest(tc.method, srv.URL+tc.path, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTeapot {
			t.Errorf("%s %s fell through to the main service fallback — route shadowed",
				tc.method, tc.path)
		}
		if resp.StatusCode != tc.want {
			t.Errorf("%s %s = %d, want %d", tc.method, tc.path, resp.StatusCode, tc.want)
		}
	}
}

// TestUnifiedTwoRegServersCoexist 单进程起两个注册服务（不同库）互不干扰。
//
// 这是把全局状态改成实例字段的直接收益，也是「同进程合并」的前提——
// 包级全局 db 时第二个 New 会覆盖第一个，两个服务共用一份数据。
func TestUnifiedTwoRegServersCoexist(t *testing.T) {
	t.Setenv("JWT_SECRET", "two-instance-secret")
	dir := t.TempDir()

	aSrv, err := RegHandler(dir + "/a.db")
	if err != nil {
		t.Fatalf("first RegHandler: %v", err)
	}
	defer aSrv.Close()
	bSrv, err := RegHandler(dir + "/b.db")
	if err != nil {
		t.Fatalf("second RegHandler: %v", err)
	}
	defer bSrv.Close()
	a, b := aSrv.Handler(), bSrv.Handler()

	post := func(srv http.Handler, user string) {
		t.Helper()
		body := strings.NewReader(`{"username":"` + user + `","password":"pw123456"}`)
		req, _ := http.NewRequest("POST", "/auth/register", body)
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, req)
		if w.Code != http.StatusOK && w.Code != http.StatusCreated {
			t.Fatalf("register %s = %d: %s", user, w.Code, w.Body.String())
		}
	}
	post(a, "alice")
	post(b, "bob")

	// a 的用户列表里不该出现 bob
	listA := httptest.NewRecorder()
	tok := login(t, a, "alice")
	req, _ := http.NewRequest("GET", "/auth/list", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	a.ServeHTTP(listA, req)
	if listA.Code != http.StatusOK {
		t.Fatalf("/auth/list = %d: %s", listA.Code, listA.Body.String())
	}
	if strings.Contains(listA.Body.String(), "bob") {
		t.Errorf("server A leaked user from server B — instances share state:\n%s", listA.Body.String())
	}
}

func login(t *testing.T, h http.Handler, user string) string {
	t.Helper()
	req, _ := http.NewRequest("POST", "/auth/login",
		strings.NewReader(`{"username":"`+user+`","password":"pw123456"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login %s = %d: %s", user, w.Code, w.Body.String())
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Token
}

// TestServeHTTPRequiresBothTLSArgs 只给一半 TLS 参数必须报错，不能静默降级成明文。
//
// ⚠️ 必须带超时守卫：删掉那处校验后，ServeHTTP 会一路走到 http.ListenAndServe
// 真的去监听端口，测试会**挂住**而不是失败（实测负向对照：挂了 600s 直到
// go test 超时）。挂住的测试没有诊断信息，只会把 CI 占满，所以这里用
// goroutine + select 把它变成一条明确的失败。
func TestServeHTTPRequiresBothTLSArgs(t *testing.T) {
	done := make(chan error, 1)
	go func() {
		done <- ServeHTTP("127.0.0.1:0", "/tmp/nope.pem", "", http.NotFoundHandler())
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ServeHTTP accepted cert without key — would silently serve plain HTTP")
		}
		if !strings.Contains(err.Error(), "both") {
			t.Errorf("error should mention that both are required, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ServeHTTP did not return: with cert-only it fell through to ListenAndServe " +
			"and is now blocking on a real port — the both-required check is gone")
	}
	_ = os.Remove("/tmp/nope.pem")
}

// TestConflictingRegRouteMovedToPrefix 合并模式下，主服务与注册服务的同名路由
// 不能静默换语义。
//
// 实测踩到的：主服务 router.go:234 有 `r.GET("/ping", controller.Ping)` 返回
// "pong"，注册服务也有 `GET /ping` 返回 {"service":"peerdrive-registration"}。
// 谁先被匹配谁生效——注册服务赢，主服务的 /ping 变成另一个响应体。
// 不 crash、不报错，监控只看「/ping 通了」，脚本才会炸。
//
// 这里钉死两件事：/ping 归主服务，注册服务的 ping 在 /_reg/ping 仍可达。
func TestConflictingRegRouteMovedToPrefix(t *testing.T) {
	t.Setenv("JWT_SECRET", "conflict-test-secret")
	t.Setenv("DB_PATH", t.TempDir()+"/reg.db")

	mainSvc := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 模拟 controller.Ping：只有 /ping 有意义，其余 404。
		if r.URL.Path == "/ping" {
			w.Write([]byte("pong"))
			return
		}
		http.NotFound(w, r)
	})
	mux, reg, err := UnifiedMux(nil, mainSvc)
	if err != nil {
		t.Fatalf("UnifiedMux: %v", err)
	}
	defer reg.Close()
	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("main-ping-not-shadowed", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/ping")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("/ping = %d, want 200 (it belongs to the main service)", resp.StatusCode)
		}
		if string(body) != "pong" {
			t.Errorf("/ping body = %q, want %q — the reg service shadowed the main service",
				string(body), "pong")
		}
	})

	t.Run("reg-ping-still-reachable-under-prefix", func(t *testing.T) {
		resp, err := http.Get(srv.URL + regPrefix + "/ping")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s/ping = %d, want 200", regPrefix, resp.StatusCode)
		}
		var body map[string]string
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["service"] != "peerdrive-registration" {
			t.Errorf("service = %q, want peerdrive-registration", body["service"])
		}
	})
}

// TestWithPrefixKeepsMethod withPrefix 不能靠切片拼路径——
// 方法名长度一变（POST/DELETE 比 GET 长）就拼错，且会悄悄丢掉方法词。
func TestWithPrefixKeepsMethod(t *testing.T) {
	cases := []struct{ in, want string }{
		{"GET /ping", "GET /_reg/ping"},
		{"POST /auth/register", "POST /_reg/auth/register"},
		{"DELETE /a-very-long-path", "DELETE /_reg/a-very-long-path"},
		{"/no-method", "/_reg/no-method"},
	}
	for _, c := range cases {
		if got := withPrefix("/_reg", c.in); got != c.want {
			t.Errorf("withPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripPrefixRewritesPath 内层 handler 必须看到剥掉前缀后的路径，
// 否则注册服务自己的 ServeMux（写死 "/ping"）会匹配不到。
func TestStripPrefixRewritesPath(t *testing.T) {
	var seen string
	h := stripPrefix("/_reg", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/_reg/ping", nil))
	if seen != "/ping" {
		t.Errorf("inner handler saw %q, want /ping", seen)
	}

	// 恰好等于前缀时应还原成根路径：signalserver 的 HandleDashboard 硬判
	// Path != "/" 就 404，空串会被它误判。
	h2 := stripPrefix("/_signal", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
	}))
	h2.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/_signal", nil))
	if seen != "/" {
		t.Errorf("inner handler saw %q, want / (empty path breaks HandleDashboard)", seen)
	}
}
