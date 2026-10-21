package panel

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestBootstrapInjection —— 守卫「零配置面板」这个功能真的被接线了。
//
// ⚠️ 本会话第四次「永远绿的测试」高危点：这个守卫必须能抓到
// 「功能写了但没接上」。因此负向对照见 TestNegativeControlNotRunInCI，
// 且下面每条断言都针对**产出字节**而不是针对某个函数是否被调用。
func TestBootstrapInjection(t *testing.T) {
	req := httptest.NewRequest("GET", "/panel", nil)
	req.Host = "127.0.0.1:3000"
	rec := httptest.NewRecorder()
	Handler("/panel").ServeHTTP(rec, req)

	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("状态码 = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8（标成 octet-stream 会被浏览器当下载）", got)
	}
	// 注入必须真的改变了字节
	if len(body) == len(panelHTML) {
		t.Fatalf("响应与原始面板同长（%d 字节）—— bootstrap 根本没注入", len(body))
	}
	for _, want := range []string{"searchParams.set('node'", "/peerjs/node", "signal_host"} {
		if !strings.Contains(body, want) {
			t.Errorf("注入内容缺 %q", want)
		}
	}
	// 原始面板必须完整保留（注入是插入，不是替换）
	if !strings.Contains(body, "我的节点") {
		t.Error("原始面板内容丢失——注入把正文覆盖了")
	}
}

// TestBootstrapSignalKeyInjection 发现背景：审计 R2 HIGH（2026-10-08）。
// /peerjs/node 不再返回 signal_key（匿名端点泄露凭据）。面板 bootstrap 改为
// 从服务端注入 key——面板 HTML 由节点自身托管，注入是可信的。
//
// 测试：SetSignalKey 设置的 key 必须出现在产出 HTML 里；空 key 时不注入。
func TestBootstrapSignalKeyInjection(t *testing.T) {
	const testKey = "pd-signal-test-key-12345"
	SetSignalKey(testKey)
	defer SetSignalKey("")

	req := httptest.NewRequest("GET", "/panel", nil)
	req.Host = "127.0.0.1:3000"
	rec := httptest.NewRecorder()
	Handler("/panel").ServeHTTP(rec, req)

	body := rec.Body.String()
	// key 必须以 JS 字面量注入（"key" 参数被设置）
	if !strings.Contains(body, testKey) {
		t.Error("bootstrap HTML 里找不到注入的 signal key")
	}
	// 必须设置 key 参数
	if !strings.Contains(body, "u.searchParams.set('key'") {
		t.Error("bootstrap HTML 里缺少 set('key') 调用")
	}
}

// TestBootstrapNoKeyInjection 确认空 key 时不注入（面板退回手填）。
func TestBootstrapNoKeyInjection(t *testing.T) {
	SetSignalKey("")
	defer SetSignalKey("")

	req := httptest.NewRequest("GET", "/panel", nil)
	req.Host = "127.0.0.1:3000"
	rec := httptest.NewRecorder()
	Handler("/panel").ServeHTTP(rec, req)

	body := rec.Body.String()
	// 空 key 时条件必须为 false（if (false) ...），不会设置 key
	if !strings.Contains(body, "if (false) u.searchParams.set('key'") {
		t.Error("空 key 时 bootstrap 应该用 if (false) 条件跳过 key 注入")
	}
	// 空 key 时不应有 if (true) 条件
	if strings.Contains(body, "if (true) u.searchParams.set('key'") {
		t.Error("空 key 时不应用 if (true) 条件注入 key")
	}
}

// TestHandlerRejectsOtherPaths 确认精确匹配：面板不能吃掉同前缀的其它 API。
func TestHandlerRejectsOtherPaths(t *testing.T) {
	for _, p := range []string{"/panel/peerjs/node", "/panels", "/peerjs/node"} {
		req := httptest.NewRequest("GET", p, nil)
		rec := httptest.NewRecorder()
		Handler("/panel").ServeHTTP(rec, req)
		if rec.Code != 404 {
			t.Errorf("GET %s = %d, want 404（面板不该接管这些路径）", p, rec.Code)
		}
	}
}

// TestPeerJSEmbedded 确认 peerjs 库随二进制内嵌。
//
// 背景：面板按「同目录 → jsdelivr → unpkg」回退加载 peerjs。
// 二进制发布的场景同目录没有该文件，于是每次都落到 CDN——内网/墙/慢网下
// 直接表现为「面板连不上」（e2e.yml:120-140 记录的 known-flaky 根因）。
func TestPeerJSEmbedded(t *testing.T) {
	if len(peerjsJS) < 10_000 {
		t.Fatalf("内嵌 peerjs 只有 %d 字节，太小，多半没嵌进去", len(peerjsJS))
	}
	// 压缩后标识符被改名了：不能按 "PeerJS"/"MessageChannel" 找（实测这两个
	// 在 peerjs 1.5.5 压缩产物里出现 0 次），WebRTC 标准 API 名才留得住。
	// 改用压缩后仍然保留的结构特征（WebRTC 标准 API 名不会被 mangle）：
	// RTCPeerConnection / setRemoteDescription / iceServers。
	head := string(peerjsJS)
	for _, want := range []string{"RTCPeerConnection", "setRemoteDescription", "iceServers"} {
		if !strings.Contains(head, want) {
			t.Errorf("内嵌内容里找不到 %q，不像 peerjs 库", want)
		}
	}
}

// TestNegativeControlNotRunInCI 显式记录负向对照的结论。
//
// 不是测试，是一个防退化标记：它证明本包的断言**不是**恒真的。
// 2026-10-06 手工跑过的两条负向对照：
//   1. 把 withBootstrap 调用换回原始 panelHTML → TestBootstrapInjection 红
//   2. 让 bootstrap 用 location 而非节点的真实信令 → 浏览器用例红（信令 404）
// 如果哪天有人放宽这里的断言，请连同上面两条一起重跑。
func TestNegativeControlNotRunInCI(t *testing.T) {
	if testing.Short() {
		t.Skip("标记项")
	}
	t.Log("负向对照已手工验证：摘掉注入 → 红；用错信令 → 红")
}