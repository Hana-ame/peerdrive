package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
)

// 测试夹具。凭据值刻意不含任何真密钥成分：GitGuardian 的 generic
// "Username Password" 检测器会对着「形如密码的赋值」报警，哪怕值是占位串。
const (
	testStunURL  = "stun:stun.l.google.com:19302"
	testTurnURL  = "turn:turn.example.com:3478"
	testTurnUser = "peerdrive"
	testTurnPass = "test-only-not-a-credential"
)

// callWebRTCInfo 跑一次 handler 并把响应体解成 map。
func callWebRTCInfo(t *testing.T, cfg *config.Config) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	WebRTCInfoHandler(cfg)(c)

	require.Equal(t, http.StatusOK, w.Code)
	var out map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out))
	return out
}

// TestWebRTCInfoHandler_TurnCredentialsReachesBrowser 是 #144 的第二个回归点。
//
// 发现背景：只修服务端 ICEServer 不够——浏览器端的数据通道是独立一条 ICE
// agent，它从 GET /p2p/webrtc/info 拿配置。旧 handler 只吐 turn_server URL，
// 于是浏览器即使配好了 TURN 也同样拿不到凭据、同样产生不了 relay candidate，
// 而面板/下载器恰恰是走浏览器这条路。两条链路必须同时修。
func TestWebRTCInfoHandler_TurnCredentialsReachesBrowser(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WebRTCSTUNServer = testStunURL
	cfg.WebRTCTURNServer = testTurnURL
	cfg.WebRTCTURNUsername = testTurnUser
	cfg.WebRTCTURNPassword = testTurnPass

	out := callWebRTCInfo(t, cfg)

	assert.Equal(t, testStunURL, out["stun_server"])
	assert.Equal(t, true, out["turn_configured"])
	assert.Equal(t, testTurnURL, out["turn_server"])
	assert.Equal(t, testTurnUser, out["turn_username"])
	assert.Equal(t, testTurnPass, out["turn_password"])
}

// TestWebRTCInfoHandler_AnonymousTurnKeepsWireFormat 保护前端兼容：匿名 TURN
// 的部署不该凭空多出两个空字段，前端按下标/存在性读字段时那会是回归。
func TestWebRTCInfoHandler_AnonymousTurnKeepsWireFormat(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.WebRTCTURNServer = testTurnURL

	out := callWebRTCInfo(t, cfg)

	assert.Equal(t, true, out["turn_configured"])
	assert.Equal(t, testTurnURL, out["turn_server"])
	_, hasUser := out["turn_username"]
	_, hasPass := out["turn_password"]
	assert.False(t, hasUser, "匿名 TURN 不下发空的凭据字段")
	assert.False(t, hasPass, "匿名 TURN 不下发空的凭据字段")
}

// TestWebRTCInfoHandler_TurnUnset 钉住默认部署的响应形状：默认 TURN 关，
// 此时 turn_configured 必须是 false，让前端能区分「没配 TURN」与
// 「配了但凭据缺失」——这两者在 #144 之前是无法区分的静默失败。
func TestWebRTCInfoHandler_TurnUnset(t *testing.T) {
	cfg := config.DefaultConfig()

	out := callWebRTCInfo(t, cfg)

	assert.Equal(t, testStunURL, out["stun_server"])
	assert.Equal(t, false, out["turn_configured"])
	_, hasTurn := out["turn_server"]
	assert.False(t, hasTurn, "未配 TURN 时不出现 turn_server（保持旧行为）")
	_, hasUser := out["turn_username"]
	assert.False(t, hasUser, "凭据已填但 TURN 未配时也不下发")
}
