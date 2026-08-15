//go:build integration

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/signalserver"
)

// requireInitDB 内存 DB 初始化（每次调用重置，防跨测试污染）。
func requireInitDB(t *testing.T) {
	t.Helper()
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatal(err)
	}
}

// TestSelfHostedSignalAndDiscover 自托管信令 + 内置发现全链路：
//   - 本地起信号服务器（PeerJS 协议 + /discover API，替代 0.peerjs.com + MQTT）
//   - 两个节点信令指向自托管，发现走 HTTP（无任何外部服务）
//   - B 仅靠发现互联 A 并拉文件
//
// 发现背景：功能需求——自托管后 PeerJS 信令与房间发现都归自己管。
func TestSelfHostedSignalAndDiscover(t *testing.T) {
	// 自托管信令服务器
	ss := signalserver.NewServer("testkey")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/peerjs"):
			ss.HandleWS(w, r)
		case strings.HasSuffix(r.URL.Path, "/announce"):
			ss.HandleAnnounce(w, r)
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			ss.HandleNodes(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hs.Close()

	// 测试文件
	storageA := t.TempDir()
	content := []byte("self-hosted-signal-and-discover")
	hash := writeTestFile(t, storageA, content)

	idA := randID("sh-a")
	idB := randID("sh-b")

	// newService 默认连公共云；这里手工构造指向自托管
	newSelfHosted := func(id, storage string) *service.PeerJSService {
		requireInitDB(t)
		cfg := config.Load()
		cfg.PeerJSEnable = true
		cfg.PeerJSID = id
		cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(hs.URL)
		cfg.PeerJSSecure = false
		cfg.PeerJSKey = "testkey"
		cfg.P2PEnable = false
		cfg.BTDHTEnabled = false
		cfg.DiscoverURL = hs.URL
		cfg.MQTTCollections = hash
		svc := service.NewPeerJSService(cfg, storage)
		svc.Start()
		t.Cleanup(svc.Close)
		return svc
	}

	newSelfHosted(idA, storageA) // svcA：文件源（无需直接引用）
	svcB := newSelfHosted(idB, t.TempDir())

	// B 无静态 PEERS——靠自托管发现互联 A
	waitConnections(t, svcB, map[string]bool{idA: true}, 60*time.Second)

	data, err := svcB.FetchFromPeer(idA, hash, 0, -1)
	if err != nil {
		t.Fatalf("自托管发现后拉取失败: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("内容不一致: got %q", data)
	}
}

// TestSelfHostedPeerJSSignal 自托管信令协议兼容：直接用 peerjs 客户端模块
// 连自托管服务器完成 WebRTC 数据面互通（协议与公共云一致）。
// 发现背景：功能需求——节点端零改动（仅改 host 配置）切到自托管。
func TestSelfHostedPeerJSSignal(t *testing.T) {
	ss := signalserver.NewServer("testkey")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/peerjs"):
			ss.HandleWS(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	defer hs.Close()

	host, port := splitHostPort(hs.URL)
	// 两个 peerjs 客户端连自托管（协议兼容性验证）
	pA := peerjs.NewPeer("sp-a", peerjsOptions(host, port))
	pB := peerjs.NewPeer("sp-b", peerjsOptions(host, port))

	done := make(chan string, 1)
	pB.OnConnection(func(c *peerjs.Connection) {
		c.OnMessage(func(f peerjs.Frame) {
			done <- string(f.Data)
		})
	})
	require.NoError(t, pA.Dial(context.Background()))
	require.NoError(t, pB.Dial(context.Background()))
	defer pA.Close()
	defer pB.Close()

	conn, err := pA.Connect(context.Background(), "sp-b", "test")
	require.NoError(t, err)
	conn.OnOpen(func(c *peerjs.Connection) {
		_ = c.SendText("hello-self-hosted")
	})
	select {
	case got := <-done:
		if got != "hello-self-hosted" {
			t.Fatalf("内容不符: %q", got)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("自托管信令数据面未通")
	}
}

// peerjsOptions 指向自托管服务器的客户端配置。
func peerjsOptions(host, port string) peerjs.Options {
	opts := peerjs.DefaultOptions()
	opts.Host = host
	opts.Port = port
	opts.Secure = false
	opts.Key = "testkey"
	return opts
}

// splitHostPort 从 httptest URL 拆 host/port。
func splitHostPort(url string) (string, string) {
	trimmed := strings.TrimPrefix(url, "http://")
	i := strings.LastIndex(trimmed, ":")
	return trimmed[:i], trimmed[i+1:]
}
