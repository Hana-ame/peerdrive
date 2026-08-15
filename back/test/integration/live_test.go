//go:build integration

package integration

import (
	"bytes"
	"context"
	"os"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
)

// 线上部署验证：以 peersignal.moonchan.xyz（cloudcone 自托管信令 + 发现）为
// 信令服务器的完整测试流。运行：PEERDRIVE_LIVE_TEST=1 go test -tags "nosqlite integration" ./test/integration/ -run TestLive -v
// 注意：cloudcone 直连可达（不走宿主机代理），跑测试时不要设 HTTPS_PROXY 指向代理。
const (
	liveSignalHost = "peersignal.moonchan.xyz"
	liveSignalKey  = "pd-signal-b9447b406828e500"
	liveDiscover   = "https://peersignal.moonchan.xyz"
)

// TestLiveSignal_DiscoveryAndInterop 线上信令 + HTTP 发现 + WebRTC 拉文件全链路。
// 发现背景：部署验证——自托管服务器上线后，节点零改动（仅改 host/key/discover）
// 走线上信令互联并拉取文件。
func TestLiveSignal_DiscoveryAndInterop(t *testing.T) {
	if os.Getenv("PEERDRIVE_LIVE_TEST") != "1" {
		t.Skip("线上测试需 PEERDRIVE_LIVE_TEST=1（会向 peersignal.moonchan.xyz 注册节点）")
	}

	storageA := t.TempDir()
	content := []byte("live-signal-test-payload")
	hash := writeTestFile(t, storageA, content)

	idA := randID("live-a")
	idB := randID("live-b")

	newLive := func(id, storage string) *service.PeerJSService {
		if err := repository.InitDB(":memory:"); err != nil {
			t.Fatal(err)
		}
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost = liveSignalHost
		cfg.PeerJSPort = "443"
		cfg.PeerJSSecure = true
		cfg.PeerJSKey = liveSignalKey
		cfg.P2PEnable = false
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = liveDiscover
		cfg.MQTTCollections = hash
		svc := service.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newLive(idA, storageA) // 文件源
	svcB := newLive(idB, t.TempDir())

	// B 无静态 PEERS——靠线上发现互联 A
	waitConnections(t, svcB, map[string]bool{idA: true}, 90*time.Second)

	data, err := svcB.FetchFromPeer(idA, hash, 0, -1)
	if err != nil {
		t.Fatalf("线上信令拉取失败: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("内容不一致: got %q", data)
	}
}

// TestLiveSignal_ProtocolCompat 线上信令协议兼容：peerjs 客户端模块
// 直连线上完成 WebRTC 数据面互通（验证 wss 部署 + 协议零差异）。
//
// 发现背景：功能验收——线上部署协议兼容：peerjs 客户端直连线上 wss 数据面互通
func TestLiveSignal_ProtocolCompat(t *testing.T) {
	if os.Getenv("PEERDRIVE_LIVE_TEST") != "1" {
		t.Skip("线上测试需 PEERDRIVE_LIVE_TEST=1")
	}

	pA := peerjs.NewPeer("live-pc-a-"+randSuffix(), liveOptions())
	pB := peerjs.NewPeer("live-pc-b-"+randSuffix(), liveOptions())

	done := make(chan string, 1)
	pB.OnConnection(func(c *peerjs.Connection) {
		c.OnMessage(func(f peerjs.Frame) {
			done <- string(f.Data)
		})
	})
	if err := pA.Dial(context.Background()); err != nil {
		t.Fatalf("A dial: %v", err)
	}
	if err := pB.Dial(context.Background()); err != nil {
		t.Fatalf("B dial: %v", err)
	}
	defer pA.Close()
	defer pB.Close()

	conn, err := pA.Connect(context.Background(), pB.ID(), "live")
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	conn.OnOpen(func(c *peerjs.Connection) {
		_ = c.SendText("hello-live")
	})
	select {
	case got := <-done:
		if got != "hello-live" {
			t.Fatalf("内容不符: %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("线上信令数据面未通")
	}
}

// liveOptions 线上信令客户端配置。
func liveOptions() peerjs.Options {
	opts := peerjs.DefaultOptions()
	opts.Host = liveSignalHost
	opts.Port = "443"
	opts.Secure = true
	opts.Key = liveSignalKey
	return opts
}
