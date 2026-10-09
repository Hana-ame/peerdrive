package regserver

// handlers_table_test.go — 注册与中继登记的表驱动单测。
//
// 拆分背景（2026-10-09）：handlers_auth.go 与 handlers_relay.go 拆出后，
// 用表驱动补充此前未覆盖的边界：
//   - 注册：空字段、纯空白 username、密码为空
//   - relay：addrs 字符串/数组/空/非法类型的 round-trip、heartbeat 对不存在节点
//   - addrs 序列化/反序列化纯函数的全部输入形态
//
// 发现背景：原 TestRelayRegisterIsIdempotent 只覆盖了数组 addrs；
// 字符串 addrs、空 addrs、数字 addrs 都是静默退化的边界。

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"peerdrive/internal/ratelimit"
)

// newUnlimitedTestServer 起一个限流桶 burst 足够大的 Server，用于
// 不需要触达限流的业务测试。
//
// 发现背景：默认 register 桶 burst=3，超过 3 个请求就会 429——
// 但注册校验测试要跑 9 个 case，会被限流打断。这里把 burst 调到 100，
// 让测试专注于业务逻辑而不是限流。
func newUnlimitedTestServer(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Setenv("JWT_SECRET", "test-secret")
	dir := t.TempDir()
	srv, err := New(filepath.Join(dir, "reg.db"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	// 把三个桶都调到「测试期间不会触达」的大小。
	srv.registerLimiter = ratelimit.New(1000, 1000)
	srv.loginLimiter = ratelimit.New(1000, 1000)
	srv.relayLimiter = ratelimit.New(1000, 1000)
	return srv.Handler().(*http.ServeMux)
}

// TestRegisterValidationTableDriven 覆盖注册请求的全部校验分支。
//
// 发现背景：原 TestRegisterLoginWhoami 只测了成功路径与重名；
// 空 username、纯空白 username、空 password、坏 JSON 都是 400 边界。
func TestRegisterValidationTableDriven(t *testing.T) {
	mux := newUnlimitedTestServer(t)
	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "valid", body: `{"username":"alice2","password":"pw123456"}`, wantCode: http.StatusOK},
		{name: "empty-username", body: `{"username":"","password":"pw123456"}`, wantCode: http.StatusBadRequest},
		{name: "whitespace-username", body: `{"username":"   ","password":"pw123456"}`, wantCode: http.StatusBadRequest},
		{name: "empty-password", body: `{"username":"alice3","password":""}`, wantCode: http.StatusBadRequest},
		{name: "missing-username", body: `{"password":"pw123456"}`, wantCode: http.StatusBadRequest},
		{name: "missing-password", body: `{"username":"alice4"}`, wantCode: http.StatusBadRequest},
		{name: "empty-body", body: `{}`, wantCode: http.StatusBadRequest},
		{name: "bad-json", body: `{not json`, wantCode: http.StatusBadRequest},
		// 重名 409：单独测（依赖先注册的 alice2）。
		{name: "duplicate", body: `{"username":"alice2","password":"pw123456"}`, wantCode: http.StatusConflict},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			// 不并行：依赖注册顺序（duplicate 依赖前面的 valid）。
			rr := do(mux, "POST", "/auth/register", c.body, "")
			if rr.Code != c.wantCode {
				t.Errorf("code = %d want %d (body=%s)", rr.Code, c.wantCode, rr.Body.String())
			}
		})
	}
}

// TestRelayRegisterTableDriven 覆盖 relay 登记的全部字段组合。
//
// 发现背景：原 TestRelayRegisterIsIdempotent 只测了数组 addrs；
// 字符串 addrs、空 addrs、数字 addrs 都是边界。
func TestRelayRegisterTableDriven(t *testing.T) {
	mux := newTestServer(t)
	cases := []struct {
		name     string
		body     string
		wantCode int
	}{
		{name: "array-addrs", body: `{"peer_id":"p1","addrs":["1.2.3.4:1"],"storage_mb":100}`, wantCode: http.StatusOK},
		{name: "string-addrs", body: `{"peer_id":"p2","addrs":"5.6.7.8:2","storage_mb":200}`, wantCode: http.StatusOK},
		{name: "empty-addrs", body: `{"peer_id":"p3","addrs":[]}`, wantCode: http.StatusOK},
		{name: "number-addrs", body: `{"peer_id":"p4","addrs":12345}`, wantCode: http.StatusOK},
		{name: "nil-addrs", body: `{"peer_id":"p5"}`, wantCode: http.StatusOK},
		{name: "missing-peer-id", body: `{"addrs":["1.2.3.4:1"]}`, wantCode: http.StatusBadRequest},
		{name: "empty-peer-id", body: `{"peer_id":"","addrs":[]}`, wantCode: http.StatusBadRequest},
		{name: "whitespace-peer-id", body: `{"peer_id":"   ","addrs":[]}`, wantCode: http.StatusBadRequest},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			rr := do(mux, "POST", "/p2p/relay/register", c.body, "")
			if rr.Code != c.wantCode {
				t.Errorf("code = %d want %d (body=%s)", rr.Code, c.wantCode, rr.Body.String())
			}
		})
	}
}

// TestRelayHeartbeatNonexistent 锁住「heartbeat 对不存在节点静默成功」的语义。
//
// 发现背景：拆 handlers_relay.go 时担心 relayHeartbeat 被改成「节点不存在即 404」
// ——那会让调用方能通过 heartbeat 探测节点是否在网，违反「心跳不泄露状态」的设计。
func TestRelayHeartbeatNonexistent(t *testing.T) {
	mux := newTestServer(t)
	rr := do(mux, "POST", "/p2p/relay/heartbeat", `{"peer_id":"ghost"}`, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("heartbeat for nonexistent: code=%d body=%s (should be 200 silent)", rr.Code, rr.Body.String())
	}
	if !contains(rr.Body.String(), `"status":"ok"`) {
		t.Errorf("body = %s, want status:ok", rr.Body.String())
	}
}

// TestRelayHeartbeatMissingPeerID 空 peer_id 必须 400（与 register 一致）。
func TestRelayHeartbeatMissingPeerID(t *testing.T) {
	mux := newTestServer(t)
	if rr := do(mux, "POST", "/p2p/relay/heartbeat", `{}`, ""); rr.Code != http.StatusBadRequest {
		t.Errorf("missing peer_id: code=%d want 400", rr.Code)
	}
}

// TestRelayListContainsAll 登记若干节点后 list 应全部返回（含不同 addrs 形态）。
//
// 发现背景：原 TestRelayRegisterIsIdempotent 只验证了 upsert 更新；
// 多节点并存 + 不同 addrs 形态的 round-trip 没被覆盖。
func TestRelayListContainsAll(t *testing.T) {
	mux := newTestServer(t)
	// 登记 3 个节点：数组 addrs / 字符串 addrs / 空 addrs。
	_ = do(mux, "POST", "/p2p/relay/register", `{"peer_id":"la","addrs":["1.1.1.1:1"]}`, "")
	_ = do(mux, "POST", "/p2p/relay/register", `{"peer_id":"lb","addrs":"2.2.2.2:2"}`, "")
	_ = do(mux, "POST", "/p2p/relay/register", `{"peer_id":"lc","addrs":[]}`, "")

	lr := do(mux, "GET", "/p2p/relay/list", "", "")
	if lr.Code != http.StatusOK {
		t.Fatalf("list: %d", lr.Code)
	}
	var out struct {
		Relays []struct {
			PeerID string `json:"peer_id"`
			Addrs  any    `json:"addrs"`
		} `json:"relays"`
	}
	if err := json.Unmarshal(lr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v body=%s", err, lr.Body.String())
	}
	if len(out.Relays) != 3 {
		t.Fatalf("relays = %d, want 3 (body=%s)", len(out.Relays), lr.Body.String())
	}
	// 按 peer_id 查。
	byID := map[string]any{}
	for _, r := range out.Relays {
		byID[r.PeerID] = r.Addrs
	}
	// 数组 addrs 回来还是数组。
	if _, ok := byID["la"].([]any); !ok {
		t.Errorf("la addrs type = %T, want []any", byID["la"])
	}
	// 字符串 addrs 回来还是字符串。
	if s, ok := byID["lb"].(string); !ok || s != "2.2.2.2:2" {
		t.Errorf("lb addrs = %v (%T), want string \"2.2.2.2:2\"", byID["lb"], byID["lb"])
	}
	// 空数组 addrs 回来还是空数组。
	if arr, ok := byID["lc"].([]any); !ok || len(arr) != 0 {
		t.Errorf("lc addrs = %v (%T), want empty []any", byID["lc"], byID["lc"])
	}
}

// TestStringifyParseAddrsRoundTrip 纯函数的全部输入形态。
//
// 发现背景：拆 handlers_relay.go 时 stringifyAddrs / parseAddrs 从 Server 方法
// 变成纯函数（原参数名 s 与 receiver 冲突），用表驱动锁住 round-trip。
func TestStringifyParseAddrsRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		input   any
		wantRaw string // stringifyAddrs 的输出
		wantOut any    // parseAddrs 回来的值（字符串形态）
	}{
		{name: "nil", input: nil, wantRaw: `null`, wantOut: nil},
		{name: "empty-string", input: "", wantRaw: "", wantOut: nil},
		{name: "plain-string", input: "1.2.3.4:1", wantRaw: "1.2.3.4:1", wantOut: "1.2.3.4:1"},
		{name: "string-array", input: []string{"a:1", "b:2"}, wantRaw: `["a:1","b:2"]`, wantOut: []string{"a:1", "b:2"}},
		{name: "empty-array", input: []string{}, wantRaw: `[]`, wantOut: []string{}},
		{name: "number", input: 12345, wantRaw: "12345", wantOut: "12345"},
		{name: "number-array", input: []int{1, 2, 3}, wantRaw: "[1,2,3]", wantOut: "[1,2,3]"},
		// 注意：[]int 经 json.Unmarshal 成 []string 会失败（类型不匹配），
		// 故 parseAddrs 退回原字符串 "[1,2,3]"——这是设计选择（容错优先）。
		{name: "map", input: map[string]string{"k": "v"}, wantRaw: `{"k":"v"}`, wantOut: `{"k":"v"}`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			raw := stringifyAddrs(c.input)
			if raw != c.wantRaw {
				t.Errorf("stringifyAddrs(%v) = %q, want %q", c.input, raw, c.wantRaw)
			}
			got := parseAddrs(raw)
			if !anyEqual(got, c.wantOut) {
				t.Errorf("parseAddrs(%q) = %v, want %v", raw, got, c.wantOut)
			}
		})
	}
}

// anyEqual 比较两个 any（JSON round-trip 后类型可能变化，做宽松比较）。
func anyEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// contains 是 strings.Contains 的薄封装（避免本文件 import strings）。
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > len(sub) && containsImpl(s, sub))
}

func containsImpl(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
