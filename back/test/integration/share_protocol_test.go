//go:build integration

package integration

// share_protocol_test.go：共享清单帧的**协议契约**测试（doc/NETDISK.md M2/M5）。
//
// 为什么用 Go↔Go 来验证 JS 客户端的契约：纯 WebRTC 消费端
// （packages/peerdrive-client，M5）在 CI 里跑不了真实浏览器，但它与节点之间
// 的约定只是几个帧类型 + 字段名。这里用**完全相同的帧序列**
// （share → share-resp → req → meta/data/done）在两个真实节点上跑一遍，
// 把字段名与语义钉死；JS 侧再对同一批字段名做单测。两边共同构成互通证据。
//
// 发现背景（网盘目标）：用户要"加入节点后看到文件链接（打包好的 collection
// 或者单独的文件），选中保存就能从别人那里下载"。"看到文件链接"依赖 share
// 帧把合集的条目（path+hash）一起下发——只给合集名，用户点不进去。

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestShareProtocolContract 显式共享一个 public 合集 → 对端经 share 帧拿到
// 完全一致的清单，并能用条目里的 hash 直接拉取内容（"选中保存"的协议基础）。
func TestShareProtocolContract(t *testing.T) {
	requireInitDB(t)

	// 匿名合集存储目录是包级全局（repository.SetAnonStorageDir），单进程
	// 双节点测试里 A/B 共用它——不影响本测试：只有 A 声明了共享。
	anonDir := t.TempDir()
	repository.SetAnonStorageDir(anonDir)

	content := []byte("contract-collection-content")
	// 先把内容放进内容寻址存储（合集条目的 hash 就是它的 sha256）
	fileHash := writeTestFile(t, anonDir, content)

	cfgA := config.Load()
	anonReader := service.NewAnonService(cfgA)
	collHash, err := anonReader.CreateCollection("契约合集", []model.AnonCollectionEntry{
		{Path: "docs/readme.txt", Providers: []model.Provider{{Type: "sha256", Value: fileHash, MimeType: "text/plain"}}},
	}, []string{"contract"})
	require.NoError(t, err, "创建合集失败")

	// A：开启共享，显式声明这个 public 合集
	cfgA.PeerJSEnable = true
	cfgA.PeerJSID = randID("cshare-a")
	cfgA.PeerJSHost, cfgA.PeerJSPort = splitHostPort(selfHostedURL)
	cfgA.PeerJSSecure = false
	cfgA.PeerJSKey = "testkey"
	cfgA.BTDHTEnabled = false
	cfgA.DiscoverURL = selfHostedURL
	cfgA.DiscoverPresence = true
	cfgA.MQTTCollections = ""
	cfgA.ShareEnable = true
	cfgA.ShareCollections = collHash
	svcA := transport.NewPeerJSService(cfgA, anonDir)
	shareSvc := service.NewNodeShare(cfgA, "") // "" = 内存模式（集成测试不落盘）
	shareSvc.SetAnonAccess(anonReader.GetCollectionByHash, anonReader.ListCollections)
	svcA.SetShareProvider(shareSvc.SnapshotFor)
	svcA.Start()
	t.Cleanup(svcA.Close)

	// 本地自检：快照解析必须命中这个合集（不含则后续断言会误判为"对端没回"）
	snap := shareSvc.Snapshot()
	require.Len(t, snap.Collections, 1, "本地共享快照应含 1 个合集: %+v", snap)
	require.Equal(t, 0, len(snap.Files), "未配置共享目录 → 不应有单文件")

	// B：未开启共享，只作为消费方
	cfgB := config.Load()
	cfgB.PeerJSEnable = true
	cfgB.PeerJSID = randID("cshare-b")
	cfgB.PeerJSHost, cfgB.PeerJSPort = splitHostPort(selfHostedURL)
	cfgB.PeerJSSecure = false
	cfgB.PeerJSKey = "testkey"
	cfgB.BTDHTEnabled = false
	cfgB.DiscoverURL = selfHostedURL
	cfgB.DiscoverPresence = true
	cfgB.MQTTCollections = ""
	svcB := transport.NewPeerJSService(cfgB, t.TempDir())
	svcB.Start()
	t.Cleanup(svcB.Close)

	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// ── 契约断言 1：share 帧拉到的清单 ──
	remote, err := svcB.RequestShares(svcA.ID())
	require.NoError(t, err, "share 帧请求失败")
	require.Len(t, remote.Collections, 1, "对端共享清单应含 1 个合集")
	got := remote.Collections[0]
	require.Equal(t, collHash, got.Hash)
	require.Equal(t, "契约合集", got.Name, "friendly_name 未随 share 下发")
	require.Equal(t, int64(1), got.Size, "size 语义 = 条目数")
	require.Len(t, got.Entries, 1, "合集条目必须随 share 下发（否则客户端点不进去）")
	require.Equal(t, "docs/readme.txt", got.Entries[0].Path)
	require.Equal(t, fileHash, got.Entries[0].Hash)
	require.Equal(t, "text/plain", got.Entries[0].Mime)
	require.Empty(t, remote.Files, "未配置共享目录 → 单文件清单为空")
	// 空清单必须是空切片而非 nil（前端直接 .map，不吃 null）
	require.NotNil(t, remote.Collections)
	require.NotNil(t, remote.Files)

	// ── 契约断言 2：用清单里的 hash 直接拉内容（"选中 → 保存"的第一步） ──
	data, err := svcB.FetchFromPeer(svcA.ID(), got.Entries[0].Hash, 0, -1)
	require.NoError(t, err, "按 share 清单里的 hash 拉取失败")
	require.Equal(t, string(content), string(data), "拉取内容与源不一致")

	// ── 契约断言 3：未开启共享的节点回空清单（而不是 err） ──
	empty, err := svcA.RequestShares(svcB.ID())
	require.NoError(t, err, "对端未开启共享时 share 应成功返回空清单")
	require.Empty(t, empty.Collections)
	require.Empty(t, empty.Files)
}
