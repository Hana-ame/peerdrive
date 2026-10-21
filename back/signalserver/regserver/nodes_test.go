package regserver

// nodes_test.go — 用户与节点名录（API-DESIGN.md §6 / Phase 7）测试。
// 发现背景：Phase 7 身份管理与授权体系要求节点可绑定运营者账号，
// 对外提供可见性分级查询（public / registered / private），并具备防占坑机制。

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
)

func newTestServerWithInstance(t *testing.T) (*Server, *http.ServeMux) {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	dir := t.TempDir()
	srv, err := New(filepath.Join(dir, "reg.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, srv.Handler().(*http.ServeMux)
}

func registerUser(t *testing.T, mux *http.ServeMux, username, password string) string {
	t.Helper()
	rr := do(mux, "POST", "/auth/register", `{"username":"`+username+`","password":"`+password+`"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("registerUser(%s): code=%d body=%s", username, rr.Code, rr.Body)
	}
	var res struct{ Token string }
	_ = json.Unmarshal(rr.Body.Bytes(), &res)
	return res.Token
}

// TestNodeRegisterAndGet 验证节点登记与公开查询流程。
func TestNodeRegisterAndGet(t *testing.T) {
	_, mux := newTestServerWithInstance(t)
	token := registerUser(t, mux, "alice", "pw123456")

	// 1. 无认证登记必须 401
	rr := do(mux, "POST", "/nodes/register", `{"peer_id":"peer-alice-1"}`, "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated register: got %d want 401", rr.Code)
	}

	// 2. 带 token 登记
	body := `{"peer_id":"peer-alice-1","endpoints":["wss://relay.example.com"],"capabilities":{"relay":true},"version":"v1.0.0","visibility":"public"}`
	rr = do(mux, "POST", "/nodes/register", body, token)
	if rr.Code != http.StatusOK {
		t.Fatalf("register node: got %d body=%s", rr.Code, rr.Body)
	}
	var regResp struct {
		Status string `json:"status"`
		PeerID string `json:"peer_id"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &regResp)
	if regResp.Status != "registered" || regResp.PeerID != "peer-alice-1" {
		t.Errorf("register response mismatch: %+v", regResp)
	}

	// 3. 公开查询 GET /nodes/{peer_id}
	rr = do(mux, "GET", "/nodes/peer-alice-1", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get node: got %d body=%s", rr.Code, rr.Body)
	}
	var nodeResp struct {
		PeerID       string         `json:"peer_id"`
		Username     string         `json:"username"`
		Endpoints    []string       `json:"endpoints"`
		Capabilities map[string]any `json:"capabilities"`
		Version      string         `json:"version"`
		Visibility   string         `json:"visibility"`
		Online       bool           `json:"online"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &nodeResp); err != nil {
		t.Fatalf("decode nodeResp: %v", err)
	}
	if nodeResp.Username != "alice" || nodeResp.Online != true || nodeResp.Visibility != "public" {
		t.Errorf("unexpected nodeResp: %+v", nodeResp)
	}

	// 4. 查询用户名下在线节点 GET /users/{username}/nodes
	rr = do(mux, "GET", "/users/alice/nodes", "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("get user nodes: got %d body=%s", rr.Code, rr.Body)
	}
	var userNodesResp struct {
		Username string `json:"username"`
		Nodes    []struct {
			PeerID   string `json:"peer_id"`
			Username string `json:"username"`
			Online   bool   `json:"online"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &userNodesResp); err != nil {
		t.Fatalf("decode userNodesResp: %v", err)
	}
	if len(userNodesResp.Nodes) != 1 || userNodesResp.Nodes[0].PeerID != "peer-alice-1" {
		t.Errorf("unexpected user nodes: %+v", userNodesResp)
	}
}

// TestNodeVisibilityLevels 验证 public / registered / private 三级可见性权限。
func TestNodeVisibilityLevels(t *testing.T) {
	_, mux := newTestServerWithInstance(t)
	aliceToken := registerUser(t, mux, "alice", "pw123456")
	bobToken := registerUser(t, mux, "bob", "pw123456")

	// Alice 注册一个 private 节点和一个 registered 节点
	_ = do(mux, "POST", "/nodes/register", `{"peer_id":"node-priv","visibility":"private"}`, aliceToken)
	_ = do(mux, "POST", "/nodes/register", `{"peer_id":"node-reg","visibility":"registered"}`, aliceToken)

	// 1. private 节点测试
	// 匿名访问 -> 404 (根据 API-DESIGN.md 不确认其存在)
	rAnon := do(mux, "GET", "/nodes/node-priv", "", "")
	if rAnon.Code != http.StatusNotFound {
		t.Errorf("anon get private node: got %d want 404", rAnon.Code)
	}
	// Bob 访问 -> 404
	rBob := do(mux, "GET", "/nodes/node-priv", "", bobToken)
	if rBob.Code != http.StatusNotFound {
		t.Errorf("bob get private node: got %d want 404", rBob.Code)
	}
	// Alice (属主) 访问 -> 200
	rAlice := do(mux, "GET", "/nodes/node-priv", "", aliceToken)
	if rAlice.Code != http.StatusOK {
		t.Errorf("alice get own private node: got %d want 200", rAlice.Code)
	}

	// 2. registered 节点测试
	// 匿名访问 -> 401 (需认证)
	rAnon = do(mux, "GET", "/nodes/node-reg", "", "")
	if rAnon.Code != http.StatusUnauthorized {
		t.Errorf("anon get registered node: got %d want 401", rAnon.Code)
	}
	// Bob (已登录用户) 访问 -> 200
	rBob = do(mux, "GET", "/nodes/node-reg", "", bobToken)
	if rBob.Code != http.StatusOK {
		t.Errorf("bob get registered node: got %d want 200", rBob.Code)
	}

	// 3. GET /users/alice/nodes 列表可见性测试
	// 匿名访问 -> private 与 registered 均不应该出现
	rr := do(mux, "GET", "/users/alice/nodes", "", "")
	var listAnon struct{ Nodes []map[string]any }
	_ = json.Unmarshal(rr.Body.Bytes(), &listAnon)
	if len(listAnon.Nodes) != 0 {
		t.Errorf("anon user nodes: got %d items want 0", len(listAnon.Nodes))
	}

	// Bob 访问 -> 只能看到 registered 节点，看不到 private
	rr = do(mux, "GET", "/users/alice/nodes", "", bobToken)
	var listBob struct{ Nodes []map[string]any }
	_ = json.Unmarshal(rr.Body.Bytes(), &listBob)
	if len(listBob.Nodes) != 1 || listBob.Nodes[0]["peer_id"] != "node-reg" {
		t.Errorf("bob user nodes: got %+v, want only node-reg", listBob.Nodes)
	}

	// Alice 访问 -> 能看到两个节点
	rr = do(mux, "GET", "/users/alice/nodes", "", aliceToken)
	var listAlice struct{ Nodes []map[string]any }
	_ = json.Unmarshal(rr.Body.Bytes(), &listAlice)
	if len(listAlice.Nodes) != 2 {
		t.Errorf("alice user nodes: got %d items want 2", len(listAlice.Nodes))
	}
}

// TestNodeAntiSquatting 验证防占坑与过期流转机制。
func TestNodeAntiSquatting(t *testing.T) {
	srv, mux := newTestServerWithInstance(t)
	aliceToken := registerUser(t, mux, "alice", "pw123456")
	bobToken := registerUser(t, mux, "bob", "pw123456")

	// Alice 注册 peer-100
	rr := do(mux, "POST", "/nodes/register", `{"peer_id":"peer-100"}`, aliceToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("alice register: got %d", rr.Code)
	}

	// Bob 尝试抢占活跃中的 peer-100 -> 409 Conflict
	rr = do(mux, "POST", "/nodes/register", `{"peer_id":"peer-100"}`, bobToken)
	if rr.Code != http.StatusConflict {
		t.Errorf("bob squat active node: got %d want 409", rr.Code)
	}

	// Alice 自己更新 peer-100 -> 200 OK
	rr = do(mux, "POST", "/nodes/register", `{"peer_id":"peer-100","version":"v2"}`, aliceToken)
	if rr.Code != http.StatusOK {
		t.Errorf("alice update own node: got %d want 200", rr.Code)
	}

	// 模拟 Alice 节点离线超过 5 分钟 (修改 last_seen)
	_, err := srv.db.Exec(`UPDATE user_nodes SET last_seen = datetime('now', '-10 minutes') WHERE peer_id = 'peer-100'`)
	if err != nil {
		t.Fatalf("update last_seen: %v", err)
	}

	// 节点离线后，在线查询应标为 online: false
	rr = do(mux, "GET", "/nodes/peer-100", "", "")
	var nodeResp struct{ Online bool }
	_ = json.Unmarshal(rr.Body.Bytes(), &nodeResp)
	if nodeResp.Online != false {
		t.Errorf("expired node: online=%v want false", nodeResp.Online)
	}

	// 节点离线后，Bob 重新注册 peer-100 -> 200 OK (所有权流转)
	rr = do(mux, "POST", "/nodes/register", `{"peer_id":"peer-100"}`, bobToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("bob register expired node: got %d want 200", rr.Code)
	}

	// 查询确认属主已变为 bob
	rr = do(mux, "GET", "/nodes/peer-100", "", "")
	var updatedResp struct{ Username string }
	_ = json.Unmarshal(rr.Body.Bytes(), &updatedResp)
	if updatedResp.Username != "bob" {
		t.Errorf("owner after expiry transfer = %s, want bob", updatedResp.Username)
	}
}

// TestNodeHeartbeatAndLifecycle 验证心跳刷新与注销权限。
func TestNodeHeartbeatAndLifecycle(t *testing.T) {
	srv, mux := newTestServerWithInstance(t)
	aliceToken := registerUser(t, mux, "alice", "pw123456")
	bobToken := registerUser(t, mux, "bob", "pw123456")

	_ = do(mux, "POST", "/nodes/register", `{"peer_id":"peer-live"}`, aliceToken)

	// 修改为 10 分钟前
	_, _ = srv.db.Exec(`UPDATE user_nodes SET last_seen = datetime('now', '-10 minutes') WHERE peer_id = 'peer-live'`)

	// 发送心跳
	rr := do(mux, "POST", "/nodes/heartbeat", `{"peer_id":"peer-live"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat: got %d", rr.Code)
	}

	// 查状态：应恢复 online: true
	rr = do(mux, "GET", "/nodes/peer-live", "", "")
	var n struct{ Online bool }
	_ = json.Unmarshal(rr.Body.Bytes(), &n)
	if !n.Online {
		t.Errorf("node after heartbeat: online=%v want true", n.Online)
	}

	// Bob 尝试删除 Alice 的节点 -> 403 Forbidden
	rr = do(mux, "DELETE", "/nodes/peer-live", "", bobToken)
	if rr.Code != http.StatusForbidden {
		t.Errorf("bob delete alice node: got %d want 403", rr.Code)
	}

	// Alice 删除自己的节点 -> 200 OK
	rr = do(mux, "DELETE", "/nodes/peer-live", "", aliceToken)
	if rr.Code != http.StatusOK {
		t.Fatalf("alice delete node: got %d want 200", rr.Code)
	}

	// 再次查询已不存在 -> 404
	rr = do(mux, "GET", "/nodes/peer-live", "", "")
	if rr.Code != http.StatusNotFound {
		t.Errorf("deleted node: got %d want 404", rr.Code)
	}
}
