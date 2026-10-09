//go:build integration

package integration

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

// TestXOR_TwoNodesFetch_ShaMatch 真实双 peer（自托管信令 + 本机 WebRTC 直连）
// 在数据面 XOR 混淆开启（两端同 secret）下取文件：内容与 sha256 必须与明文
// 基线一致——加密发生在传输层（back/peerjs/xor.go），业务层哈希在解密后计算，
// 取文件链路对加密不可见。
//
// 起法与 TestTwoNodesInterop 相同（同机 host candidate 直连，无需 STUN；
// PEERDRIVE_SKIP_RTC=1 时跳过）；差别只在两端配置了 XOR 开关+同一把密钥。
// XOR 配置经 env 注入（config.Load 读取 PEERDRIVE_PEERJS_XOR_*），两个 service
// 在 env 生效期内构造 → 两端同 key。
//
// Discovery background: 加密正确性的验收口径——XOR 不能改变上层协议语义：
// 全量取回（sha 校验）+ 分片取回（offset/size 语义）都必须原样通过。
func TestXOR_TwoNodesFetch_ShaMatch(t *testing.T) {
	t.Setenv("PEERDRIVE_PEERJS_XOR_ENABLE", "true")
	t.Setenv("PEERDRIVE_PEERJS_XOR_KEY", "xor-test-secret")

	storageA := t.TempDir()
	content := make([]byte, 700*1024) // 跨多个 64KB 块
	for i := range content {
		content[i] = byte(i * 7)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("xor-a"), randID("xor-b")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})

	waitConnections(t, svcA, map[string]bool{svcB.ID(): true}, 60*time.Second)
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	// 全量取回：内容 + sha256 一致
	data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
	if err != nil {
		t.Fatalf("FetchFromPeer (xor on): %v", err)
	}
	if !bytes.Equal(data, content) {
		t.Fatalf("content mismatch: got %d bytes, want %d", len(data), len(content))
	}
	if got := sha256Hex(data); got != hash {
		t.Fatalf("sha256 mismatch: got %s want %s", got, hash)
	}

	// 分片取回：offset/size 语义不受加密影响
	const offset = 100 * 1024
	const size = 50 * 1024
	seg, err := svcB.FetchFromPeer(svcA.ID(), hash, offset, size)
	if err != nil {
		t.Fatalf("FetchFromPeer(range, xor on): %v", err)
	}
	if !bytes.Equal(seg, content[offset:offset+size]) {
		t.Fatalf("range content mismatch: got %d bytes", len(seg))
	}
}

// TestXOR_ConcurrentFetch_Race 并发多路取回（-race 覆盖）：XOR 加密路径
// （发送侧 xorApply 新分配 + 收侧解密）在并发 serveFile/fetchReader 下无
// 数据竞争、无串帧。
//
// Discovery background: XOR 引入后新增的分配/解密代码在并发路径上，
// 必须与既有 SendFrame 的 sendMu 原子性叠加验证——加密不能破坏"头+体
// 连续"的帧协议约束。
func TestXOR_ConcurrentFetch_Race(t *testing.T) {
	t.Setenv("PEERDRIVE_PEERJS_XOR_ENABLE", "true")
	t.Setenv("PEERDRIVE_PEERJS_XOR_KEY", "xor-test-secret")

	storageA := t.TempDir()
	content := make([]byte, 256*1024)
	for i := range content {
		content[i] = byte(i % 251)
	}
	hash := writeTestFile(t, storageA, content)

	idA, idB := randID("xor-ra"), randID("xor-rb")
	svcA := newService(t, idA, storageA, false, nil)
	svcB := newService(t, idB, t.TempDir(), false, []string{idA})
	waitConnections(t, svcB, map[string]bool{svcA.ID(): true}, 60*time.Second)

	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			data, err := svcB.FetchFromPeer(svcA.ID(), hash, 0, -1)
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(data, content) {
				errs <- errors.New("content mismatch under xor")
				return
			}
			errs <- nil
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent fetch under xor: %v", err)
		}
	}
}
