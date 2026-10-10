package transport

// nat.go: NAT 拓扑分类与对称型 NAT 盲拨避让 (Issue #322)。
//
// 架构定位：
// 针对国内家庭宽带和移动网络普遍采用 CGNAT（Symmetric NAT / 对称型 NAT）导致
// WebRTC 直连打洞成功率极低（通常低于 10%）的客观痛点：
// 1. 节点 NAT 类型分类规范：明确区分 NAT_PUBLIC、NAT_CONE 与 NAT_SYMMETRIC；
// 2. 拓扑感知拨号避让 (Avoid Wasteful Dialing)：当本地与远端同时处于 NAT_SYMMETRIC 时，
//    主动判定跳过直拨尝试，避免白白耗尽拨号预算并陷入 30 秒的静默超时等待；
// 3. SuperNode 中继支持：具备公网 IP 的节点可标记为 SuperNode，优先充当双向中继与枢纽。
//
// 安全与边界防线：
// 1. 输入规范化与白名单校验：对信令上报或对端声明的 NAT 类型与角色字符串进行严格白名单过滤，
//    拦截并丢弃任何畸形、超长或代码注入字符串。
// 2. 防御伪造与降级：未知类型统一收敛为 NAT_UNKNOWN，保持标准向后兼容。

import (
	"strings"
)

const (
	// NATTypePublic 公网直接可达（无 NAT 阻隔或 1:1 NAT）。
	NATTypePublic = "NAT_PUBLIC"

	// NATTypeCone 全锥型或限制锥型 NAT（打洞成功率极高，>85%）。
	NATTypeCone = "NAT_CONE"

	// NATTypeSymmetric 对称型 NAT（每次连接端口映射变化，打洞成功率极低，<10%）。
	NATTypeSymmetric = "NAT_SYMMETRIC"

	// NATTypeUnknown 未完成探测或无法识别的 NAT 类型。
	NATTypeUnknown = "NAT_UNKNOWN"
)

const (
	// NodeRolePeer 普通标准对等节点。
	NodeRolePeer = "peer"

	// NodeRoleSuperNode 拥有公网 IP 或优质网络的中继拓扑枢纽节点。
	NodeRoleSuperNode = "supernode"

	// NodeRoleEdge 边缘消费端节点。
	NodeRoleEdge = "edge"
)

// NormalizeNATType 严格归一化并校验 NAT 类型字符串。
func NormalizeNATType(raw string) string {
	cleaned := strings.ToUpper(strings.TrimSpace(raw))
	switch cleaned {
	case NATTypePublic:
		return NATTypePublic
	case NATTypeCone:
		return NATTypeCone
	case NATTypeSymmetric:
		return NATTypeSymmetric
	default:
		return NATTypeUnknown
	}
}

// NormalizeNodeRole 严格归一化节点角色。
func NormalizeNodeRole(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	switch cleaned {
	case NodeRoleSuperNode:
		return NodeRoleSuperNode
	case NodeRoleEdge:
		return NodeRoleEdge
	default:
		return NodeRolePeer
	}
}

// ShouldAttemptDirectDial 判定两个节点之间是否应当尝试发起 WebRTC 直接拨号。
//
// 避让规则：
// 1. 若对端为 SuperNode，其具备公网能力，必定尝试连接；
// 2. 若双方均为 NAT_SYMMETRIC（双对称 NAT），直连打洞几乎 100% 失败，
//    返回 false 避免无谓发起握手、浪费拨号配额并挂起 30 秒超时；
// 3. 其余情况（至少一方为公网或锥型 NAT，或状态未知）允许尝试直拨。
func ShouldAttemptDirectDial(localNAT, remoteNAT string, remoteRole string) bool {
	l := NormalizeNATType(localNAT)
	r := NormalizeNATType(remoteNAT)
	role := NormalizeNodeRole(remoteRole)

	// 1. 公网中继或 SuperNode 总是允许连接
	if role == NodeRoleSuperNode || r == NATTypePublic || l == NATTypePublic {
		return true
	}

	// 2. 双对称型 NAT 盲拨避让
	if l == NATTypeSymmetric && r == NATTypeSymmetric {
		return false
	}

	// 3. 锥型 NAT 或未知状态放行
	return true
}
