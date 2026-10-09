package transport

// Golden-vector contract test for the wsconn split.
//
// 发现背景：把 WSSession 从 internal/transport 拆到 internal/wsconn 时，行为
// 必须逐字节不变——帧线格式、超时值、错误文案一个都不能漂移。肉眼看不出来，
// 所以这里把当前代码产生的**原始 WebSocket 帧字节**和**解码结果**录成
// testdata/ws_vectors.json（先用拆分前的 refactor tip 生成并提交），然后每次
// go test 都重新采集一遍做逐字节比对。
//
// 生成 fixture：
//
//	go test -tags "nosqlite golden" -run TestGenerateGolden \
//	  -args -out=/path/to/vectors.json ./internal/transport/
//
// 客户端半边是手写的 RFC 6455 客户端：写带掩码的帧、读原始帧，所以 fixture
// 里没有一个字节是从 gorilla 推出来的。

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	peerjs "github.com/Hana-ame/go-peerjs"
)

const goldenFixture = "testdata/ws_vectors.json"

type goldenFile struct {
	GeneratedAt string      `json:"generated_at"`
	GoVersion   string      `json:"go_version"`
	GorillaVer  string      `json:"gorilla_websocket"`
	Vectors     []goldenVec `json:"vectors"`
}

type goldenVec struct {
	Name      string      `json:"name"`
	Op        string      `json:"op"`
	Header    any         `json:"header,omitempty"`
	BodyLen   int         `json:"body_len,omitempty"`
	BodyHead  string      `json:"body_head_b64,omitempty"`
	Raw       *frameRec   `json:"raw,omitempty"`
	Frames    []frameRec  `json:"frames,omitempty"`
	Delivered *delivered  `json:"delivered,omitempty"`
	Reply     []frameRec  `json:"reply,omitempty"`
	Closed    *bool       `json:"closed,omitempty"`
	Note      string      `json:"note"`
}

type delivered struct {
	IsText bool   `json:"is_text"`
	Len    int    `json:"data_len"`
	Head   string `json:"data_head_b64,omitempty"`
}

// frameRec is one raw WebSocket frame. Frames up to maxFrameHex bytes are
// recorded in full; larger ones are summarized as head + length. Beyond the
// header an oversized payload is a run of identical bytes and carries no
// information, while a full ~200KB hex blob would bloat the fixture and make
// every review of it a scroll through repetition.
type frameRec struct {
	Hex  string `json:"hex,omitempty"`
	Head string `json:"head_hex,omitempty"`
	Len  int    `json:"len"`
}

const maxFrameHex = 4096

func capFrame(b []byte) frameRec {
	if len(b) <= maxFrameHex {
		return frameRec{Hex: hexA(b), Len: len(b)}
	}
	h := 64
	if len(b) < h {
		h = len(b)
	}
	return frameRec{Head: hexA(b[:h]), Len: len(b)}
}

func capPtr(b []byte) *frameRec {
	fr := capFrame(b)
	return &fr
}

// TestGoldenVectors is the equivalence gate: whatever the split does, the raw
// wire bytes and the decode results must still match the pre-split fixture.
func TestGoldenVectors(t *testing.T) {
	want, err := loadFixture()
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	got := captureAll(t)

	if len(want) != len(got) {
		t.Fatalf("vector count: fixture %d, captured %d", len(want), len(got))
	}
	for i := range want {
		a, b := want[i], got[i]
		if a.Name != b.Name {
			t.Fatalf("vector %d: fixture %q, captured %q", i, a.Name, b.Name)
		}
		// Marshal both sides so JSON number types (int vs float64) and map
		// key order are normalized before comparing.
		ja, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("%s: marshal fixture: %v", a.Name, err)
		}
		jb, err := json.Marshal(b)
		if err != nil {
			t.Fatalf("%s: marshal captured: %v", b.Name, err)
		}
		if !reflect.DeepEqual(ja, jb) {
			t.Errorf("vector %s diverged from the pre-split baseline:\nfixture: %s\ncaptured: %s",
				a.Name, string(ja), string(jb))
		}
	}
	t.Logf("%d vectors match the pre-split baseline", len(want))
}

func loadFixture() ([]goldenVec, error) {
	b, err := os.ReadFile(goldenFixture)
	if err != nil {
		return nil, err
	}
	var f goldenFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, err
	}
	return f.Vectors, nil
}

// captureAll records every vector the current code produces. It is shared
// with the generator so the assertion and the fixture can never drift apart.
func captureAll(t *testing.T) []goldenVec {
	t.Helper()
	out := make([]goldenVec, 0, 32)
	out = append(out, captureSendVectors(t)...)
	out = append(out, captureReadVectors(t)...)
	return out
}

// ---------------------------------------------------------------------------
// write direction: session → client, raw frames captured on the wire
// ---------------------------------------------------------------------------

func captureSendVectors(t *testing.T) []goldenVec {
	t.Helper()
	out := make([]goldenVec, 0, 16)

	// Headers chosen to hit the interesting paths in encoding/json and in the
	// frame length encoding: short (<126), extended-16 (126-65535), and
	// characters encoding/json escapes.
	headers := []any{
		map[string]any{"type": "req", "reqId": "r1", "hash": strings.Repeat("a", 64), "offset": 0, "size": 65536},
		map[string]any{"type": "meta", "reqId": "r1", "size": 12345, "name": "a/b.txt"},
		map[string]any{"type": "done", "reqId": "r1", "offset": 12345},
		map[string]any{"type": "err", "reqId": "r1", "msg": "sha256 mismatch"},
		map[string]any{"type": "share", "reqId": "s1"},
		map[string]any{"type": "admin", "method": "GET", "path": "/peerjs/node?x=1", "reqId": "a1"},
		// encoding/json escapes <, > and &; it does not escape unicode.
		map[string]any{"type": "meta", "reqId": "u1", "name": "<script>&\"q\"中文"},
		// Crosses the 125-byte frame-length boundary.
		map[string]any{"type": "admin", "method": "POST", "path": "/peerjs/fetch", "reqId": "a2",
			"body": map[string]any{"peer": strings.Repeat("b", 40), "hash": strings.Repeat("c", 64),
				"offset": 0, "size": 1048576}},
	}

	for i, h := range headers {
		name := fmt.Sprintf("sendjson.h%02d", i)
		cl, sess, closeFn := openPair(t)

		if err := sess.SendJSON(h); err != nil {
			t.Fatalf("%s: SendJSON: %v", name, err)
		}
		frames, err := drainFrames(t, cl, 1, 500*time.Millisecond)
		closeFn()
		if err != nil {
			t.Fatalf("%s: drain: %v", name, err)
		}
		if len(frames) != 1 {
			t.Fatalf("%s: want exactly 1 frame, got %d", name, len(frames))
		}
		out = append(out, goldenVec{
			Name:   name,
			Op:     "sendjson",
			Header: h,
			Frames: frames,
			Note:   fmt.Sprintf("single text frame, %d bytes on the wire", frames[0].Len),
		})
	}

	// SendFrame across the frame-length boundaries: 0 (no second frame), 1,
	// 100 (2-byte length field), 200 (extended-16), 65537 (extended-64).
	head := map[string]any{"type": "data", "reqId": "d1", "offset": 4096}
	for _, n := range []int{0, 1, 100, 200, 65537} {
		name := fmt.Sprintf("sendframe.body%06d", n)
		cl, sess, closeFn := openPair(t)

		body := make([]byte, n)
		for i := range body {
			body[i] = byte(i*7 + 13)
		}
		if err := sess.SendFrame(head, body); err != nil {
			t.Fatalf("%s: SendFrame: %v", name, err)
		}
		want := 1
		if n > 0 {
			want = 2
		}
		frames, err := drainFrames(t, cl, want, 500*time.Millisecond)
		closeFn()
		if err != nil {
			t.Fatalf("%s: drain: %v", name, err)
		}
		if len(frames) != want {
			t.Fatalf("%s: want %d frames, got %d", name, want, len(frames))
		}
		note := "empty body writes only the header frame"
		if n > 0 {
			note = fmt.Sprintf("text header + binary body, %d bytes", n)
		}
		out = append(out, goldenVec{
			Name: name, Op: "sendframe", Header: head,
			BodyLen: n, BodyHead: b64(body[:min16(n)]),
			Frames: frames, Note: note,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// read direction: client → session, decode result + reply frames
// ---------------------------------------------------------------------------

func captureReadVectors(t *testing.T) []goldenVec {
	t.Helper()
	out := make([]goldenVec, 0, 16)

	limited := 3 * 64 * 1024 // matches the session's read limit

	cases := []readCase{
		{
			name: "read.text",
			build: func() []byte {
				return clientFrame(0x01, mustJSON(map[string]any{
					"type": "req", "reqId": "r9", "hash": strings.Repeat("e", 64), "offset": 0, "size": 1024,
				}))
			},
			wantDeliv: true,
			note:      "text frame → IsText=true, payload preserved",
		},
		{
			name: "read.binary",
			build: func() []byte {
				b := make([]byte, 512)
				for i := range b {
					b[i] = byte(255 - i%251)
				}
				return clientFrame(0x02, b)
			},
			wantDeliv: true,
			note:      "binary frame → IsText=false, payload preserved",
		},
		{
			name: "read.unicode",
			build: func() []byte {
				return clientFrame(0x01, mustJSON(map[string]any{
					"type": "meta", "name": "中文/path\u2028ctl"}))
			},
			wantDeliv: true,
			note:      "utf-8 payload including U+2028",
		},
		{
			name: "read.escapes",
			build: func() []byte {
				return clientFrame(0x01, mustJSON(map[string]any{
					"type": "meta", "name": "<x>&\"q\""}))
			},
			wantDeliv: true,
			note:      "json-escaped characters round-trip",
		},
		{
			name: "read.overlimit.text",
			build: func() []byte {
				return clientFrame(0x01, []byte(strings.Repeat("x", limited+100)))
			},
			wantClose: true,
			note:      "over the read limit: no callback, session closes, close 1009 written back",
		},
		{
			name: "read.overlimit.binary",
			build: func() []byte {
				return clientFrame(0x02, make([]byte, limited+1))
			},
			wantClose: true,
			note:      "binary over the read limit: close 1009 written back",
		},
		{
			name: "read.badopcode",
			build: func() []byte { return clientFrame(0x07, []byte("nope")) },
			wantClose: true,
			note:      "reserved opcode: protocol error, close 1002 written back",
		},
		{
			name: "read.close.normal",
			build: func() []byte { return clientFrame(0x08, []byte{0x03, 0xe8}) }, // 1000
			wantClose: true,
			note:      "client close 1000: session closes, close frame echoed back",
		},
		{
			name: "read.close.abnormal",
			build: func() []byte { return clientFrame(0x08, []byte{0x03, 0xe9}) }, // 1001
			wantClose: true,
			note:      "client close 1001: session closes",
		},
	}

	for _, tc := range cases {
		out = append(out, captureReadVec(t, tc))
	}

	// Truncated frame: the header promises more bytes than are ever sent. The
	// client write side is closed so the pending read terminates instead of
	// blocking forever.
	{
		cl, sess, closeFn := openPair(t)
		closedCh, closed := awaitClose(sess)

		f := clientFrame(0x02, make([]byte, 100))
		if _, err := cl.Write(f[:2+8]); err != nil { // send the header only
			t.Fatalf("read.truncated: write: %v", err)
		}
		cl.Close()

		expectClosed(t, "read.truncated", closedCh)
		rec := closed() // captured BEFORE closeFn, which would close it unconditionally
		closeFn()
		out = append(out, goldenVec{
			Name:   "read.truncated",
			Op:     "read",
			Raw:    capPtr(f[:2+8]),
			Closed: rec,
			Note:   "header promises 100 bytes, only the header is sent: read fails, session closes",
		})
	}
	return out
}

type readCase struct {
	name      string
	build     func() []byte
	wantDeliv bool
	wantClose bool
	note      string
}

func captureReadVec(t *testing.T, tc readCase) goldenVec {
	t.Helper()
	cl, sess, closeFn := openPair(t)
	closedCh, closed := awaitClose(sess)

	gotCh := make(chan delivered, 1)
	sess.OnMessage(func(fr peerjs.Frame) {
		d := delivered{IsText: fr.IsText, Len: len(fr.Data)}
		if len(fr.Data) > 0 {
			d.Head = b64(fr.Data[:min16(len(fr.Data))])
		}
		select {
		case gotCh <- d:
		default:
		}
	})

	raw := tc.build()
	if _, err := cl.Write(raw); err != nil {
		t.Fatalf("%s: write: %v", tc.name, err)
	}

	var got *delivered
	if tc.wantDeliv {
		select {
		case d := <-gotCh:
			got = &d
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: no message delivered", tc.name)
		}
	} else {
		// Wait for the readLoop to get a chance, then confirm nothing came
		// through. A message here means the failure case was mishandled.
		select {
		case d := <-gotCh:
			t.Fatalf("%s: unexpected message delivered: %+v", tc.name, d)
		case <-time.After(200 * time.Millisecond):
		}
	}

	reply, err := drainFrames(t, cl, -1, 200*time.Millisecond)
	if err != nil {
		t.Fatalf("%s: drain replies: %v", tc.name, err)
	}
	if tc.wantClose {
		expectClosed(t, tc.name, closedCh)
	}

	rec := closed() // captured BEFORE closeFn, which would close it unconditionally
	closeFn()
	return goldenVec{
		Name:      tc.name,
		Op:        "read",
		Raw:       capPtr(raw),
		Delivered: got,
		Reply:     reply,
		Closed:    rec,
		Note:      tc.note,
	}
}

// ---------------------------------------------------------------------------
// plumbing
// ---------------------------------------------------------------------------

// openPair performs the RFC 6455 client half of the handshake over a real
// loopback TCP connection and returns the client socket (for raw frame I/O)
// plus a session built on the upgraded server socket.
//
// A real socket rather than net.Pipe: pipe writes block until the reader
// consumes, which deadlocks against the Upgrader writing the 101 response.
// The origin decision is a no-op here — the golden data is about frame bytes,
// and the origin policy has its own tests in wsconn.
func openPair(t *testing.T) (net.Conn, *WSSession, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			accepted <- c
		}
	}()

	cl, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	var sr net.Conn
	select {
	case sr = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("accept timeout")
	}

	if _, err := cl.Write(upgradeRequest); err != nil {
		t.Fatalf("write upgrade request: %v", err)
	}
	// The server reads the request and keeps its bufio.Reader so Upgrade can
	// reuse it and observe zero buffered bytes — the state net/http leaves it
	// in.
	req, br, err := readHTTPRequest(sr)
	if err != nil {
		t.Fatalf("read upgrade request: %v", err)
	}

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := up.Upgrade(&hijackRW{conn: sr, reader: br}, req, nil)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}

	// The 101 must be drained before frames are read, otherwise its bytes
	// would be parsed as a frame header.
	cl.SetReadDeadline(time.Now().Add(5 * time.Second))
	if err := drainHTTPResponse(cl); err != nil {
		t.Fatalf("drain 101 response: %v", err)
	}
	cl.SetReadDeadline(time.Time{})
	sr.SetDeadline(time.Time{})

	sess := NewWSSession("local", conn)
	return cl, sess, func() {
		sess.Close()
		_ = conn.Close()
		_ = sr.Close()
		_ = cl.Close()
		_ = ln.Close()
	}
}

// upgradeRequest is the HTTP request the hand-rolled client sends. The
// Sec-WebSocket-Key is fixed so the whole exchange is reproducible.
var upgradeRequest = []byte("GET /ws/peer HTTP/1.1\r\n" +
	"Host: localhost\r\n" +
	"Connection: Upgrade\r\n" +
	"Upgrade: websocket\r\n" +
	"Origin: http://localhost:5173\r\n" +
	"Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n" +
	"Sec-WebSocket-Version: 13\r\n" +
	"\r\n")

// hijackRW is an http.ResponseWriter that hands the real socket back to the
// Upgrader, the way net/http does after reading the request.
type hijackRW struct {
	conn   net.Conn
	reader *bufio.Reader
	h      http.Header
}

func (h *hijackRW) Header() http.Header {
	if h.h == nil {
		h.h = http.Header{}
	}
	return h.h
}
func (h *hijackRW) Write(p []byte) (int, error) { return len(p), nil }
func (h *hijackRW) WriteHeader(status int)      {}
func (h *hijackRW) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return h.conn, bufio.NewReadWriter(h.reader, bufio.NewWriter(h.conn)), nil
}

// readHTTPRequest reads one HTTP request and returns it together with the
// buffered reader, so the Upgrader can reuse it and observe zero buffered
// bytes.
func readHTTPRequest(r io.Reader) (*http.Request, *bufio.Reader, error) {
	br := bufio.NewReader(r)
	req, err := http.ReadRequest(br)
	if err != nil {
		return nil, nil, err
	}
	return req, br, nil
}

// drainHTTPResponse reads one HTTP response off the wire (the 101).
func drainHTTPResponse(r io.Reader) error {
	br := bufio.NewReader(r)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if strings.TrimSpace(line) == "" {
			return nil
		}
	}
}

// awaitClose wires OnClose to a channel and returns it plus a reader for the
// observed closed flag.
func awaitClose(sess *WSSession) (<-chan struct{}, func() *bool) {
	ch := make(chan struct{}, 1)
	var (
		mu     sync.Mutex
		closed bool
	)
	sess.OnClose(func() {
		mu.Lock()
		closed = true
		mu.Unlock()
		select {
		case ch <- struct{}{}:
		default:
		}
	})
	return ch, func() *bool {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			return nil
		}
		v := true
		return &v
	}
}

func expectClosed(t *testing.T, name string, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s: session did not close", name)
	}
}

// ---------------------------------------------------------------------------
// raw frame codec, hand-rolled: no gorilla
// ---------------------------------------------------------------------------

func hexA(b []byte) string { return fmt.Sprintf("%x", b) }
func b64(b []byte) string  { return base64.StdEncoding.EncodeToString(b) }
func min16(n int) int      {
	if n < 16 {
		return n
	}
	return 16
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// clientFrame builds one client→server frame, masked as RFC 6455 requires.
// The mask key is fixed so the recorded bytes are reproducible.
func clientFrame(opcode byte, payload []byte) []byte {
	var b []byte
	b = append(b, 0x80|opcode)
	switch {
	case len(payload) < 126:
		b = append(b, 0x80|byte(len(payload)))
	case len(payload) < 65536:
		b = append(b, 0x80|126)
		b = append(b, byte(len(payload)>>8), byte(len(payload)))
	default:
		b = append(b, 0x80|127)
		var e [8]byte
		binary.BigEndian.PutUint64(e[:], uint64(len(payload)))
		b = append(b, e[:]...)
	}
	mask := []byte{0x37, 0x02, 0xa3, 0xd4}
	b = append(b, mask...)
	out := make([]byte, len(payload))
	for i, c := range payload {
		out[i] = c ^ mask[i%4]
	}
	return append(b, out...)
}

// drainFrames reads up to max frames. max<0 means "until the deadline".
// End of stream (EOF, a short read, ECONNRESET from a closing peer, or a
// deadline) is not an error: the caller decides whether the frames it got are
// what it expected. Anything else is a real failure.
func drainFrames(t *testing.T, cl net.Conn, max int, wait time.Duration) ([]frameRec, error) {
	t.Helper()
	var out []frameRec
	cl.SetReadDeadline(time.Now().Add(wait))
	defer cl.SetReadDeadline(time.Time{})
	for max < 0 || len(out) < max {
		fr, err := readRawFrame(cl)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
				errors.Is(err, syscall.ECONNRESET) || isTimeout(err) {
				break
			}
			return out, err
		}
		out = append(out, capFrame(fr))
	}
	return out, nil
}

func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// readRawFrame reads exactly one frame: header, mask key and payload.
func readRawFrame(r io.Reader) ([]byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	masked := hdr[1]&0x80 != 0
	n := int(hdr[1] & 0x7f)

	var extra []byte
	switch {
	case n == 126:
		extra = make([]byte, 2)
		if _, err := io.ReadFull(r, extra); err != nil {
			return nil, err
		}
		n = int(binary.BigEndian.Uint16(extra))
	case n == 127:
		extra = make([]byte, 8)
		if _, err := io.ReadFull(r, extra); err != nil {
			return nil, err
		}
		n = int(binary.BigEndian.Uint64(extra))
	}
	var mk []byte
	if masked {
		mk = make([]byte, 4)
		if _, err := io.ReadFull(r, mk); err != nil {
			return nil, err
		}
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	out := make([]byte, 0, 2+len(extra)+len(mk)+n)
	out = append(out, hdr[:]...)
	out = append(out, extra...)
	out = append(out, mk...)
	out = append(out, payload...)
	return out, nil
}
