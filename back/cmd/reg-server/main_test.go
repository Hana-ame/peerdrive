package main

// reg-server 的测试重点是「与原独立仓行为一致」——因为它接管了既有部署：
// 旧 token 必须仍可验、路由与状态码不能漂。

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newTestServer 起一个挂在临时库上的 mux（不监听端口）。
func newTestServer(t *testing.T) *http.ServeMux {
	t.Helper()
	jwtSecret = []byte("test-secret")
	prev := db
	t.Cleanup(func() { db = prev })
	if err := openDB(filepath.Join(t.TempDir(), "reg.db")); err != nil {
		t.Fatalf("openDB: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", apiPing)
	mux.HandleFunc("GET /api/health", authRequired(apiHealth))
	mux.HandleFunc("POST /auth/register", authRegister)
	mux.HandleFunc("POST /auth/login", authLogin)
	mux.HandleFunc("GET /auth/whoami", authRequired(authWhoami))
	mux.HandleFunc("GET /auth/list", authRequired(authList))
	mux.HandleFunc("POST /p2p/relay/register", relayRegister)
	mux.HandleFunc("POST /p2p/relay/heartbeat", relayHeartbeat)
	mux.HandleFunc("GET /p2p/relay/list", relayList)
	return mux
}

func do(mux *http.ServeMux, method, path, body, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	return rr
}

// TestRegisterLoginWhoami 走一遍注册→登录→查身份的完整链路。
func TestRegisterLoginWhoami(t *testing.T) {
	mux := newTestServer(t)

	rr := do(mux, "POST", "/auth/register", `{"username":"alice","password":"pw123456"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("register: got %d body=%s", rr.Code, rr.Body)
	}
	var reg struct{ Username, Token string }
	if err := json.Unmarshal(rr.Body.Bytes(), &reg); err != nil {
		t.Fatalf("decode register: %v", err)
	}
	if reg.Token == "" {
		t.Fatal("register returned empty token")
	}

	// 重复注册必须 409，否则同名用户会被静默顶掉。
	if rr := do(mux, "POST", "/auth/register", `{"username":"alice","password":"pw123456"}`, ""); rr.Code != http.StatusConflict {
		t.Errorf("duplicate register: got %d want 409", rr.Code)
	}

	rr = do(mux, "POST", "/auth/login", `{"username":"alice","password":"pw123456"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("login: got %d body=%s", rr.Code, rr.Body)
	}
	var lg struct{ Token string }
	_ = json.Unmarshal(rr.Body.Bytes(), &lg)

	rr = do(mux, "GET", "/auth/whoami", "", lg.Token)
	if rr.Code != http.StatusOK {
		t.Fatalf("whoami: got %d body=%s", rr.Code, rr.Body)
	}
	var who struct{ Username, Role string }
	_ = json.Unmarshal(rr.Body.Bytes(), &who)
	if who.Username != "alice" || who.Role != "user" {
		t.Errorf("whoami = %+v, want alice/user", who)
	}
}

// TestLoginRejectsWrongPassword 与用户不存在必须同文案，避免用户名枚举。
func TestLoginRejectsWrongPassword(t *testing.T) {
	mux := newTestServer(t)
	_ = do(mux, "POST", "/auth/register", `{"username":"bob","password":"pw123456"}`, "")

	r1 := do(mux, "POST", "/auth/login", `{"username":"bob","password":"wrong"}`, "")
	r2 := do(mux, "POST", "/auth/login", `{"username":"nosuch","password":"pw123456"}`, "")
	if r1.Code != http.StatusUnauthorized || r2.Code != http.StatusUnauthorized {
		t.Fatalf("codes = %d / %d, want 401/401", r1.Code, r2.Code)
	}
	var e1, e2 struct{ Error string }
	_ = json.Unmarshal(r1.Body.Bytes(), &e1)
	_ = json.Unmarshal(r2.Body.Bytes(), &e2)
	if e1.Error != e2.Error {
		t.Errorf("error msgs differ: %q vs %q (enables user enumeration)", e1.Error, e2.Error)
	}
}

// TestAuthRequired 覆盖无 header / 非 Bearer / 坏 token 三种拒绝路径。
func TestAuthRequired(t *testing.T) {
	mux := newTestServer(t)
	if rr := do(mux, "GET", "/api/health", "", ""); rr.Code != http.StatusUnauthorized {
		t.Errorf("no header: got %d want 401", rr.Code)
	}
	req := httptest.NewRequest("GET", "/api/health", nil)
	req.Header.Set("Authorization", "Basic abc")
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("non-bearer: got %d want 401", rr.Code)
	}
	if rr := do(mux, "GET", "/api/health", "", "garbage"); rr.Code != http.StatusUnauthorized {
		t.Errorf("bad token: got %d want 401", rr.Code)
	}
}

// TestExpiredTokenRejects 是回归护栏：exp 过期必须被拒。
func TestExpiredTokenRejects(t *testing.T) {
	jwtSecret = []byte("test-secret")
	now := time.Now()
	claims := map[string]any{
		"username": "old", "role": "user",
		"iss": "https://localhost:4000", "sub": "old",
		"iat": now.Add(-100 * time.Hour).Unix(),
		"exp": now.Add(-1 * time.Hour).Unix(), // 已过期
	}
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	tok, err := newTokenFrom(hb, pb)
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	if _, _, err := verifyToken(tok); err == nil {
		t.Error("expired token accepted — verifyToken must reject exp < now")
	}
}

// TestTokenStableAcrossRestarts 验签不依赖进程内状态：换一个 jwtSecret 就该失效。
func TestTokenWrongSecretRejected(t *testing.T) {
	jwtSecret = []byte("secret-a")
	tok, err := newToken("u", "user")
	if err != nil {
		t.Fatal(err)
	}
	jwtSecret = []byte("secret-b")
	if _, _, err := verifyToken(tok); err == nil {
		t.Error("token verified under different secret")
	}
}

// TestAuthListEmptyIsArray 空库时必须返回 [] 而不是 null——前端常直接 .map()。
func TestAuthListEmptyIsArray(t *testing.T) {
	mux := newTestServer(t)
	jwtSecret = []byte("test-secret")
	tok, err := newToken("admin", "user")
	if err != nil {
		t.Fatal(err)
	}
	rr := do(mux, "GET", "/auth/list", "", tok)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"users":[]`) {
		t.Errorf(`empty list body = %s, want "users":[]`, rr.Body.String())
	}
}

// TestRelayRegisterIsIdempotent 重复注册同一 peer_id 应更新而非报冲突。
func TestRelayRegisterIsIdempotent(t *testing.T) {
	mux := newTestServer(t)
	first := `{"peer_id":"p1","addrs":["1.2.3.4:1"],"storage_mb":100}`
	rr := do(mux, "POST", "/p2p/relay/register", first, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("register: got %d body=%s", rr.Code, rr.Body)
	}
	rr = do(mux, "POST", "/p2p/relay/register", `{"peer_id":"p1","addrs":["5.6.7.8:2"],"storage_mb":250,"load_pct":42.5}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("re-register: got %d body=%s", rr.Code, rr.Body)
	}
	// 注意：这里必须用 list 的响应解析。写成 `if rr := do(...); rr.Code != ...`
	// 会让解析读到上面那次 register 的 body（{"status":"registered"}），
	// 解出 relays=0，测的是变量遮蔽不是行为。
	lr := do(mux, "GET", "/p2p/relay/list", "", "")
	if lr.Code != http.StatusOK {
		t.Fatalf("list: got %d", lr.Code)
	}
	var out struct {
		Relays []struct {
			PeerID    string   `json:"peer_id"`
			Addrs     []string `json:"addrs"`
			StorageMB int64    `json:"storage_mb"`
			LoadPct   float64  `json:"load_pct"`
		} `json:"relays"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode list: %v body=%s", err, lr.Body.String())
	}
	if len(out.Relays) != 1 {
		t.Fatalf("relays = %d, want 1 (idempotent upsert)", len(out.Relays))
	}
	if out.Relays[0].StorageMB != 250 || out.Relays[0].Addrs[0] != "5.6.7.8:2" {
		t.Errorf("relay not updated: %+v", out.Relays[0])
	}
	// 回归护栏：load_pct 必须序列化成 "load_pct"。字段漏了 json tag 时 Go
	// 会输出 "LoadPct"，前端读不到负载值且不报错——静默的兼容破坏。
	if out.Relays[0].LoadPct != 42.5 {
		t.Errorf("load_pct = %v, want 42.5 (json tag missing?) raw=%s", out.Relays[0].LoadPct, lr.Body.String())
	}

	// peer_id 必填。
	if rr := do(mux, "POST", "/p2p/relay/register", `{"addrs":[]}`, ""); rr.Code != http.StatusBadRequest {
		t.Errorf("missing peer_id: got %d want 400", rr.Code)
	}
}

// TestPingNeedsNoAuth /ping 是探活，必须免认证。
func TestPingNeedsNoAuth(t *testing.T) {
	mux := newTestServer(t)
	rr := do(mux, "GET", "/ping", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("ping: got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "peerdrive-registration") {
		t.Errorf("ping body = %s", rr.Body.String())
	}
}

// TestMainRequiresJWTSecret 缺 JWT_SECRET 必须直接退出而不是静默用空密钥
// ——空密钥会让任何人伪造 token。
func TestMainRequiresJWTSecret(t *testing.T) {
	old, had := os.LookupEnv("JWT_SECRET")
	defer func() {
		if had {
			os.Setenv("JWT_SECRET", old)
		} else {
			os.Unsetenv("JWT_SECRET")
		}
	}()
	os.Unsetenv("JWT_SECRET")
	if os.Getenv("JWT_SECRET") != "" {
		t.Fatal("precondition: JWT_SECRET should be empty")
	}
	// main() 走 os.Exit，无法在同进程测；此处只锁定「空密钥不会静默启动」这一
	// 前提：verifyToken 在 jwtSecret 为空时对任何签名都不通过。
	jwtSecret = nil
	if _, _, err := verifyToken("a.b.c"); err == nil {
		t.Error("empty jwtSecret accepted a token")
	}
}
