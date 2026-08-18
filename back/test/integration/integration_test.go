//go:build integration

// Package integration 集成测试（第 6 项优化 2026-08-18 起默认脱外网）：
//   - 信令：TestMain 起全局自托管 signalserver（httptest 内存服务），
//     所有节点指向它——不再依赖 0.peerjs.com 公共云（无需代理/外网）
//   - 数据面：真实 WebRTC（同机双 pion host candidate 直连，无 STUN）
//   - 发现：自托管 /announce + /nodes API（替代 MQTT）
//   - 外网测试单独门控：
//     - PEERDRIVE_MQTT_TEST=1 → MQTT 公共 broker 发现测试（mqtt_test.go）
//     - PEERDRIVE_LIVE_TEST=1  → 线上全链路（live_test.go）
//
// 运行（无需外网，但 -p 1 串行必须：多组测试共享全局信令，并行互相干扰）：
//
//	cd back && go test -tags "nosqlite integration" ./test/integration/ -count=1 -p 1
package integration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/signalserver"
	"peerdrive/internal/transport"
)

// selfHostedURL 全局自托管信令 + 发现服务器（TestMain 启动，脱外网）。
// 第 6 项优化：集成测试不再依赖公共云信令——公共信令并行跑会互相干扰
// 且必须代理/外网，自托管 httptest 本地串行稳定。
var selfHostedURL string

// TestMain 启动全局自托管信令服务器（PeerJS 协议 + 发现 API）。
func TestMain(m *testing.M) {
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
	selfHostedURL = hs.URL
	code := m.Run()
	hs.Close()
	os.Exit(code)
}

// splitHostPort 从 httptest URL 拆 host/port。
func splitHostPort(url string) (string, string) {
	trimmed := strings.TrimPrefix(url, "http://")
	i := strings.LastIndex(trimmed, ":")
	return trimmed[:i], trimmed[i+1:]
}

// randID 生成唯一节点 ID（避免公共信令上 ID 冲突）。
func randID(prefix string) string {
	return fmt.Sprintf("%s-%s", prefix, randSuffix())
}

// randSuffix 4 字节随机 hex 后缀。
func randSuffix() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// writeTestFile 写入内容寻址测试文件（storage/{h[:2]}/{h}），返回 hash。
func writeTestFile(t *testing.T, storageDir string, content []byte) string {
	t.Helper()
	h := sha256Hex(content)
	dir := filepath.Join(storageDir, h[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, h), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return h
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// newService 构造启用 PeerJS 的 service（P2P/BT 关闭加速）。
// peers：静态对端列表（不设则仅被动接收/靠发现）。
// 第 6 项优化：信令默认指向全局自托管服务器（selfHostedURL），脱外网；
// mqtt=true 时发现走公共 broker（需 PEERDRIVE_MQTT_TEST=1，见 mqtt_test.go）。
func newService(t *testing.T, id, storageDir string, mqtt bool, peers []string, collections ...string) *transport.PeerJSService {
	t.Helper()
	// 文件索引等需要 repository.DB；集成测试每个 service 用独立内存库
	// （InitDB 重新 Open 覆盖全局单例——防止上一测试留下的 file_index 行
	// 污染本测试的 sync/list 断言。发现背景：verb 测试连跑时 sync 多出文件）。
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	cfg := config.Load()
	cfg.PeerJSEnable = true
	cfg.PeerJSID = id
	cfg.PeerJSHost, cfg.PeerJSPort = splitHostPort(selfHostedURL)
	cfg.PeerJSSecure = false
	cfg.PeerJSKey = "testkey"
	cfg.BTDHTEnabled = false
	cfg.PeerJSPeers = join(peers)
	cfg.MQTTEnable = mqtt
	if mqtt {
		cfg.MQTTBroker = "tcp://broker.emqx.io:1883"
		cfg.MQTTCollections = join(collections)
	}
	// H2：create 只允许 DownloadDir 根内的文件；测试统一把根指到 storageDir，
	// 需要 create 的测试把源文件写进 storageDir 即可。
	cfg.DownloadDir = storageDir
	svc := transport.NewPeerJSService(cfg, storageDir)
	svc.Start()
	t.Cleanup(svc.Close)
	return svc
}

// waitConnections 轮询等待与指定节点的连接建立。
// PEERDRIVE_SKIP_RTC=1（无 UDP 沙箱，如 docker 默认）时跳过互联类测试——
// 数据面 WebRTC 需要 UDP；本地 WS/admin 类测试不经过本函数不受影响。
func waitConnections(t *testing.T, svc *transport.PeerJSService, want map[string]bool, timeout time.Duration) {
	t.Helper()
	if os.Getenv("PEERDRIVE_SKIP_RTC") == "1" {
		t.Skip("PEERDRIVE_SKIP_RTC=1：无 UDP 环境跳过 WebRTC 互联测试")
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conns := svc.Connections()
		ok := true
		for id, wantConn := range want {
			_, has := conns[id]
			if has != wantConn {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := svc.Connections()
	t.Fatalf("连接状态未达预期 want=%v got=%v", want, keys(got))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func join(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ","
		}
		out += s
	}
	return out
}
