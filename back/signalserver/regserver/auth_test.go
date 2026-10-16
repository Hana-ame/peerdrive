package regserver

// auth_test.go — 认证中间件与权限边界的表驱动单测。
//
// 拆分背景（2026-10-09）：authRequired / authInfoOf / writeJSON / writeErr
// 从 regserver.go 拆到 auth.go 后，用表驱动覆盖中间件的全部拒绝路径
// 与「context 未注入身份」的兜底行为。
//
// 发现背景：原 TestAuthRequired 只覆盖了 3 条拒绝路径；Basic / 多空格 /
// 无 scheme 等都是静默 401 的边界，值得锁定。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthRequiredTableDriven 覆盖 Authorization 头的全部解析分支。
func TestAuthRequiredTableDriven(t *testing.T) {
	mux := newTestServer(t)

	cases := []struct {
		name       string
		header     string
		wantCode   int
		wantBody   string
	}{
		{name: "missing-header", header: "", wantCode: http.StatusUnauthorized, wantBody: "missing authorization header"},
		{name: "basic-scheme", header: "Basic abc", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "no-scheme", header: "abc", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "bearer-no-space", header: "Bearerabc", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "bearer-multi-space", header: "Bearer  abc", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "bearer-empty-token", header: "Bearer ", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "bearer-garbage", header: "Bearer garbage", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		// 大小写不敏感：bearer / BEARER 都应被识别（但 token 无效仍 401）。
		{name: "lowercase-bearer", header: "bearer garbage", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
		{name: "uppercase-bearer", header: "BEARER garbage", wantCode: http.StatusUnauthorized, wantBody: "invalid or expired token"},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest("GET", "/api/health", nil)
			if c.header != "" {
				req.Header.Set("Authorization", c.header)
			}
			rr := httptest.NewRecorder()
			mux.ServeHTTP(rr, req)
			if rr.Code != c.wantCode {
				t.Fatalf("code = %d want %d", rr.Code, c.wantCode)
			}
			if !strings.Contains(rr.Body.String(), c.wantBody) {
				t.Errorf("body = %s, want to contain %q", rr.Body.String(), c.wantBody)
			}
		})
	}
}

// TestAuthRequiredAcceptsValidToken 是拒绝路径的对照：有效 token 必须通过。
// 拆 auth.go 时最怕的是中间件被悄悄改坏——本用例锁住「能通过」。
func TestAuthRequiredAcceptsValidToken(t *testing.T) {
	mux := newTestServer(t)
	// 直接注册拿 token。
	rr := do(mux, "POST", "/auth/register", `{"username":"alice","password":"pw123456"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d", rr.Code)
	}
	var reg struct{ Token string }
	_ = json.Unmarshal(rr.Body.Bytes(), &reg)

	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Authorization", "Bearer "+reg.Token)
	resp := httptest.NewRecorder()
	mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Errorf("valid token rejected: code=%d body=%s", resp.Code, resp.Body.String())
	}
}

// TestAuthInfoOfWithoutMiddleware 未经 authRequired 时返回零值——
// 这是 handler 直接读 context 的兜底行为（authWhoami 就这么用）。
func TestAuthInfoOfWithoutMiddleware(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	got := authInfoOf(r)
	if got.username != "" || got.role != "" {
		t.Errorf("authInfoOf without middleware = %+v, want zero", got)
	}
}

// TestAuthInfoOfWithMiddleware 经 authRequired 后能读到身份。
func TestAuthInfoOfWithMiddleware(t *testing.T) {
	srv := newBareServer(t, "test-secret")
	// 构造一个 handler，调用 authInfoOf 读出身份。
	captured := authInfo{}
	next := func(w http.ResponseWriter, r *http.Request) {
		captured = authInfoOf(r)
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	}
	wrapped := srv.authRequired(next)

	tok, err := srv.newToken("dave", "admin")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp := httptest.NewRecorder()
	wrapped.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("code = %d", resp.Code)
	}
	if captured.username != "dave" || captured.role != "admin" {
		t.Errorf("captured = %+v, want dave/admin", captured)
	}
}

// TestAuthWhoamiRoleOverride 锁定 role 以库为准的语义：
// 库里改过 role 的用户，whoami 返回库里的值，不是 token 里缓存的。
//
// 发现背景：拆 handlers_auth.go 时担心 authWhoami 的「以库为准」逻辑被
// 顺手简化成「直接用 token 里的 role」——那会让管理员改 role 后不生效。
func TestAuthWhoamiRoleOverride(t *testing.T) {
	mux := newTestServer(t)
	// 注册一个用户。
	rr := do(mux, "POST", "/auth/register", `{"username":"eve","password":"pw123456"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d", rr.Code)
	}
	var reg struct{ Token string }
	_ = json.Unmarshal(rr.Body.Bytes(), &reg)
	if reg.Token == "" {
		t.Fatal("no token")
	}
	// 基本路径：token 里是 user，whoami 返回 user。
	// （真正的 role 覆盖测试见 TestAuthWhoamiRoleFromDB。）
	rr2 := do(mux, "GET", "/auth/whoami", "", reg.Token)
	if rr2.Code != http.StatusOK {
		t.Fatalf("whoami: %d", rr2.Code)
	}
	var who struct{ Username, Role string }
	_ = json.Unmarshal(rr2.Body.Bytes(), &who)
	if who.Role != "user" {
		t.Errorf("role = %q, want user (token-embedded, no DB override yet)", who.Role)
	}
}

// TestAuthWhoamiRoleFromDB 锁定 role 以库为准：直接改库后 whoami 应返回新 role。
//
// 发现背景：authWhoami 的「以库为准」是安全关键路径——管理员改 role 必须即时生效，
// 否则用户能靠旧 token 一直用旧权限。本用例锁住 DB 优先语义。
func TestAuthWhoamiRoleFromDB(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret")
	srv, err := New(filepath.Join(t.TempDir(), "reg.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer srv.Close()
	// 注册一个用户，拿 token。
	mux := srv.Handler().(*http.ServeMux)
	rr := do(mux, "POST", "/auth/register", `{"username":"frank","password":"pw123456"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d", rr.Code)
	}
	var reg struct{ Token string }
	_ = json.Unmarshal(rr.Body.Bytes(), &reg)
	// 直接改库里的 role。
	if _, err := srv.db.Exec(`UPDATE users SET role='admin' WHERE username='frank'`); err != nil {
		t.Fatalf("update role: %v", err)
	}
	// whoami 应返回 admin（库值），不是 token 里的 user。
	rr2 := do(mux, "GET", "/auth/whoami", "", reg.Token)
	if rr2.Code != http.StatusOK {
		t.Fatalf("whoami: %d", rr2.Code)
	}
	var who struct{ Username, Role string }
	_ = json.Unmarshal(rr2.Body.Bytes(), &who)
	if who.Role != "admin" {
		t.Errorf("role = %q, want admin (DB override)", who.Role)
	}
}

// TestAuthInfoContextIsolation 锁住 authCtxKey 的未导出语义：
// 外部包不能伪造身份——这是用未导出 struct 类型做 context key 的目的。
//
// 本用例只是记录设计意图；真正的隔离由 Go 编译器保证（外部包无法引用
// authCtxKey 类型），故这里只验证「同包内能读到、跨 context 不串」。
func TestAuthInfoContextIsolation(t *testing.T) {
	srv := newBareServer(t, "test-secret")
	tok1, _ := srv.newToken("u1", "user")
	tok2, _ := srv.newToken("u2", "admin")

	// 两个独立 request，身份不串。
	req1 := httptest.NewRequest("GET", "/x", nil)
	req1.Header.Set("Authorization", "Bearer "+tok1)
	req2 := httptest.NewRequest("GET", "/y", nil)
	req2.Header.Set("Authorization", "Bearer "+tok2)

	var captured []authInfo
	next := func(w http.ResponseWriter, r *http.Request) {
		captured = append(captured, authInfoOf(r))
		writeJSON(w, http.StatusOK, map[string]string{"ok": "yes"})
	}
	wrapped := srv.authRequired(next)

	resp1 := httptest.NewRecorder()
	wrapped.ServeHTTP(resp1, req1)
	resp2 := httptest.NewRecorder()
	wrapped.ServeHTTP(resp2, req2)

	if len(captured) != 2 {
		t.Fatalf("captured %d identities, want 2", len(captured))
	}
	if captured[0].username != "u1" {
		t.Errorf("req1 identity = %q, want u1", captured[0].username)
	}
	if captured[1].username != "u2" {
		t.Errorf("req2 identity = %q, want u2 (no cross-request leak)", captured[1].username)
	}
}
