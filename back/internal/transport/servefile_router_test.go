package transport

// servefile_router_test.go：serveFile 多源路由 + 回源防环测试（第 3 项
// 优化，2026-08-18）。
//
// 背景：serveFile 由本地语义（fileIndex + CAS）升级为多源路由
// （source.Manager：本地 → 对端 → URL 模板）后，对端 req 可能回源到
// 其它节点——A←→B 互连时 B 请求 A 没有的文件会触发 A→B→A 无限递归。
// 防环：req 帧带 trace（经过的节点链），转发路径上的节点发现自己在链中
// 即拒绝（dcReq.Trace；serveFile 经 context 传播，OpenStreamFrom 携带）。
//
// 注意：本文件不 import source 包（transport ↔ source 有依赖，内部测试
// 引入 source 会构成 import 环）——router 用本地 fakeRouter 实现
// FileRouter 接口，行为对齐 source.Manager/LocalSource 语义。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeRouter 内存版 FileRouter：行为对齐 LocalSource（clamp + 限长）。
type fakeRouter struct {
	content []byte
	hash    string
}

func (f *fakeRouter) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if hash != f.hash {
		return nil, fmt.Errorf("not found")
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > int64(len(f.content)) {
		off = int64(len(f.content))
	}
	length := size
	if length < 0 || off+length > int64(len(f.content)) {
		length = int64(len(f.content)) - off
	}
	return io.NopCloser(bytes.NewReader(f.content[off : off+length])), nil
}

func (f *fakeRouter) InfoSize(ctx context.Context, hash string) (int64, error) {
	if hash != f.hash {
		return 0, fmt.Errorf("not found")
	}
	return int64(len(f.content)), nil
}

// TestServeFile_LoopDetected trace 含本节点 ID → 拒绝，不回源（防死循环）。
//
// 发现背景：代码审阅 2026-08-18——serveFile 接多源路由后必须防回源环；
// trace 是帧协议新增的可选字段（omitempty 兼容旧对端）。
func TestServeFile_LoopDetected(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.id = "node-a"
	sess := &fakeSession{id: "node-b"}
	svc.serveFile(sess, dcReq{
		Type: "req", Hash: hashOf("loop"), ReqID: "r1",
		Trace: []string{"node-c", "node-a"}, // 本节点已在链路中
	})
	types := sess.sentTypes()
	require.Len(t, types, 1, "应恰好回一个 err 帧")
	assert.Equal(t, "err", types[0])
	assert.Equal(t, "loop detected", sess.sent[0]["msg"])
}

// TestServeFile_RouterMultiSource 装配 router 后：命中走多源路由
// （meta.total 来自 InfoSize + data 流式发送，含分片请求），未命中
// err not found。
//
// 发现背景：代码审阅 2026-08-18——serveFile 由 openFile 硬编码升级为
// FileRouter 接口（source.Manager 实现），本测试锁定「路由命中 + total
// 语义 + 分片 + 未命中错误」行为。
func TestServeFile_RouterMultiSource(t *testing.T) {
	svc := newTestPeerJSService(t)
	content := []byte("hello multi-source router")
	hash := hashOf(string(content))
	svc.SetFileRouter(&fakeRouter{content: content, hash: hash})

	// 全量请求（size=-1）：meta(total) → data(完整内容) → done
	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes(), "命中必须 meta+data+done")
	assert.Equal(t, float64(len(content)), frames[0].header["total"], "meta.total 来自 InfoSize")
	assert.Equal(t, content, frames[1].body, "data 块必须与原文一致")
	assert.Equal(t, float64(len(content)), frames[2].header["size"], "done.size 是实际发送量")

	// 分片请求：offset=2 size=4 → data 只回 [2:6]
	sess2 := &fakeSession{id: "remote2"}
	svc.serveFile(sess2, dcReq{Type: "req", Hash: hash, Offset: 2, Size: 4, ReqID: "r2"})
	frames2 := sess2.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess2.sentFrameTypes())
	assert.Equal(t, content[2:6], frames2[1].body, "分片请求必须回对应区间")
	assert.Equal(t, float64(len(content)), frames2[0].header["total"], "meta.total 仍是文件全量")

	// 未命中：err not found
	sess3 := &fakeSession{id: "remote3"}
	svc.serveFile(sess3, dcReq{Type: "req", Hash: hashOf("nope"), ReqID: "r3"})
	assert.Equal(t, "err", sess3.sentTypes()[0])
	assert.Equal(t, "not found", sess3.sent[0]["msg"])
}

// TestServeFile_NoRouterFallback 未装配 router → 本地语义（fileIndex + CAS）。
// 保持旧行为不回归（测试/独立模式）。
func TestServeFile_NoRouterFallback(t *testing.T) {
	svc := newTestPeerJSService(t)
	content := []byte("fallback-local")
	hash := hashOf(string(content))
	// 写内容寻址存储
	dir := filepath.Join(svc.storageDir, hash[:2])
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, hash), content, 0o644))

	sess := &fakeSession{id: "remote"}
	svc.serveFile(sess, dcReq{Type: "req", Hash: hash, Size: -1, ReqID: "r1"})
	frames := sess.sentFrames()
	require.Equal(t, []string{"meta", "data", "done"}, sess.sentFrameTypes())
	assert.Equal(t, float64(len(content)), frames[0].header["total"])
	assert.Equal(t, content, frames[1].body)
}

// TestOpenStreamFrom_TracePropagation OpenStreamFrom 把 trace 透传到对端
// req 帧（防环链路的传播点）；根请求（trace=nil）不带该字段。
//
// 发现背景：代码审阅 2026-08-18——serveFile 回源时若丢 trace，下游节点
// 无法判断环；ctx 传递让「根请求（无 trace）→ 转发（带 trace）→ 拒绝环」
// 链路完整。PeerSource 的 ctx→trace 读取在 source/peer.go（几行代码，
// 由本测试覆盖帧侧传播 + conn.go TraceKey 注释约束）。
func TestOpenStreamFrom_TracePropagation(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.id = "node-a"
	sess := bindFakeConn(t, svc, "peerB")

	// 带 trace：发出的 req 帧必须携带（转发链路 node-b → node-a → 下游）
	r, err := svc.OpenStreamFrom("peerB", hashOf("x"), 0, -1, []string{"node-b"})
	require.NoError(t, err)
	var reqHeader map[string]any
	for _, fr := range sess.sentFrames() {
		if fr.header["type"] == "req" {
			reqHeader = fr.header
		}
	}
	require.NotNil(t, reqHeader, "OpenStreamFrom 应发出 req 帧")
	trace, ok := reqHeader["trace"].([]any)
	require.True(t, ok, "带 trace 的请求必须序列化 trace 字段")
	assert.Equal(t, []any{"node-b"}, trace, "trace 原样透传给下游")
	r.Close()

	// 根请求（trace=nil）：omitempty 不序列化 trace 字段
	sess2 := bindFakeConn(t, svc, "peerC")
	r2, err := svc.OpenStreamFrom("peerC", hashOf("y"), 0, -1, nil)
	require.NoError(t, err)
	var rootHeader map[string]any
	for _, fr := range sess2.sentFrames() {
		if fr.header["type"] == "req" {
			rootHeader = fr.header
		}
	}
	require.NotNil(t, rootHeader)
	_, hasTrace := rootHeader["trace"]
	assert.False(t, hasTrace, "根请求不得携带 trace")
	r2.Close()
}

// 测试用 os 工具（避免本文件顶部 import os/path 噪音）。
func osMkdirAll(t *testing.T, dir string) error {
	t.Helper()
	return mkdirAllForTest(dir)
}

func osWriteFile(t *testing.T, path string, content []byte) error {
	t.Helper()
	return writeFileForTest(path, content)
}

// mkdirAllForTest/writeFileForTest 薄封装（TestServeFile_NoRouterFallback 用）。
func mkdirAllForTest(dir string) error {
	return os.MkdirAll(dir, 0o755)
}

func writeFileForTest(path string, content []byte) error {
	return os.WriteFile(path, content, 0o644)
}

var _ = json.Marshal // 保留 json import（fakeSession.feed 用 peerjs.Frame 构造）