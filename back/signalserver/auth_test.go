package signalserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTokenFromRequest 表驱动测试 token 提取：query 参数优先，Bearer header 其次。
// 发现背景：tokenFromRequest 有两个输入源（query param + Authorization header），
// 旧实现只通过集成测试覆盖，缺少对边界组合的表驱动覆盖。
func TestTokenFromRequest(t *testing.T) {
	tests := []struct {
		name   string
		url    string // full URL (with query string)
		header string // Authorization header value (empty = no header)
		want   string
	}{
		{"no token anywhere", "/status", "", ""},
		{"query param only", "/status?token=abc", "", "abc"},
		{"header only", "/status", "Bearer def", "def"},
		{"query param beats header", "/status?token=abc", "Bearer def", "abc"},
		{"header without Bearer prefix", "/status", "rawtoken", ""},
		{"header with lowercase bearer", "/status", "bearer ghi", "ghi"},
		{"header with mixed case bearer", "/status", "Bearer XYZ", "XYZ"},
		{"header with Bearer prefix only", "/status", "Bearer ", ""},
		{"header with short bearer-like prefix", "/status", "bea", ""},
		{"header with extra spaces", "/status", "Bearer   spaced   ", "spaced"},
		{"query param with spaces", "/status?token=has%20space", "", "has space"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.url, nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			assert.Equal(t, tt.want, tokenFromRequest(r))
		})
	}
}

// TestOpsTokenOK 表驱动测试 ops 鉴权：无凭据拒绝、错误 token 拒绝、正确 token 放行。
// 发现背景：opsTokenOK 的安全语义（空 ops token = 关闭）与 peerjs 协议行为
// （空 token whitelist = 不限）刻意不同，需要表驱动锁定每种组合。
func TestOpsTokenOK(t *testing.T) {
	tests := []struct {
		name     string
		opsToken string // server's configured ops token
		reqToken string // token presented in request (query or header)
		want     bool
	}{
		{"no ops token configured", "", "anything", false},
		{"no ops token, empty token", "", "", false},
		{"ops token set, no token presented", "ops-1", "", false},
		{"ops token set, wrong token", "ops-1", "wrong", false},
		{"ops token set, correct token", "ops-1", "ops-1", true},
		{"ops token set, empty token", "ops-1", "", false},
		{"empty ops token with empty req token", "", "", false},
		{"empty ops token with non-empty req token", "", "x", false},
		{"exact match single char", "x", "x", true},
		{"exact match long token", "abc-123-xyz", "abc-123-xyz", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithOpsToken(tt.opsToken))
			r := httptest.NewRequest(http.MethodGet, "/status?token="+tt.reqToken, nil)
			assert.Equal(t, tt.want, srv.opsTokenOK(r),
				"opsToken=%q reqToken=%q", tt.opsToken, tt.reqToken)
		})
	}
}

// TestOpsTokenViaHeader 表驱动测试 Authorization: Bearer 路径的 ops 鉴权。
// 发现背景：curl/dashboard 脚本通过 header 传 token，WebSocket 升级只能走 query param，
// 两条路径都必须覆盖。
func TestOpsTokenViaHeader(t *testing.T) {
	tests := []struct {
		name    string
		header  string // Authorization header value
		opsTok  string // server's configured ops token
		wantOK  bool
	}{
		{"no header, ops token set", "", "ops-1", false},
		{"no header, no ops token", "", "", false},
		{"wrong bearer, ops token set", "Bearer wrong", "ops-1", false},
		{"correct bearer, ops token set", "Bearer ops-1", "ops-1", true},
		{"lowercase bearer, ops token set", "bearer ops-1", "ops-1", true},
		{"no bearer prefix", "ops-1", "ops-1", false},
		{"bearer only no value", "Bearer ", "ops-1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithOpsToken(tt.opsTok))
			r := httptest.NewRequest(http.MethodGet, "/status", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			assert.Equal(t, tt.wantOK, srv.opsTokenOK(r))
		})
	}
}
