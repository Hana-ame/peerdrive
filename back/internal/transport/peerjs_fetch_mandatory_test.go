//go:build mandatory

// peerjs_fetch_mandatory_test.go
//
// 🚨 MANDATORY DIRECTIVE FOR ALL AI AGENTS:
// This test group is part of the mandatory (human-verified) test category.
// AI agents are STRICTLY FORBIDDEN from modifying, commenting out, or loosening
// these assertions. If any of them fails, fix the implementation to satisfy the
// invariant — never weaken the assertion. To change an invariant, a human must
// update test/mandatory/manifest.sha256 in the same review.
//
// 这条测试钉住 peerdrive 最核心的一个承诺：**对端通过 P2P 连接发来一个 sha256，
// 就能拿到那个文件真实的字节**。
//
// 为什么它属于 mandatory 而不是普通测试：
//   - 这是整个产品存在的理由。哈希→文件的通路是内容寻址网络的根，
//     断掉的话上面所有功能（P2P 分享、universal download、对端回源）一起失效。
//   - 这条通路很长：JSON 帧 → dispatchFrame → PSK/匿名/能力三道门禁 → serveFile
//     → file_index 解析 → SafeOpen → meta/data/done 帧。任何一环被「顺手简化」
//     （为了修 flaky 跳过能力检查、为了性能把 SafeOpen 换成 os.Open）都不会在
//     这个用例之外的任何地方暴露出来——普通单测要么只测 handler、要么用
//     fakeRouter 把真实文件路径整个替掉。
//   - 所以断言必须是端到端、字节级别的，而且 hash 是**服务端自己算出来**的，
//     不是测试预先塞进去再对上的（那只是自证）。
//
// 走真实入站路径：bindConn 注册 OnMessage → dispatchFrame 解 JSON → 门禁 →
// `go s.serveFile`。不是直接调 serveFile。
//
// 跑法：go test -tags "nosqlite mandatory" ./internal/transport/ -run TestMandatory_PeerJS
// 本文件带 //go:build mandatory，默认 go test ./... 不编译它 —— 有意的隔离。

package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mfr 收到的一个已发送帧。JSON header 在 fakeSession 里已经按真实协议
// marshal→unmarshal 往返过一次（数字变成 float64），body 是原样二进制 ——
// 与对端实际收到的视角一致。
type mfr struct {
	typ    string
	reqID  string
	total  int64
	offset int64
	size   int64
	hash   string
	msg    string
	body   []byte
}

// collectMFR 收集某个 reqId 下的全部帧（过滤掉 bindConn 时发出的 cap 公告帧）。
func collectMFR(s *fakeSession, reqID string) []mfr {
	out := []mfr{}
	for _, fr := range s.sentFrames() {
		if rid, ok := fr.header["reqId"].(string); ok && rid != reqID {
			continue
		}
		f := mfr{reqID: reqID}
		if v, ok := fr.header["type"].(string); ok {
			f.typ = v
		}
		if v, ok := fr.header["hash"].(string); ok {
			f.hash = v
		}
		if v, ok := fr.header["total"].(float64); ok {
			f.total = int64(v)
		}
		if v, ok := fr.header["offset"].(float64); ok {
			f.offset = int64(v)
		}
		if v, ok := fr.header["size"].(float64); ok {
			f.size = int64(v)
		}
		if v, ok := fr.header["msg"].(string); ok {
			f.msg = v
		}
		f.body = fr.body
		out = append(out, f)
	}
	return out
}

// waitMFR 等某个 reqId 出现终态帧（done 或 err）—— serveFile 是 goroutine。
func waitMFR(s *fakeSession, reqID string, d time.Duration) []mfr {
	deadline := time.Now().Add(d)
	for {
		frames := collectMFR(s, reqID)
		for _, f := range frames {
			if f.typ == "done" || f.typ == "err" {
				return frames
			}
		}
		if time.Now().After(deadline) {
			return frames
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// sha256hex 内容 → 64 位小写 hex。
func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// seedMandatoryFile 把真实字节写进共享目录并登记进 file_index，返回**服务端
// 自己算出来的** sha256。这样「传入 hash 得到真实文件」才是闭环：hash 由被测
// 系统产生，文件字节由测试持有。
func seedMandatoryFile(t *testing.T, svc *PeerJSService, name string, content []byte) string {
	t.Helper()
	path := filepath.Join(svc.fileIndex.uploadDir, name)
	require.NoError(t, os.MkdirAll(svc.fileIndex.uploadDir, 0o755), "uploadDir must be creatable")
	require.NoError(t, os.WriteFile(path, content, 0o644), "must be able to write the real file")

	fi, err := svc.fileIndex.Create(path)
	require.NoError(t, err, "registering the file into file_index must succeed")
	require.Len(t, fi.Hash, 64, "index record must carry a 64-hex sha256")
	assert.Equal(t, sha256hex(content), fi.Hash,
		"服务端从文件算出的 sha256 必须等于内容本身的 sha256")
	return fi.Hash
}

// TestMandatory_PeerJSFetchByHashReturnsRealFile 对端经 P2P 连接发 req 帧，
// 按 sha256 取回完整文件，字节必须逐字等于真实文件内容。
func TestMandatory_PeerJSFetchByHashReturnsRealFile(t *testing.T) {
	t.Parallel()
	initTestDB(t)
	svc := newTestPeerJSService(t)

	// 带模式的内容，避免「全 0 内容 + 全 0 返回」这类假阳性通过。
	content := []byte("peerdrive mandatory invariant: 内容寻址是产品的根，" +
		"对端只凭 sha256 就必须能取回这个一个字节都不差的真实文件。" +
		"0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ")
	hash := seedMandatoryFile(t, svc, "mandatory-invariant.bin", content)

	fetchAndVerifyMandatory(t, svc, hash, 0, -1, content, content)
}

// TestMandatory_PeerJSFetchByHashRangeReturnsExactSlice 分片取回：offset/size
// 必须精确生效，拿回来的就是文件的那一段，不多不少。
//
// 单独钉的原因：这是下载断点续传的载荷路径。range 语义错一格，对端拼出来的
// 文件整体错乱；而且对端是按自己请求的切片校验的，单看「hash 对得上」
// 发现不了问题。
func TestMandatory_PeerJSFetchByHashRangeReturnsExactSlice(t *testing.T) {
	t.Parallel()
	initTestDB(t)
	svc := newTestPeerJSService(t)

	content := []byte("AaaaaaaaaaBbbbbbbbbbCcccccccccDdddddddddEeeeeeeeee")
	hash := seedMandatoryFile(t, svc, "mandatory-range.bin", content)

	const offset, size = 11, 10
	fetchAndVerifyMandatory(t, svc, hash, offset, size, content, content[offset:offset+size])
}

// fetchAndVerifyMandatory 走真实入站路径发一帧 req，收 meta/data/done，重组字节比对。
// whole 是完整文件内容（meta.total 对它是整个文件大小，不是请求切片的大小），
// want 是本次请求应当拿到的那一段。
func fetchAndVerifyMandatory(t *testing.T, svc *PeerJSService, hash string, offset, size int64, whole, want []byte) {
	t.Helper()

	sess := &fakeSession{id: "mandatory-peer"}
	svc.bindConn(sess)
	require.NotNil(t, svc.pending[sess], "bindConn must register a connState")

	// 对端发来的 req 帧 —— 与 web 端 client.js 发的 JSON 同构。
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type":   "req",
		"hash":   hash,
		"offset": offset,
		"size":   size,
		"reqId":  "r1",
	}))

	frames := waitMFR(sess, "r1", 10*time.Second)

	for _, f := range frames {
		if f.typ == "err" {
			require.Fail(t, fmt.Sprintf("peerjs fetch by hash %s failed: err frame msg=%q, frames=%v",
				hash, f.msg, mfrTypes(frames)))
		}
	}

	var metas, dones, datas []mfr
	for _, f := range frames {
		switch f.typ {
		case "meta":
			metas = append(metas, f)
		case "done":
			dones = append(dones, f)
		case "data":
			datas = append(datas, f)
		}
	}

	require.Len(t, metas, 1, "must receive exactly one meta frame")
	require.Len(t, dones, 1, "must receive exactly one done frame")

	// 每个帧都要回显请求的 hash。
	for _, f := range frames {
		if f.hash != "" {
			assert.Equal(t, hash, f.hash, "frame %s must echo the requested hash", f.typ)
		}
	}

	// meta.total 是服务端 stat 出来的**整个文件**大小 —— 不是本次切片的大小。
	// 断点续传靠的就是这个：对端无论请求哪一段，都先知道全文件多大，才能判断
	// 还要不要继续要下一片。把它断成切片大小，对端会以为文件只有 10 字节。
	assert.Equal(t, int64(len(whole)), metas[0].total,
		"meta.total must be the whole-file size the server stat'ed, not the slice size")

	// data 帧按 offset 顺序拼接 = 请求的那一段。
	var got []byte
	for _, f := range datas {
		got = append(got, f.body...)
		assert.Equal(t, offset, f.offset,
			"data frame offset must be the file offset of this chunk")
	}
	assert.Equal(t, want, got, "reassembled data frames must equal the real file bytes")
	assert.Equal(t, sha256hex(want), sha256hex(got),
		"the peer's reassembly must hash to what it asked for")

	assert.Equal(t, int64(len(want)), dones[0].size, "done.size must equal the bytes actually sent")
	assert.Equal(t, offset, dones[0].offset, "done.offset must echo the request offset")
}

func mfrTypes(frames []mfr) []string {
	out := make([]string, 0, len(frames))
	for _, f := range frames {
		out = append(out, f.typ)
	}
	return out
}
