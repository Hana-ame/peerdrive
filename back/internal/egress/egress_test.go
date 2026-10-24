package egress

// egress_test.go: 统一出口管线与适配器全量单测。
// 覆盖三大消费端出口形态（HTTP/WS/PeerJS）、Range 裁剪、防环阻断与背压调度。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// fakeProvider 内存数据源模拟器。
type fakeProvider struct {
	files map[string][]byte
}

func newFakeProvider() *fakeProvider {
	return &fakeProvider{files: make(map[string][]byte)}
}

func (p *fakeProvider) add(data []byte) string {
	h := sha256.Sum256(data)
	hash := hex.EncodeToString(h[:])
	p.files[hash] = data
	return hash
}

func (p *fakeProvider) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	data, ok := p.files[hash]
	if !ok {
		return nil, errors.New("file not found")
	}
	total := int64(len(data))
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := size
	if limit < 0 || offset+limit > total {
		limit = total - offset
	}
	r := bytes.NewReader(data[offset : offset+limit])
	return io.NopCloser(r), nil
}

func (p *fakeProvider) InfoSize(ctx context.Context, hash string) (int64, error) {
	data, ok := p.files[hash]
	if !ok {
		return -1, errors.New("file not found")
	}
	return int64(len(data)), nil
}

// fakeFrameSender 模拟 WebSocket / PeerJS 帧接收器。
type fakeFrameSender struct {
	mu     sync.Mutex
	jsonMsgs []any
	frames   []struct {
		Header any
		Body   []byte
	}
}

func (f *fakeFrameSender) SendJSON(v any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jsonMsgs = append(f.jsonMsgs, v)
	return nil
}

func (f *fakeFrameSender) SendFrame(header any, body []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]byte, len(body))
	copy(cp, body)
	f.frames = append(f.frames, struct {
		Header any
		Body   []byte
	}{Header: header, Body: cp})
	return nil
}

// TestPipeline_HTTP_FullFile 发现背景：验证 HTTP 全量文件下发符合 RFC 标准规范（200 OK、ETag、Content-Length、块池复用）。
func TestPipeline_HTTP_FullFile(t *testing.T) {
	fp := newFakeProvider()
	// 创建大于单块（64KB）的数据以验证循环块传输
	content := bytes.Repeat([]byte("A"), DefaultChunkSize+1024)
	hash := fp.add(content)

	pipeline := NewPipeline(fp, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{
		Hash:     hash,
		Offset:   0,
		Size:     -1,
		Filename: "test.txt",
	}

	err := pipeline.Serve(req, sink)
	if err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", res.StatusCode)
	}
	if gotLen := rec.Body.Len(); gotLen != len(content) {
		t.Fatalf("body length mismatch: got %d, expected %d", gotLen, len(content))
	}
	if rec.Header().Get("Accept-Ranges") != "bytes" {
		t.Errorf("missing Accept-Ranges: bytes")
	}
	if rec.Header().Get("ETag") != fmt.Sprintf("\"%s\"", hash) {
		t.Errorf("ETag mismatch: got %s", rec.Header().Get("ETag"))
	}
}

// TestPipeline_HTTP_Range 发现背景：验证媒体播放器流式 Range 请求（206 Partial Content 与 Content-Range 响应头边界）。
func TestPipeline_HTTP_Range(t *testing.T) {
	fp := newFakeProvider()
	content := []byte("0123456789ABCDEF0123456789ABCDEF")
	hash := fp.add(content)

	pipeline := NewPipeline(fp, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{
		Hash:   hash,
		Offset: 10,
		Size:   16,
	}

	err := pipeline.Serve(req, sink)
	if err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	res := rec.Result()
	t.Cleanup(func() { _ = res.Body.Close() })

	if res.StatusCode != http.StatusPartialContent {
		t.Fatalf("expected 206 Partial Content, got %d", res.StatusCode)
	}
	expectedRange := fmt.Sprintf("bytes 10-25/%d", len(content))
	if gotRange := rec.Header().Get("Content-Range"); gotRange != expectedRange {
		t.Errorf("Content-Range mismatch: got %s, want %s", gotRange, expectedRange)
	}
	if gotBody := rec.Body.Bytes(); !bytes.Equal(gotBody, content[10:26]) {
		t.Fatalf("range body mismatch: got %q, want %q", string(gotBody), string(content[10:26]))
	}
}

// TestPipeline_HTTP_EmptyFile 发现背景：验证 0 字节空文件请求能正常返回 200 OK，防止边界下溢或误报 416。
func TestPipeline_HTTP_EmptyFile(t *testing.T) {
	fp := newFakeProvider()
	hash := fp.add([]byte(""))

	pipeline := NewPipeline(fp, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{Hash: hash, Offset: 0, Size: -1}
	if err := pipeline.Serve(req, sink); err != nil {
		t.Fatalf("Serve empty file failed: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK for empty file, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("expected 0 bytes body, got %d", rec.Body.Len())
	}
}

// TestPipeline_PeerJS_Frames 发现背景：验证 WebRTC DataChannel 的帧协议流程（meta -> 连续 data 块 -> done）。
func TestPipeline_PeerJS_Frames(t *testing.T) {
	fp := newFakeProvider()
	// 两块数据触发多次 data 帧
	content := bytes.Repeat([]byte("B"), DefaultChunkSize+500)
	hash := fp.add(content)

	fs := &fakeFrameSender{}
	sink := NewPeerJSEgressSink(fs)
	pipeline := NewPipeline(fp, "node-1")

	req := EgressRequest{
		Hash:   hash,
		Offset: 0,
		Size:   -1,
		ReqID:  "req-42",
	}

	if err := pipeline.Serve(req, sink); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	// 验证 JSON 帧：meta 帧与 done 帧
	if len(fs.jsonMsgs) != 2 {
		t.Fatalf("expected 2 json frames (meta, done), got %d", len(fs.jsonMsgs))
	}
	metaFrame, ok := fs.jsonMsgs[0].(map[string]any)
	if !ok || metaFrame["type"] != "meta" || metaFrame["reqId"] != "req-42" {
		t.Errorf("invalid meta frame: %+v", fs.jsonMsgs[0])
	}
	doneFrame, ok := fs.jsonMsgs[1].(map[string]any)
	if !ok || doneFrame["type"] != "done" || doneFrame["size"] != int64(len(content)) {
		t.Errorf("invalid done frame: %+v", fs.jsonMsgs[1])
	}

	// 验证二进制数据帧：应有两块
	if len(fs.frames) != 2 {
		t.Fatalf("expected 2 data frames, got %d", len(fs.frames))
	}
	totalBytes := 0
	for _, frame := range fs.frames {
		totalBytes += len(frame.Body)
	}
	if totalBytes != len(content) {
		t.Errorf("total transferred bytes mismatch: got %d, want %d", totalBytes, len(content))
	}
}

// TestPipeline_WebSocket_AdminBin 发现背景：验证 WebSocket local 管理面的 admin-bin 模式（先下发声明头再推二进制数据）。
func TestPipeline_WebSocket_AdminBin(t *testing.T) {
	fp := newFakeProvider()
	content := []byte("hello admin bin payload")
	hash := fp.add(content)

	fs := &fakeFrameSender{}
	sink := NewWebSocketEgressSinkFromFrameSender(fs)
	pipeline := NewPipeline(fp, "node-1")

	req := EgressRequest{
		Hash:     hash,
		Offset:   0,
		Size:     -1,
		ReqID:    "admin-101",
		MimeType: "text/plain",
	}

	if err := pipeline.Serve(req, sink); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	// 应有 1 个 admin-bin 声明头
	if len(fs.jsonMsgs) != 1 {
		t.Fatalf("expected 1 json declaration, got %d", len(fs.jsonMsgs))
	}
	hdr := fs.jsonMsgs[0].(map[string]any)
	if hdr["type"] != "admin-bin" || hdr["mime"] != "text/plain" || hdr["reqId"] != "admin-101" {
		t.Errorf("unexpected admin-bin header: %+v", hdr)
	}

	// 应有 1 个二进制数据块
	if len(fs.frames) != 1 || !bytes.Equal(fs.frames[0].Body, content) {
		t.Fatalf("binary frame mismatch")
	}
}

// TestPipeline_InvalidHash 发现背景：防止非 64 位 SHA-256 哈希输入导致内部路径拼接或下层组件 panic。
func TestPipeline_InvalidHash(t *testing.T) {
	fp := newFakeProvider()
	pipeline := NewPipeline(fp, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{Hash: "not-a-valid-sha256"}
	err := pipeline.Serve(req, sink)
	if !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("expected ErrInvalidHash, got %v", err)
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", rec.Code)
	}
}

// TestPipeline_TraceLoop 发现背景：验证分布式多跳回源时的防环阻断，避免 A <-> B 死循环消耗连接与内存。
func TestPipeline_TraceLoop(t *testing.T) {
	fp := newFakeProvider()
	content := []byte("some content")
	hash := fp.add(content)

	pipeline := NewPipeline(fp, "my-node-id")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{
		Hash:  hash,
		Trace: []string{"peer-a", "my-node-id", "peer-b"}, // 包含当前节点 ID
	}

	err := pipeline.Serve(req, sink)
	if !errors.Is(err, ErrLoopDetected) {
		t.Fatalf("expected ErrLoopDetected, got %v", err)
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", rec.Code)
	}
}

// TestPipeline_RangeNotSatisfiable 发现背景：验证请求偏移大于等于总大小时返回 416，防止无效数据流占用。
func TestPipeline_RangeNotSatisfiable(t *testing.T) {
	fp := newFakeProvider()
	content := []byte("short")
	hash := fp.add(content)

	pipeline := NewPipeline(fp, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	req := EgressRequest{
		Hash:   hash,
		Offset: 100, // 超过 content 长度 5
		Size:   10,
	}

	err := pipeline.Serve(req, sink)
	if !errors.Is(err, ErrRangeNotSatisfiable) {
		t.Fatalf("expected ErrRangeNotSatisfiable, got %v", err)
	}
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("expected 416, got %d", rec.Code)
	}
}

// TestPipeline_ContextCancel 发现背景：验证客户端提前关闭连接时，管线立即退出并释放资源，不发生阻塞泄露。
func TestPipeline_ContextCancel(t *testing.T) {
	fp := newFakeProvider()
	content := bytes.Repeat([]byte("C"), DefaultChunkSize*5)
	hash := fp.add(content)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 预先取消

	fs := &fakeFrameSender{}
	sink := NewPeerJSEgressSink(fs)
	pipeline := NewPipeline(fp, "node-1")

	req := EgressRequest{
		Hash:    hash,
		Context: ctx,
	}

	err := pipeline.Serve(req, sink)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestPipeline_Backpressure 发现背景：验证底层网络慢写时 WaitForBackpressure 正常阻塞与唤醒调度。
func TestPipeline_Backpressure(t *testing.T) {
	fp := newFakeProvider()
	content := bytes.Repeat([]byte("D"), DefaultChunkSize*2)
	hash := fp.add(content)

	fs := &fakeFrameSender{}
	sink := NewPeerJSEgressSink(fs)

	var waitCount int
	sink.SetBackpressureHook(func(ctx context.Context) error {
		waitCount++
		return nil
	})

	pipeline := NewPipeline(fp, "node-1")
	req := EgressRequest{Hash: hash}

	if err := pipeline.Serve(req, sink); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	if waitCount == 0 {
		t.Errorf("expected backpressure hook to be called at least once")
	}
}

// TestGuessMimeType 发现背景：验证显式指定、按文件名后缀推导、magic 嗅探的三级兜底策略。
func TestGuessMimeType(t *testing.T) {
	// 显式指定优先
	if m := GuessMimeType("video/mp4", "test.jpg", nil); m != "video/mp4" {
		t.Errorf("expected explicit mime, got %s", m)
	}
	// 按扩展名推导
	if m := GuessMimeType("", "sample.png", nil); m != "image/png" {
		t.Errorf("expected image/png for .png, got %s", m)
	}
	// 默认兜底
	if m := GuessMimeType("", "", nil); m != "application/octet-stream" {
		t.Errorf("expected fallback octet-stream, got %s", m)
	}
}

// TestChunkPool_GetAndPut 发现背景：验证 64KB 块池借还行为和容量保全。
func TestChunkPool_GetAndPut(t *testing.T) {
	buf := GetChunkBuffer()
	if len(buf) != DefaultChunkSize {
		t.Fatalf("buffer size mismatch: %d", len(buf))
	}
	buf[0] = 0x55
	PutChunkBuffer(buf)
	// 再次获取应能成功获取
	buf2 := GetChunkBuffer()
	if len(buf2) != DefaultChunkSize {
		t.Fatalf("re-acquired buffer size mismatch: %d", len(buf2))
	}
	PutChunkBuffer(buf2)
}

// errorProvider 模拟读取错误的数据源。
type errorProvider struct {
	openErr error
	readErr error
}

func (e *errorProvider) OpenRange(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if e.openErr != nil {
		return nil, e.openErr
	}
	return &errReader{err: e.readErr}, nil
}

func (e *errorProvider) InfoSize(ctx context.Context, hash string) (int64, error) {
	return 1024, nil
}

type errReader struct {
	err error
}

func (r *errReader) Read(p []byte) (int, error) {
	return 0, r.err
}

func (r *errReader) Close() error {
	return nil
}

// TestPipeline_NotFound 发现背景：数据源未命中时返回 ErrNotFound 并写入 404 / NOT_FOUND 错误帧。
func TestPipeline_NotFound(t *testing.T) {
	ep := &errorProvider{openErr: errors.New("underlying not found")}
	pipeline := NewPipeline(ep, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	err := pipeline.Serve(EgressRequest{Hash: hash}, sink)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

// TestPipeline_ProviderReadError 发现背景：中间读取流发生 IO 错误时，管线正确向 Sink 投递 500 / READ_FAILED。
func TestPipeline_ProviderReadError(t *testing.T) {
	readErr := errors.New("disk read failed")
	ep := &errorProvider{readErr: readErr}
	pipeline := NewPipeline(ep, "node-1")

	fs := &fakeFrameSender{}
	sink := NewPeerJSEgressSink(fs)

	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	err := pipeline.Serve(EgressRequest{Hash: hash, ReqID: "req-err"}, sink)
	if !errors.Is(err, readErr) {
		t.Fatalf("expected readErr, got %v", err)
	}

	// 应有 meta 帧与 err 帧
	if len(fs.jsonMsgs) < 2 {
		t.Fatalf("expected at least 2 json messages, got %d", len(fs.jsonMsgs))
	}
	lastMsg := fs.jsonMsgs[len(fs.jsonMsgs)-1].(map[string]any)
	if lastMsg["type"] != "err" || lastMsg["code"] != "READ_FAILED" {
		t.Errorf("unexpected err frame: %+v", lastMsg)
	}
}

// TestPipeline_NilProvider 发现背景：未装配数据源时优雅返回 503 与 UNAVAILABLE。
func TestPipeline_NilProvider(t *testing.T) {
	pipeline := NewPipeline(nil, "node-1")
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	err := pipeline.Serve(EgressRequest{Hash: hash}, sink)
	if err == nil {
		t.Fatalf("expected error for nil provider")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rec.Code)
	}
}

// TestPipeline_BackpressureError 发现背景：背压等待中若通道被外部关闭或上下文取消，管线应及时终止。
func TestPipeline_BackpressureError(t *testing.T) {
	fp := newFakeProvider()
	content := bytes.Repeat([]byte("E"), DefaultChunkSize*2)
	hash := fp.add(content)

	fs := &fakeFrameSender{}
	sink := NewPeerJSEgressSink(fs)
	bpErr := errors.New("datachannel closed")
	sink.SetBackpressureHook(func(ctx context.Context) error {
		return bpErr
	})

	pipeline := NewPipeline(fp, "node-1")
	err := pipeline.Serve(EgressRequest{Hash: hash}, sink)
	if !errors.Is(err, bpErr) {
		t.Fatalf("expected bpErr, got %v", err)
	}
}

// TestHTTPEgressSink_HeadersAlreadySent 发现背景：重复写 Header 或在写 Header 之后写错误应当防御性拦截。
func TestHTTPEgressSink_HeadersAlreadySent(t *testing.T) {
	rec := httptest.NewRecorder()
	sink := NewHTTPEgressSink(rec)

	meta := EgressMeta{Hash: "abc", TotalSize: 100, Offset: 0, ContentSize: 100}
	if err := sink.WriteMeta(meta); err != nil {
		t.Fatalf("WriteMeta failed: %v", err)
	}
	// 重复调用 WriteMeta 报 ErrHeaderAlreadySent
	if err := sink.WriteMeta(meta); !errors.Is(err, ErrHeaderAlreadySent) {
		t.Errorf("expected ErrHeaderAlreadySent, got %v", err)
	}
	// Header 发送后调用 WriteError 报 ErrPrematureTermination
	if err := sink.WriteError("ERR", "msg", 500); !errors.Is(err, ErrPrematureTermination) {
		t.Errorf("expected ErrPrematureTermination, got %v", err)
	}
}

// TestWebSocketEgressSink_ErrorFrame 发现背景：验证 WebSocket 在报错时准确输出带状态码与 reqId 的 JSON 错误帧。
func TestWebSocketEgressSink_ErrorFrame(t *testing.T) {
	fs := &fakeFrameSender{}
	sink := NewWebSocketEgressSinkFromFrameSender(fs)

	// 先发 meta 绑定 reqID
	_ = sink.WriteMeta(EgressMeta{ReqID: "req-ws-99"})
	_ = sink.WriteError("CUSTOM_ERR", "custom error message", 403)

	if len(fs.jsonMsgs) < 2 {
		t.Fatalf("expected 2 messages, got %d", len(fs.jsonMsgs))
	}
	errFrame := fs.jsonMsgs[1].(map[string]any)
	if errFrame["type"] != "err" || errFrame["code"] != "CUSTOM_ERR" || errFrame["reqId"] != "req-ws-99" {
		t.Errorf("unexpected ws err frame: %+v", errFrame)
	}
}

