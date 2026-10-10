package transport

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNAT_NormalizationAndBoundary 验证输入清洗、异常注入与边界容错。
// 发现背景：Issue #322 要求对信令上报或对端声明的 NAT 类型与角色做严格白名单验证，防止注入。
func TestNAT_NormalizationAndBoundary(t *testing.T) {
	t.Run("valid NAT normalization", func(t *testing.T) {
		assert.Equal(t, NATTypePublic, NormalizeNATType("NAT_PUBLIC"))
		assert.Equal(t, NATTypePublic, NormalizeNATType("  nat_public  "))
		assert.Equal(t, NATTypeCone, NormalizeNATType("nat_cone"))
		assert.Equal(t, NATTypeSymmetric, NormalizeNATType("nat_symmetric"))
	})

	t.Run("invalid and malicious NAT input fallback to unknown", func(t *testing.T) {
		maliciousInputs := []string{
			"",
			"   ",
			"NAT_INJECTION\x00_ATTACK",
			"../../../etc/passwd",
			strings.Repeat("A", 1000),
			"<script>alert(1)</script>",
			"UNKNOWN_TYPE",
		}
		for _, in := range maliciousInputs {
			assert.Equal(t, NATTypeUnknown, NormalizeNATType(in))
		}
	})

	t.Run("valid NodeRole normalization", func(t *testing.T) {
		assert.Equal(t, NodeRoleSuperNode, NormalizeNodeRole("SUPERNODE"))
		assert.Equal(t, NodeRoleSuperNode, NormalizeNodeRole(" supernode "))
		assert.Equal(t, NodeRoleEdge, NormalizeNodeRole("edge"))
		assert.Equal(t, NodeRolePeer, NormalizeNodeRole("peer"))
		assert.Equal(t, NodeRolePeer, NormalizeNodeRole("unknown_role_defaults_to_peer"))
	})
}

// TestNAT_ShouldAttemptDirectDial 验证对称型 NAT 盲拨避让与 SuperNode 中继规则。
// 发现背景：验证国内对称型 NAT 场景下避免发起注定超时的无谓握手，节约连接配额。
func TestNAT_ShouldAttemptDirectDial(t *testing.T) {
	// 1. 双对称型 NAT 互连：必须判定为 false（避免 30s 超时与无谓握手）
	assert.False(t, ShouldAttemptDirectDial(NATTypeSymmetric, NATTypeSymmetric, NodeRolePeer))

	// 2. 至少一方为锥型 NAT (NAT_CONE)：允许尝试打洞直拨
	assert.True(t, ShouldAttemptDirectDial(NATTypeCone, NATTypeSymmetric, NodeRolePeer))
	assert.True(t, ShouldAttemptDirectDial(NATTypeSymmetric, NATTypeCone, NodeRolePeer))
	assert.True(t, ShouldAttemptDirectDial(NATTypeCone, NATTypeCone, NodeRolePeer))

	// 3. 至少一方为公网 (NAT_PUBLIC)：允许直拨
	assert.True(t, ShouldAttemptDirectDial(NATTypePublic, NATTypeSymmetric, NodeRolePeer))
	assert.True(t, ShouldAttemptDirectDial(NATTypeSymmetric, NATTypePublic, NodeRolePeer))

	// 4. 对端为 SuperNode：即便本地是对称 NAT 也必须允许拨号
	assert.True(t, ShouldAttemptDirectDial(NATTypeSymmetric, NATTypeSymmetric, NodeRoleSuperNode))

	// 5. 状态未知：向后兼容默认放行
	assert.True(t, ShouldAttemptDirectDial(NATTypeUnknown, NATTypeSymmetric, NodeRolePeer))
	assert.True(t, ShouldAttemptDirectDial(NATTypeSymmetric, NATTypeUnknown, NodeRolePeer))
}
