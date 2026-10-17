package transport

import (
	"testing"

	"github.com/pion/webrtc/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 测试夹具。凭据值刻意不含任何真密钥成分：GitGuardian 的 generic
// "Username Password" 检测器会对着「形如密码的赋值」报警，哪怕值是占位串。
const (
	testStunURL  = "stun:stun.l.google.com:19302"
	testTurnURL  = "turn:turn.example.com:3478"
	testTurnUser = "peerdrive"
	testTurnPass = "test-only-not-a-credential"
)

// TestParseICEServers_TurnCarriesCredentials 是 #144 的回归测试。
//
// 发现背景：TURN 之前只被拼成 `webrtc.ICEServer{URLs: ...}`，没有
// Username / Password / CredentialType。认证 TURN 服务器（如 coturn）收到无凭据的
// Allocate 请求会直接拒绝，relay candidate 永不产生——而对称 NAT 只有 relay
// candidate 这条出路。所以「配了 TURN 却依然连不上」，且没有任何日志线索。
// 这条用例把凭据必须真正进入 ICEServer 钉死。
func TestParseICEServers_TurnCarriesCredentials(t *testing.T) {
	servers := parseICEServers(
		testStunURL,
		"turn:turn.example.com:3478,turns:turn.example.com:5349",
		testTurnUser,
		testTurnPass,
	)

	require.Len(t, servers, 2, "STUN 与 TURN 各一条")

	stun := servers[0]
	assert.Equal(t, []string{testStunURL}, stun.URLs)
	assert.Empty(t, stun.Username, "STUN 不带凭据")

	turn := servers[1]
	// 逗号分隔的多个 TURN URL 必须保持在同一条 ICEServer 上（Pion 语义），
	// 拆成多条会变成两条独立的 Allocate，行为不同。
	assert.Equal(t, []string{testTurnURL, "turns:turn.example.com:5349"}, turn.URLs)
	assert.Equal(t, testTurnUser, turn.Username)
	// Pion names the W3C "credential" field `Credential interface{}`; under
	// CredentialType=password it must carry the password string. Asserting on
	// the concrete string (not just non-nil) matters because a wrong type here
	// only fails at Allocate time against a live server, not at build time.
	assert.Equal(t, testTurnPass, turn.Credential)
	assert.Equal(t, webrtc.ICECredentialTypePassword, turn.CredentialType,
		"配了用户名就必须声明凭据类型，否则 Pion 不知道用哪个字段去认证")
}

// TestParseICEServers_AnonymousTurnUnchanged 保护兼容面：匿名 TURN
// （只给 URL、不配凭据）是合法部署，不能被这次改动改变行为——特别是不能
// 无端填一个空的 CredentialType，Pion 见到会当成「有凭据但为空」而认证失败。
func TestParseICEServers_AnonymousTurnUnchanged(t *testing.T) {
	servers := parseICEServers("", testTurnURL, "", "")

	require.Len(t, servers, 1)
	assert.Equal(t, []string{testTurnURL}, servers[0].URLs)
	assert.Empty(t, servers[0].Username)
	assert.Empty(t, servers[0].Credential)
	assert.Empty(t, servers[0].CredentialType, "匿名 TURN 不声明凭据类型")
}

// TestParseICEServers_TurnUnsetIgnoresCredentials 覆盖一个配置陷阱：
// 凭据填了但 TURN URL 没填时，不应该凭空造出一条 TURN server——否则
// 「忘配 URL」会表现成「连不上 TURN」而不是「没配 TURN」，比原来更难排查。
func TestParseICEServers_TurnUnsetIgnoresCredentials(t *testing.T) {
	servers := parseICEServers(testStunURL, "", testTurnUser, testTurnPass)

	require.Len(t, servers, 1, "只应有 STUN 一条")
	assert.Equal(t, []string{testStunURL}, servers[0].URLs)
}

// TestParseICEServers_EmptyYieldsNothing 钉住默认值路径：两个都空时返回 nil，
// 服务照常建 PeerConnection（内网 host candidate 就够，见 REFACTOR §ICE）。
func TestParseICEServers_EmptyYieldsNothing(t *testing.T) {
	assert.Empty(t, parseICEServers("", "", "", ""))
}

// TestParseICEServers_PasswordOnlyStillSetsCredentialType 覆盖只设了密码、
// 用户名留空的半配置状态。此时仍然要声明 CredentialType：TURN 服务器可能
// 只校验密码，若把类型留空，Pion 会退回不认证的请求，症状同样是静默失败。
func TestParseICEServers_PasswordOnlyStillSetsCredentialType(t *testing.T) {
	servers := parseICEServers("", testTurnURL, "", testTurnPass)

	require.Len(t, servers, 1)
	assert.Empty(t, servers[0].Username)
	assert.Equal(t, testTurnPass, servers[0].Credential)
	assert.Equal(t, webrtc.ICECredentialTypePassword, servers[0].CredentialType)
}
