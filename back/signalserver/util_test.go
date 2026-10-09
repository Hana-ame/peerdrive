package signalserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidPeerID 表驱动测试 peer id 校验：空/超长/保留名/正常 id。
// 发现背景：validPeerID 是 audit A-12 的安全修复——旧实现对 id 只判空，
// 攻击者可注册 ?id=local 伪造管理会话。需要表驱动锁定所有拒绝条件。
func TestValidPeerID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{"empty string", "", false},
		{"normal id", "node-abc123", true},
		{"short id", "a", true},
		{"long id at limit", strings.Repeat("a", 128), true},
		{"id over limit", strings.Repeat("a", 129), false},
		{"way over limit", strings.Repeat("a", 1000), false},
		{"reserved local", "local", false},
		{"reserved LOCAL", "LOCAL", true},
		{"contains special chars", "node/abc", true},
		{"contains spaces", "node abc", true},
		{"unicode id", "节点-中文", true},
		{"unicode id at limit", strings.Repeat("中", 42), true},
		{"unicode id over limit", strings.Repeat("中", 43), false},
		{"numeric only", "12345", true},
		{"alphanumeric", "abc123", true},
		{"with dashes", "node-abc-def", true},
		{"with dots", "node.abc.def", true},
		{"starts with dash", "-node", true},
		{"ends with dash", "node-", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, validPeerID(tt.id), "validPeerID(%q)", tt.id)
		})
	}
}

// TestRandomID 验证 randomID 返回合法的字母数字 id。
// 发现背景：randomID 是面板连接信令的第一步（GET /peerjs/id），
// 返回的 id 必须是非空、固定长度、仅含 [a-z0-9] 的字符串。
func TestRandomID(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := randomID()
		assert.Len(t, id, 16, "randomID must be exactly 16 chars")
		for _, c := range id {
			assert.True(t, (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9'),
				"randomID must only contain [a-z0-9], got %q", string(c))
		}
	}
}

// TestRandomID_Uniqueness 验证 100 次生成不重复（统计概率）。
func TestRandomID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool, 100)
	for i := 0; i < 100; i++ {
		id := randomID()
		assert.False(t, seen[id], "randomID should not repeat within 100 calls")
		seen[id] = true
	}
}

// TestClientIP 表驱动测试 IP 提取：IPv4、IPv6、无端口、异常 RemoteAddr。
// 发现背景：clientIP 只信 RemoteAddr，不读 X-Forwarded-For（安全设计），
// 需要表驱动覆盖各种 RemoteAddr 格式。
func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"IPv4 with port", "192.168.1.1:12345", "192.168.1.1"},
		{"IPv4 with port and space", "192.168.1.1:12345  ", "192.168.1.1"},
		{"IPv6 with port", "[::1]:8080", "::1"},
		{"IPv6 loopback no port", "::1", "::1"},
		{"IPv4 localhost", "127.0.0.1:9000", "127.0.0.1"},
		{"no colon (SplitHostPort fails)", "not-an-ip", "not-an-ip"},
		{"empty RemoteAddr", "", ""},
		{"IPv6 with port, spaces", "[fe80::1]:9000 ", "fe80::1"},
		{"port 0", "10.0.0.1:0", "10.0.0.1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/test", nil)
			r.RemoteAddr = tt.remoteAddr
			assert.Equal(t, tt.want, clientIP(r))
		})
	}
}
