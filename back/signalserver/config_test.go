package signalserver

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWithCORSOrigins 表驱动测试 CORS 配置选项：nil/空/清理空白/保留非空。
// 发现背景：WithCORSOrigins 会 TrimSpace 并过滤空字符串，需要表驱动覆盖边界。
func TestWithCORSOrigins(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		want    []string
	}{
		{"nil input -> empty output", nil, []string{}},
		{"empty input -> empty output", []string{}, []string{}},
		{"single origin", []string{"https://example.com"}, []string{"https://example.com"}},
		{"whitespace trimmed", []string{"  https://a.com  ", "\thttps://b.com\t"}, []string{"https://a.com", "https://b.com"}},
		{"empty strings filtered", []string{"https://a.com", "", "https://b.com"}, []string{"https://a.com", "https://b.com"}},
		{"all empty strings -> empty", []string{"", "  ", "\t"}, []string{}},
		{"null origin preserved", []string{"null", "https://a.com"}, []string{"null", "https://a.com"}},
		{"star preserved", []string{"*"}, []string{"*"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithCORSOrigins(tt.input))
			assert.Equal(t, tt.want, srv.corsOrigins)
		})
	}
}

// TestWithTokenWhitelist 表驱动测试 token 白名单配置：nil/空/正常/去重。
// 发现背景：WithTokenWhitelist 的语义——空列表=不限制（peerjs 协议默认），
// 非空列表=严格匹配。需要表驱动覆盖边界。
func TestWithTokenWhitelist(t *testing.T) {
	tests := []struct {
		name    string
		input   []string
		wantLen int
		wantMap map[string]bool
	}{
		{"nil input -> nil whitelist", nil, 0, nil},
		{"empty input -> nil whitelist", []string{}, 0, nil},
		{"single token", []string{"tok-a"}, 1, map[string]bool{"tok-a": true}},
		{"multiple tokens", []string{"tok-a", "tok-b", "tok-c"}, 3, map[string]bool{"tok-a": true, "tok-b": true, "tok-c": true}},
		{"duplicate tokens", []string{"tok-a", "tok-a", "tok-b"}, 2, map[string]bool{"tok-a": true, "tok-b": true}},
		{"empty token included", []string{"", "tok-a"}, 2, map[string]bool{"": true, "tok-a": true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithTokenWhitelist(tt.input))
			assert.Len(t, srv.tokenWhitelist, tt.wantLen)
			if tt.wantMap != nil {
				assert.Equal(t, tt.wantMap, srv.tokenWhitelist)
			}
		})
	}
}

// TestWithOpsToken 表驱动测试 ops token 配置。
func TestWithOpsToken(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty -> empty", "", ""},
		{"normal token", "ops-token-123", "ops-token-123"},
		{"single char", "x", "x"},
		{"long token", "pd-signal-1edf5e05e4a52b7351392574", "pd-signal-1edf5e05e4a52b7351392574"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithOpsToken(tt.in))
			assert.Equal(t, tt.want, srv.opsToken)
		})
	}
}

// TestWithRateLimit 表驱动测试 rate limit 配置：各桶独立启停。
// 发现背景：WithRateLimit 的三个桶（announce/ws/id）可以独立配置，
// 一个桶为 0 不影响其他桶。
func TestWithRateLimit(t *testing.T) {
	tests := []struct {
		name          string
		cfg           RateLimitConfig
		wantAnnounceNil bool
		wantWSNil       bool
		wantIDNil       bool
	}{
		{"all zero -> all nil", RateLimitConfig{}, true, true, true},
		{"announce only", RateLimitConfig{AnnounceRPS: 10, AnnounceBurst: 100}, false, true, true},
		{"ws only", RateLimitConfig{WSRPS: 5, WSBurst: 50}, true, false, true},
		{"id only", RateLimitConfig{IDRPS: 1, IDBurst: 10}, true, true, false},
		{"announce and ws", RateLimitConfig{AnnounceRPS: 10, AnnounceBurst: 100, WSRPS: 5, WSBurst: 50}, false, false, true},
		{"all configured", RateLimitConfig{AnnounceRPS: 10, AnnounceBurst: 100, WSRPS: 5, WSBurst: 50, IDRPS: 1, IDBurst: 10}, false, false, false},
		{"zero rps, non-zero burst -> nil", RateLimitConfig{AnnounceRPS: 0, AnnounceBurst: 10}, true, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer("testkey", WithRateLimit(tt.cfg))
			if tt.wantAnnounceNil {
				assert.Nil(t, srv.announceLim, "announceLim should be nil (not configured)")
			} else {
				assert.NotNil(t, srv.announceLim, "announceLim should be set")
			}
			if tt.wantWSNil {
				assert.Nil(t, srv.wsLim, "wsLim should be nil (not configured)")
			} else {
				assert.NotNil(t, srv.wsLim, "wsLim should be set")
			}
			if tt.wantIDNil {
				assert.Nil(t, srv.idLim, "idLim should be nil (not configured)")
			} else {
				assert.NotNil(t, srv.idLim, "idLim should be set")
			}
		})
	}
}

// TestNewServer_Defaults 表驱动测试 NewServer 默认值。
// 发现背景：NewServer 的关键默认值（key fallback, queueTTL, heartbeatTTL）
// 是多个组件依赖的隐式契约，需要表驱动锁定。
func TestNewServer_Defaults(t *testing.T) {
	tests := []struct {
		name        string
		key         string
		wantKey     string
		wantQueueTTL int64 // seconds
	}{
		{"empty key -> peerjs", "", "peerjs", 30},
		{"custom key", "my-key", "my-key", 30},
		{"default key", "testkey", "testkey", 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewServer(tt.key)
			assert.Equal(t, tt.wantKey, srv.key)
			assert.Equal(t, int64(srv.queueTTL.Seconds()), tt.wantQueueTTL)
			assert.Equal(t, int64(srv.heartbeatTTL.Seconds()), int64(90))
			assert.NotNil(t, srv.clients)
			assert.NotNil(t, srv.queues)
			assert.NotNil(t, srv.disc)
			assert.NotNil(t, srv.peerLinks)
			assert.NotNil(t, srv.peerColls)
			assert.NotNil(t, srv.peerStats)
			assert.False(t, srv.startedAt.IsZero(), "startedAt must be set")
		})
	}
}
