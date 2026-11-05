package node

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Hana-ame/peerdrive/webrtc"
)

// 发现背景：e2e 链路的两个断点都在「区间」上——对端拿 sha256 要数据时，
// offset/length 的裁剪、以及每段的摘要，错一处浏览器就会拼出坏文件、或者
// 永远等不到 check。这里用内存 sender 把协议逐帧钉住，不依赖真实 WebRTC。
func testService(t *testing.T) (svc *Service, payload string, content []byte) {
	t.Helper()
	dir := t.TempDir()

	// 2 MiB + 一点零头：跨过多个 1 MiB 分块，且最后一块不是整块。
	content = make([]byte, 2*1024*1024+12345)
	for i := range content {
		content[i] = byte(i*31 + 7)
	}
	payload = filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(payload, content, 0o644); err != nil {
		t.Fatalf("写 payload: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "note.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatalf("写 note: %v", err)
	}

	svc, err := Load(dir, filepath.Join(t.TempDir(), "metadata.db"), "test")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	return svc, payload, content
}

func TestLoadBuildsCollectionFromStore(t *testing.T) {
	svc, payload, content := testService(t)
	coll := svc.Collection()

	if coll.Count != 2 {
		t.Fatalf("collection.Count = %d, want 2", coll.Count)
	}
	if want := int64(len(content) + len("hello")); coll.TotalBytes != want {
		t.Fatalf("collection.TotalBytes = %d, want %d", coll.TotalBytes, want)
	}

	var ref *FileRef
	for i := range coll.Files {
		if coll.Files[i].Filename == "payload.bin" {
			ref = &coll.Files[i]
		}
	}
	if ref == nil {
		t.Fatal("collection 里没有 payload.bin")
	}
	wantSHA := sha256.Sum256(content)
	if ref.SHA256 != hex.EncodeToString(wantSHA[:]) {
		t.Fatalf("sha256 = %s, want %x", ref.SHA256, wantSHA)
	}
	if ref.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", ref.Size, len(content))
	}
	if ref.Path != payload {
		t.Fatalf("path = %s, want %s", ref.Path, payload)
	}
}

func TestServeFileRangeBytesAndDigest(t *testing.T) {
	svc, _, content := testService(t)
	sha := sha256.Sum256(content)
	digest := hex.EncodeToString(sha[:])

	offset := int64(123*1024 + 5)
	length := int64(200*1024 + 7)

	rec := &recorder{}
	if err := svc.ServeFile(rec, 7, FileRequest{SHA256: digest, Offset: offset, Length: length}); err != nil {
		t.Fatalf("ServeFile: %v", err)
	}

	header := rec.header(t)
	if header.SHA256 != digest || header.Offset != offset || header.Length != length || header.Size != int64(len(content)) {
		t.Fatalf("header = %+v", header)
	}

	got := rec.data(t)
	if !bytes.Equal(got, content[offset:offset+length]) {
		t.Fatalf("区间字节不一致：len(got)=%d len(want)=%d", len(got), length)
	}

	check := rec.check(t)
	wantDigest := sha256.Sum256(content[offset : offset+length])
	if check.Digest != hex.EncodeToString(wantDigest[:]) || check.Length != length || check.Stream != 7 {
		t.Fatalf("check = %+v", check)
	}
	if check.Offset != offset {
		t.Fatalf("check.Offset = %d, want %d", check.Offset, offset)
	}
}

// 发现背景：浏览器最后一块是按 1 MiB 切出来的，长度可能越过文件尾；服务端
// 若直接 ReadAt 会报错，正确语义是裁到文件尾（返回头里给出真实 length）。
func TestServeFileClampsTail(t *testing.T) {
	svc, _, content := testService(t)
	sha := sha256.Sum256(content)
	digest := hex.EncodeToString(sha[:])

	offset := int64(len(content) - 1000)
	rec := &recorder{}
	if err := svc.ServeFile(rec, 11, FileRequest{SHA256: digest, Offset: offset, Length: 999999}); err != nil {
		t.Fatalf("ServeFile: %v", err)
	}
	header := rec.header(t)
	if header.Length != 1000 {
		t.Fatalf("header.Length = %d, want 1000", header.Length)
	}
	if got := rec.data(t); !bytes.Equal(got, content[offset:]) {
		t.Fatalf("尾部区间字节不一致")
	}
}

// 发现背景：offset 或 sha256 不成立时必须回 KindError，而不是静默不发数据——
// 浏览器靠这条帧把块标成待重试，否则只会表现为「块永远没收完」。
func TestServeFileErrorsAreFrames(t *testing.T) {
	svc, _, content := testService(t)
	digest := hex.EncodeToString(func() []byte { s := sha256.Sum256(content); return s[:] }())

	cases := []struct {
		name string
		req  FileRequest
	}{
		{"unknown sha", FileRequest{SHA256: "00", Offset: 0, Length: 1}},
		{"offset past end", FileRequest{SHA256: digest, Offset: int64(len(content)) + 1, Length: 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{}
			if err := svc.ServeFile(rec, 3, tc.req); err != nil {
				t.Fatalf("ServeFile 应把错误编码成帧而不是直接返回: %v", err)
			}
			if kinds := rec.kinds(); len(kinds) != 1 || kinds[0] != webrtc.KindError {
				t.Fatalf("帧 = %v, want 一条 KindError", kinds)
			}
			var payload ErrorPayload
			if err := json.Unmarshal(rec.frames[0].Data, &payload); err != nil {
				t.Fatalf("ErrorPayload 解析失败: %v", err)
			}
			if payload.Message == "" {
				t.Fatal("ErrorPayload.Message 为空")
			}
		})
	}
}

func TestServeCollectionFrame(t *testing.T) {
	svc, _, _ := testService(t)
	rec := &recorder{}
	if err := svc.ServeCollection(rec, 5); err != nil {
		t.Fatalf("ServeCollection: %v", err)
	}
	if kinds := rec.kinds(); len(kinds) != 1 || kinds[0] != webrtc.KindMetadata {
		t.Fatalf("帧 = %v, want 一条 KindMetadata", kinds)
	}
	if rec.frames[0].Stream != 5 {
		t.Fatalf("stream = %d, want 5", rec.frames[0].Stream)
	}
	var got Collection
	if err := json.Unmarshal(rec.frames[0].Data, &got); err != nil {
		t.Fatalf("collection 解析失败: %v", err)
	}
	if got.Count != svc.Collection().Count {
		t.Fatalf("wire collection.Count = %d, want %d", got.Count, svc.Collection().Count)
	}
}

// ---- 内存 sender ----

type recorded struct {
	Kind   webrtc.Kind
	Stream uint32
	Data   []byte
}

type recorder struct {
	mu     sync.Mutex
	frames []recorded
}

func (r *recorder) SendJSON(kind webrtc.Kind, stream uint32, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, recorded{Kind: kind, Stream: stream, Data: b})
	return nil
}

func (r *recorder) SendBytes(stream uint32, b []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, recorded{Kind: webrtc.KindData, Stream: stream, Data: append([]byte(nil), b...)})
	return nil
}

func (r *recorder) kinds() []webrtc.Kind {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]webrtc.Kind, 0, len(r.frames))
	for _, f := range r.frames {
		out = append(out, f.Kind)
	}
	return out
}

func (r *recorder) header(t *testing.T) FileHeader {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.frames) == 0 || r.frames[0].Kind != webrtc.KindMetadata {
		t.Fatalf("首帧不是 metadata: %v", r.kinds())
	}
	var h FileHeader
	if err := json.Unmarshal(r.frames[0].Data, &h); err != nil {
		t.Fatalf("FileHeader 解析失败: %v", err)
	}
	return h
}

func (r *recorder) data(t *testing.T) []byte {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []byte
	for _, f := range r.frames {
		if f.Kind == webrtc.KindData {
			out = append(out, f.Data...)
		}
	}
	if len(out) == 0 {
		t.Fatal("没有 data 帧")
	}
	return out
}

func (r *recorder) check(t *testing.T) RangeCheck {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range r.frames {
		if f.Kind == webrtc.KindCheck {
			var c RangeCheck
			if err := json.Unmarshal(f.Data, &c); err != nil {
				t.Fatalf("RangeCheck 解析失败: %v", err)
			}
			return c
		}
	}
	t.Fatal("没有 check 帧")
	return RangeCheck{}
}
