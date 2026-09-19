//go:build integration

package integration

// peer_pull_test.go：跨节点拉取保存端到端（doc/NETDISK.md M3）。
//
// 覆盖用户主诉求："选中文件保存就可以从别人那里下载"——A 持有内容（只有
// 内容寻址存储、没有其它索引），B 通过真实 WebRTC 把内容拉过来、校验 sha256、
// 落盘、并登记进本地文件索引（此后 B 的"我的文件"能看到，且 B 能把它继续
// 服务给第三个节点）。
//
// 发现背景（网盘目标）：此前只有 FetchFromPeer（整包驻留内存、64MB HTTP 上限）
// 和 source.p2p 按需回源（不落盘）。"保存到我的网盘"需要流式落盘 + 内容校验
// + 索引登记三件事一起，缺一个用户都会觉得"下下来了但找不到文件"。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"
)

// TestPeerPullSavesToLocalDrive A 持有内容 → B 拉取保存 → 落盘 + 登记 + 可再次服务。
func TestPeerPullSavesToLocalDrive(t *testing.T) {
	requireInitDB(t)

	storageA := t.TempDir()
	content := []byte("peer-pull-e2e-content-0123456789")
	hash := writeTestFile(t, storageA, content)

	newNode := func(id, storage, downloadDir string) *transport.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = selfHostedURL
		cfg.DiscoverPresence = true
		cfg.MQTTCollections = ""
		// downloadDir 必须显式指定：节点内部的 file_index 以它为
		// 「允许根目录」（H2 安全边界），保存目录必须落在同一个根里，
		// 否则登记被拒 → serveFile 回退 CAS → 保存的文件服务不出去。
		if downloadDir != "" {
			cfg.DownloadDir = downloadDir
		}
		svc := transport.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	svcA := newNode(randID("pull-a"), storageA, "")
	// B 的保存目录 = 它的 file_index 允许根目录（生产部署里也是 cfg.DownloadDir）
	downloadRoot := t.TempDir()
	svcB := newNode(randID("pull-b"), t.TempDir(), downloadRoot)
	// C 提前建好：requireInitDB 会重置全局内存 DB（见 integration_test.go），
	// 拉取完成后再建节点会把 B 的索引登记冲掉——那会让"保存后可继续服务"
	// 的断言失败于测试自身的副作用，而不是产品行为。
	svcC := newNode(randID("pull-c"), t.TempDir(), "")

	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// B 侧拉取服务：复用节点自己的 file_index（真实链路，不做测试替身）
	idxB := svcB.FileIndex()
	puller := service.NewPeerPuller(downloadRoot)
	puller.SetSource(svcB)
	puller.SetFileAccess(
		func(h string) bool {
			fi, err := idxB.Info(h)
			return err == nil && fi != nil && fi.Path != "" && fi.Size > 0
		},
		func(path string) (string, int64, error) {
			fi, err := idxB.Create(path)
			if err != nil {
				return "", 0, err
			}
			return fi.Hash, fi.Size, nil
		},
	)

	job, err := puller.Start(svcA.ID(), hash, "saved.bin", "from-peer/saved.bin", "")
	require.NoError(t, err, "启动拉取失败")

	// 等终态
	var done service.PullJob
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		j, ok := puller.Get(job.ID)
		require.True(t, ok)
		if j.Done() {
			done = j
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.Equal(t, service.PullDone, done.Status, "拉取未成功: err=%s", done.Error)
	require.False(t, done.Skipped, "本地没有该内容，不应跳过")

	// ① 落盘位置保留了对端给的相对结构
	want := filepath.Join(downloadRoot, "pulled", "from-peer", "saved.bin")
	require.Equal(t, want, done.SavedTo)
	got, err := os.ReadFile(want)
	require.NoError(t, err, "保存的文件读不到")
	require.Equal(t, string(content), string(got))
	require.Equal(t, int64(len(content)), done.Received)

	// ② 已登记进本地索引（"我的文件"可见），且能按 hash 查到
	fi, err := idxB.Info(hash)
	require.NoError(t, err, "登记后 info 应可查到")
	require.Equal(t, hash, fi.Hash)
	require.Equal(t, want, fi.Path)

	// ③ 再次"保存"同一内容 → 跳过（内容寻址去重）
	again, err := puller.Start(svcA.ID(), hash, "saved.bin", "from-peer/saved.bin", "")
	require.NoError(t, err)
	deadline = time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if j, ok := puller.Get(again.ID); ok && j.Done() {
			require.Equal(t, service.PullDone, j.Status)
			require.True(t, j.Skipped, "本地已有同内容，应跳过下载")
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	// ④ 保存下来的文件可被本节点继续服务（B 现在是持有者）。
	//    这里用 B 自己的入站路径验证：C 从 B 拉同一 hash 应成功。
	waitConnections(t, svcC, map[string]bool{svcB.ID(): true}, 60*time.Second)
	data, err := svcC.FetchFromPeer(svcB.ID(), hash, 0, -1)
	require.NoError(t, err, "B 保存后应能把内容服务给其它节点")
	require.Equal(t, string(content), string(data))
}
