//go:build integration

package integration

import (
	"testing"
	"time"

	"peerdrive/internal/transport"
)

// TestMQTTDiscovery 两个节点通过 MQTT 分片房间互相发现（无任何静态配置）。
// 覆盖：announce 发布、分片 topic 订阅、onPeer 回调、心跳幂等。
//
// 发现背景：功能测试——MQTT 分片房间互相发现（announce/订阅/心跳兜底）
func TestMQTTDiscovery(t *testing.T) {
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	gotA := make(chan string, 4)
	gotB := make(chan string, 4)
	discA := transport.NewMQTTDiscovery("tcp://broker.emqx.io:1883", "peerdrive/v1/test", "pd-disc-a-"+randSuffix(), func(id string) { gotA <- id })
	discB := transport.NewMQTTDiscovery("tcp://broker.emqx.io:1883", "peerdrive/v1/test", "pd-disc-b-"+randSuffix(), func(id string) { gotB <- id })

	idA := "mqtt-node-a-" + randSuffix()
	idB := "mqtt-node-b-" + randSuffix()

	discA.Start([]string{hash})
	discB.Start([]string{hash})
	defer discA.Stop()
	defer discB.Stop()

	discA.Announce(idA, []string{hash})
	discB.Announce(idB, []string{hash})

	// A 应收到 B 的 announce，B 应收到 A 的
	waitFor := func(ch chan string, want string, timeout time.Duration) {
		t.Helper()
		deadline := time.After(timeout)
		for {
			select {
			case got := <-ch:
				if got == want {
					return
				}
			case <-deadline:
				t.Fatalf("未收到 %s 的 announce（MQTT 发现失败）", want)
			}
		}
	}
	waitFor(gotA, idB, 60*time.Second)
	waitFor(gotB, idA, 60*time.Second)
}

// TestMQTTDiscoverThenPeerJSInterop MQTT 发现 → PeerJS 互联 → 拉文件：
// B 完全不知道 A 的 peer id（无静态配置），仅通过 MQTT 分片发现后经
// 公共云信令直连 A 拉取文件——覆盖发现与互联全链路。
//
// 发现背景：功能测试——MQTT 发现 → PeerJS 互联 → 拉文件全链路
func TestMQTTDiscoverThenPeerJSInterop(t *testing.T) {
	storageA := t.TempDir()
	content := []byte("mqtt-discovered-peerjs-transfer")
	hash := writeTestFile(t, storageA, content)

	// A：MQTT 开启，announce 自己的 peer id
	svcA := newService(t, randID("it-ma"), storageA, true, nil, hash)
	// B：MQTT 开启，无 PEERS 配置——靠发现互联
	svcB := newService(t, randID("it-mb"), t.TempDir(), true, nil, hash)

	// 等 B 发现并连上 A
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 120*time.Second)

	data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
	if err != nil {
		t.Fatalf("MQTT 发现后拉取失败: %v", err)
	}
	if string(data) != string(content) {
		t.Fatalf("内容不一致: got %q", data)
	}
}
