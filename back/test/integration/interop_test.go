//go:build integration

package integration

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"peerdrive/internal/service"
)

// TestTwoNodesInterop 双节点互通：B 经公共云信令 + WebRTC 直连 A 拉文件，
// 校验内容与 sha256 一致（覆盖帧协议 req/meta/data/done 全链路）。
//
// 发现背景：功能验收——双节点经公共云信令互通基线（WebRTC 拉取 + sha256 校验）
func TestTwoNodesInterop(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 700*1024) // 跨多个 64KB chunk
	for i := range content {
		content[i] = byte(i * 7)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("it-a"), randID("it-b")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})

	waitConnections(t, svcA, map[string]bool{svcB.ID(): true}, 60*time.Second)
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// B 从 A 拉取
	data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
	if err != nil {
		t.Fatalf("FetchFromPeer: %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("内容不一致: got %d bytes, want %d", len(data), len(content))
	}
	if got := sha256Hex(data); got != hash {
		t.Fatalf("sha256 不一致: got %s want %s", got, hash)
	}

	// 反向：A 从 B 拉（B 没有该文件 → 应报错）
	if _, err := svcA.FetchFromPeer(svcB.ID(), hash, 0, -1); err == nil {
		t.Fatal("B 没有该文件却拉取成功（应失败）")
	}
}

// TestTwoNodesRangeFetch 分片/range 拉取：offset+size 只取中间一段。
//
// 发现背景：功能测试——range（offset/size）语义：只取中间一段
func TestTwoNodesRangeFetch(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 300*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("it-ra"), randID("it-rb")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	const offset = 100 * 1024
	const size = 50 * 1024
	data, err := svcB.FetchFromPeer(svcA.ID(), hash, offset, size)
	if err != nil {
		t.Fatalf("FetchFromPeer(range): %v", err)
	}
	if !bytes.Equal(data, content[offset:offset+size]) {
		t.Fatalf("range 内容不一致: got %d bytes", len(data))
	}
}

// TestThreeNodesInterop 3 节点互通：A(文件源) + B + C，
// B/C 都从 A 拉取；C 也验证与 B 互联（三方两两可见）。
//
// 发现背景：功能测试——三节点两两互联互拉
func TestThreeNodesInterop(t *testing.T) {
	storageA := t.TempDir()
	content := []byte("three-nodes-interop-" + hex.EncodeToString(make([]byte, 0)) + "-payload")
	hash := writeTestFile(t, storageA, content)

	idA, idB, idC := randID("it-3a"), randID("it-3b"), randID("it-3c")
	svcA := newService(t, idA, storageA, false, []string{idB, idC})
	svcB := newService(t, idB, t.TempDir(), false, []string{idA, idC})
	svcC := newService(t, idC, t.TempDir(), false, []string{idA, idB})

	// 两两互联：B→A、C→A、C→B
	waitConnections(t, svcA, map[string]bool{svcB.ID(): true, svcC.ID(): true}, 90*time.Second)
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true, svcC.ID(): true}, 90*time.Second)
	waitConnections(t, svcC, map[string]bool{svcA.ID(): true, svcB.ID(): true}, 90*time.Second)

	for name, svc := range map[string]*service.PeerJSService{"B": svcB, "C": svcC} {
		data, err := svc.FetchFromPeer(svcA.ID(), hash, 0, -1)
		if err != nil {
			t.Fatalf("%s 从 A 拉取失败: %v", name, err)
		}
		if !bytes.Equal(data, content) {
			t.Fatalf("%s 拉取内容不一致", name)
		}
	}

	// C 从 B 拉（B 已从 A 拉到过但未落盘 → 仍应失败；此处验证 C↔B 连接存在即可，
	// 落盘转发属于上层同步逻辑，不在本协议范围）
	_ = svcC.Connections()
	if _, err := svcC.FetchFromPeer(svcB.ID(), hash, 0, -1); err == nil {
		t.Log("注意: C 从 B 拉取成功（B 可能已缓存该文件）")
	}
}

// TestFourNodesStar 4 节点星型：A 提供文件，B/C/D 全部同时从 A 拉取（一对多并发）。
//
// 发现背景：功能测试——一对多并发（同一节点同时服务多个对端）
func TestFourNodesStar(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 128*1024)
	for i := range content {
		content[i] = byte(i)
	}
	hash := writeTestFile(t, storageA, content)

	idA := randID("it-4a")
	svcA := newService(t, idA, storageA, false, nil)
	var others []*service.PeerJSService
	for i := 0; i < 3; i++ {
		svc := newService(t, randID("it-4x"), t.TempDir(), false, []string{idA})
		others = append(others, svc)
	}

	want := map[string]bool{svcA.ID(): true}
	for _, s := range others {
		waitConnections(t, s, want, 90*time.Second)
	}

	// 并发从 A 拉取
	done := make(chan error, len(others))
	for _, s := range others {
		go func(s *service.PeerJSService) {
			data, err := s.FetchFromPeer(svcA.ID(), hash, 0, -1)
			if err != nil {
				done <- err
				return
			}
			if !bytes.Equal(data, content) {
				done <- bytesErr(len(data))
				return
			}
			done <- nil
		}(s)
	}
	for range others {
		if err := <-done; err != nil {
			t.Fatalf("并发拉取失败: %v", err)
		}
	}
}

// bytesErr 简单错误构造。
func bytesErr(n int) error {
	return fmt.Errorf("size mismatch: %d", n)
}


// TestConcurrentLargeFetches 并发大文件拉取：同一节点上 4 个并发请求 × 2MB。
// 发现背景：旧流控实现每个 serveFile 各自注册 OnBufferedAmountLow（pion 替换式
// 回调），并发请求时只有一个能收到低水位事件，其余在 bufferedAmount 超阈值时
// 死等（本测试在修复前会卡到超时）。修复：流控下沉 SendFrame（连接级全局回调）。
func TestConcurrentLargeFetches(t *testing.T) {
	storageA := t.TempDir()
	content := make([]byte, 2*1024*1024)
	for i := range content {
		content[i] = byte(i * 13)
	}
	hash := writeTestFile(t, storageA, content)

	idA := randID("it-ca")
	newService(t, idA, storageA, false, nil) // svcA：文件源，无需直接引用
	var clients []*service.PeerJSService
	for i := 0; i < 4; i++ {
		svc := newService(t, randID("it-cx"), t.TempDir(), false, []string{idA})
		clients = append(clients, svc)
	}
	for _, s := range clients {
		waitConnections(t, s, map[string]bool{idA: true}, 60*time.Second)
	}

	done := make(chan error, len(clients))
	for _, s := range clients {
		go func(s *service.PeerJSService) {
			data, err := s.FetchFromPeer(idA, hash, 0, -1)
			if err != nil {
				done <- err
				return
			}
			if !bytes.Equal(data, content) {
				done <- fmt.Errorf("内容不一致: got %d bytes", len(data))
				return
			}
			done <- nil
		}(s)
	}
	for range clients {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("并发大文件拉取失败: %v", err)
			}
		case <-time.After(120 * time.Second):
			t.Fatal("并发大文件拉取超时（流控死锁？）")
		}
	}
}
