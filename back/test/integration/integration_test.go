//go:build integration

// Package integration 覆盖真实网络的集成测试：
//   - 双节点 peerjs 互通（公共云信令 0.peerjs.com）
//   - MQTT 分片发现（公共 broker broker.emqx.io）
//   - MQTT 发现 + peerjs 互联拉文件
//   - 3+ 节点互通
//
// 运行（需要外网 + 代理）：
//
//	cd back && HTTPS_PROXY=... GOPROXY=... go test -tags "nosqlite integration" ./test/integration/ -count=1 -v
package integration

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/transport"
)

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
// peers：静态对端列表（不设则仅被动接收/靠 MQTT 发现）。
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
	cfg.PeerJSHost = "0.peerjs.com"
	cfg.PeerJSPort = "443"
	cfg.PeerJSSecure = true
	cfg.PeerJSKey = "peerjs"
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
func waitConnections(t *testing.T, svc *transport.PeerJSService, want map[string]bool, timeout time.Duration) {
	t.Helper()
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
