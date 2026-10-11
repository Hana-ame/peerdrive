package signalserver

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestHandleCORSPreflight 表驱动测试 preflight-only CORS handler（不设 Allow-Origin）。
// 发现背景：handleCORSPreflight 与 handleCORS 的区别是前者不设 wildcard header，
// 旧实现只通过集成测试间接覆盖，缺少对 OPTIONS vs 非 OPTIONS 的表驱动覆盖。
func TestHandleCORSPreflight(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		wantHandled bool
	}{
		{"OPTIONS preflight", http.MethodOptions, true},
		{"GET normal request", http.MethodGet, false},
		{"POST normal request", http.MethodPost, false},
		{"PUT normal request", http.MethodPut, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, "/status", nil)
			handled := handleCORSPreflight(w, r)
			assert.Equal(t, tt.wantHandled, handled)
			if tt.wantHandled {
				assert.Equal(t, http.StatusNoContent, w.Code)
				// Must NOT set Allow-Origin (the point of this function)
				assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
			}
		})
	}
}

// TestAllowCORS 表驱动测试 wildcard CORS header 设置。
func TestAllowCORS(t *testing.T) {
	w := httptest.NewRecorder()
	allowCORS(w)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Content-Type", w.Header().Get("Access-Control-Allow-Headers"))
	assert.Equal(t, "GET, POST, OPTIONS", w.Header().Get("Access-Control-Allow-Methods"))
}

// TestAllowCORSFor 表驱动测试 allow-list CORS：无列表=通配、有列表=匹配回显、不匹配=无头。
// 发现背景：WithCORSOrigins 的 opt-in 语义——不配保持旧行为，配了只放行匹配的 Origin。
// 需要覆盖：无列表、有列表含 "*"、有列表精确匹配、Origin "null"（file:// 面板）、
// 大小写不敏感匹配、列表外 Origin 不设置 header。
func TestAllowCORSFor(t *testing.T) {
	tests := []struct {
		name     string
		origins  []string // server's configured allow-list (nil = not configured)
		origin   string   // request's Origin header
		wantACAO string   // expected Access-Control-Allow-Origin value
	}{
		{"no list, any origin -> wildcard", nil, "", "*"},
		{"no list, with origin -> wildcard", nil, "https://example.com", "*"},
		{"no list, file:// origin -> wildcard", nil, "null", "*"},
		{"explicit star in list -> wildcard", []string{"*"}, "https://anything.com", "*"},
		{"explicit star, empty origin -> wildcard", []string{"*"}, "", "*"},
		{"exact match, echo origin", []string{"https://peerdrive.pages.dev"}, "https://peerdrive.pages.dev", "https://peerdrive.pages.dev"},
		{"null in list, null origin -> echo", []string{"https://peerdrive.pages.dev", "null"}, "null", "null"},
		{"non-listed origin -> no header", []string{"https://peerdrive.pages.dev"}, "https://evil.example", ""},
		{"empty origin -> no header", []string{"https://peerdrive.pages.dev"}, "", ""},
		{"case insensitive match", []string{"https://peerdrive.pages.dev"}, "https://PEERDRIVE.PAGES.DEV", "https://PEERDRIVE.PAGES.DEV"},
		{"multiple origins, match second", []string{"https://a.com", "https://b.com"}, "https://b.com", "https://b.com"},
		{"multiple origins, no match", []string{"https://a.com", "https://b.com"}, "https://c.com", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey")
			if tt.origins != nil {
				srv.corsOrigins = tt.origins
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/peerjs/id", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			srv.allowCORSFor(w, r)
			assert.Equal(t, tt.wantACAO, w.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

// TestHandleCORS 表驱动测试 handleCORS（wildcard + preflight short-circuit）。
// 发现背景：handleCORS 在 preflight 时返回 204，正常请求时设置 header 但不拦截。
// 需要覆盖两种场景 + CORS 列表配置的影响。
func TestHandleCORS(t *testing.T) {
	tests := []struct {
		name        string
		method      string
		corsOrigins []string // server's configured allow-list
		reqOrigin   string   // request's Origin header
		wantHandled bool
		wantACAO    string
	}{
		{"OPTIONS no list -> handled + wildcard", http.MethodOptions, nil, "", true, "*"},
		{"GET no list -> not handled + wildcard", http.MethodGet, nil, "", false, "*"},
		{"POST no list -> not handled + wildcard", http.MethodPost, nil, "", false, "*"},
		{"OPTIONS with list, no origin -> handled + no header", http.MethodOptions, []string{"https://a.com"}, "", true, ""},
		{"OPTIONS with list, matching origin -> handled + echo", http.MethodOptions, []string{"https://a.com"}, "https://a.com", true, "https://a.com"},
		{"OPTIONS with star in list -> handled + wildcard", http.MethodOptions, []string{"*"}, "https://anything.com", true, "*"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey")
			if tt.corsOrigins != nil {
				srv.corsOrigins = tt.corsOrigins
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tt.method, "/peerjs/id", nil)
			if tt.reqOrigin != "" {
				r.Header.Set("Origin", tt.reqOrigin)
			}
			handled := srv.handleCORS(w, r)
			assert.Equal(t, tt.wantHandled, handled)
			assert.Equal(t, tt.wantACAO, w.Header().Get("Access-Control-Allow-Origin"))
		})
	}
}

// TestOriginAllowed 表驱动测试 WS 升级的 Origin 判定。
// 发现背景（红队 R2）：upgrader.CheckOrigin 曾硬编码为 true，导致
// WithCORSOrigins 只约束 REST 端点、信令 socket 完全不受约束——攻击者页面
// 仍可向任意 peer id 发 OFFER（每个 OFFER 在目标节点上产生一次 WebRTC 分配）。
// 本用例把「配了白名单就必须真的拦住跨域 socket」钉死，同时守住向后兼容：
// 不配白名单必须全放行（file:// 面板 Origin 是 "null"，历史行为不能变）。
func TestOriginAllowed(t *testing.T) {
	tests := []struct {
		name      string
		origins   []string
		origin    string
		wantAllow bool
	}{
		{"no allow-list: accept everything", nil, "https://evil.example.com", true},
		{"no allow-list: null origin (file:// panel)", nil, "null", true},
		{"no allow-list: no Origin header (Go dialer)", nil, "", true},
		{"empty allow-list: accept", []string{}, "https://evil.example.com", true},

		{"list set: matching origin", []string{"https://panel.example.com"}, "https://panel.example.com", true},
		{"list set: case-insensitive match", []string{"https://panel.example.com"}, "HTTPS://PANEL.example.com", true},
		{"list set: other origin rejected", []string{"https://panel.example.com"}, "https://evil.example.com", false},
		{"list set: wildcard allows all", []string{"*"}, "https://evil.example.com", true},
		{"list set: null origin accepted", []string{"null"}, "null", true},
		{"list set: null origin rejected when not listed", []string{"https://panel.example.com"}, "null", false},
		{"list set: no Origin header accepted", []string{"https://panel.example.com"}, "", true},
		{"list set: prefix must not match", []string{"https://panel.example.com"}, "https://panel.example.com.evil.example.com", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := NewServer("testkey", WithCORSOrigins(tt.origins))
			r := httptest.NewRequest(http.MethodGet, "/peerjs?token=testkey&id=x", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			assert.Equal(t, tt.wantAllow, s.originAllowed(r))
		})
	}
}
