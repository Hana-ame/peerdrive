package transport

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	hashutil "peerdrive/pkg/hashutil"
)

// TestPresenceRoom_IsStrictSHA256 存在房间名必须是合法 64hex，且确实是 sha256 值。
// 发现背景（互联层）：存在房间让「零共享 collection 的两个节点」也能互相发现，
// 但它会被塞进 announce 的 collections 字段。peerdrive 自己的 signalserver 对该
// 字段只 trim 不校验，而线上信令由 wintools 维护、实现未知——一旦某天对方做
// 「必须 64hex」校验，可读房间名（如 "_presence"）会让**整条 announce 被 400 拒掉**，
// 连带内容分片房间一起登记不上，发现全断。本测试把「必须是合法 strict sha256」
// 钉死，防止后来者把它改成可读名字。
func TestPresenceRoom_IsStrictSHA256(t *testing.T) {
	assert.True(t, hashutil.IsStrictSHA256(PresenceRoom),
		"存在房间名必须是 64 位小写 hex（当前 %q）", PresenceRoom)
	assert.True(t, hashutil.IsValidSHA256(PresenceRoom))
}

// TestDiscoveryRooms_PresenceToggle 房间列表 = 配置内容分片 + 可选存在房间。
// 发现背景（互联层）：节点级互联（存在房间）与内容分片发现是叠加关系——
// 关掉存在房间必须精确回到「只有配置声明的内容房间」，不能有残留，
// 否则 PEERDRIVE_DISCOVER_PRESENCE=false 关不干净。
func TestDiscoveryRooms_PresenceToggle(t *testing.T) {
	const coll = "1111111111111111111111111111111111111111111111111111111111111111"

	t.Run("开启存在房间时叠加且去重", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = coll
		svc.cfg.DiscoverPresence = true

		rooms := svc.discoveryRooms()
		assert.Equal(t, []string{coll, PresenceRoom}, rooms)
	})

	t.Run("关闭存在房间时只有配置房间", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = coll
		svc.cfg.DiscoverPresence = false

		rooms := svc.discoveryRooms()
		assert.Equal(t, []string{coll}, rooms)
	})

	t.Run("未配置任何内容房间时只剩存在房间", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = ""
		svc.cfg.DiscoverPresence = true

		// 这正是默认部署的形态：互联层必须能在「没有共享内容 hash」时独立工作。
		rooms := svc.discoveryRooms()
		require.Equal(t, []string{PresenceRoom}, rooms)
	})

	t.Run("运营者误把存在房间写进配置时不重复", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = PresenceRoom
		svc.cfg.DiscoverPresence = true

		assert.Equal(t, []string{PresenceRoom}, svc.discoveryRooms())
	})

	t.Run("非法内容房间被过滤但仍带存在房间", func(t *testing.T) {
		svc := newTestPeerJSService(t)
		svc.cfg.MQTTCollections = "not-a-hash," + coll
		svc.cfg.DiscoverPresence = true

		assert.Equal(t, []string{coll, PresenceRoom}, svc.discoveryRooms())
	})
}

// TestDiscoveryDialAllowed_MaxPeers 发现拨号预算受 PEERDRIVE_MAX_PEERS 约束。
// 发现背景（互联层）：存在房间让「任意节点都能发现任意节点」，若发现即拨号，
// 节点数一多就退化成 O(n²) 全互联（每对节点一条 WebRTC 连接）；用既有但一直
// 没被使用的 PEERDRIVE_MAX_PEERS 兜住上限。几个易错点单独锁住：
//   - "local"（浏览器直连本节点的本地 WS 会话）不是对端节点，不得占用预算；
//   - 未配置/<=0 表示不限，不能因为默认零值把互联彻底关死。
func TestDiscoveryDialAllowed_MaxPeers(t *testing.T) {
	newWithConns := func(maxPeers int, ids ...string) *PeerJSService {
		svc := newTestPeerJSService(t)
		svc.cfg.MaxPeers = maxPeers
		for _, id := range ids {
			svc.conns[id] = &fakeSession{id: id}
		}
		return svc
	}

	t.Run("达到上限后拒绝", func(t *testing.T) {
		svc := newWithConns(2, "a", "b")
		assert.False(t, svc.discoveryDialAllowed())
	})

	t.Run("未达上限允许", func(t *testing.T) {
		svc := newWithConns(2, "a")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("local 会话不占预算", func(t *testing.T) {
		svc := newWithConns(1, "local")
		assert.True(t, svc.discoveryDialAllowed(), "本地 WS 会话不是对端节点")
	})

	t.Run("未配置上限时视为不限", func(t *testing.T) {
		svc := newWithConns(0, "a", "b", "c", "d", "e", "f", "g", "h", "i")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("负数上限视为不限", func(t *testing.T) {
		svc := newWithConns(-1, "a", "b", "c")
		assert.True(t, svc.discoveryDialAllowed())
	})

	t.Run("上限为 1 时 local 之后仍可拨一个对端", func(t *testing.T) {
		svc := newWithConns(1, "local")
		assert.True(t, svc.discoveryDialAllowed())
		svc.conns["real"] = &fakeSession{id: "real"}
		assert.False(t, svc.discoveryDialAllowed())
	})
}

// TestMaxPeers_ZeroMeansUnlimited 显式锁 maxPeers 的兜底语义（<=0 → 极大值）。
func TestMaxPeers_ZeroMeansUnlimited(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.MaxPeers = 0
	assert.Greater(t, svc.maxPeers(), 1<<20, "0 必须解释为不限，而不是 0 个对端")

	svc.cfg.MaxPeers = 3
	assert.Equal(t, 3, svc.maxPeers())
}
