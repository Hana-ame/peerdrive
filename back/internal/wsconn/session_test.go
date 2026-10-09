package wsconn

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeConn is a scripted Conn. It records every call so the deadline ladder,
// the write ordering and the close ordering can be asserted directly, which
// a real socket cannot give (gorilla's ReadMessage swallows control frames
// and normalizes errors, so neither the ping wire frame nor the deadline
// values would be observable).
//
// It is deliberately synchronous: no goroutine of its own, so there is no
// place for a race to hide.
type fakeConn struct {
	mu sync.Mutex

	readLimit      int64
	readDeadlines  []time.Time
	writeDeadlines []time.Time
	pong           func(string) error
	writes         []writeCall
	readPos        int
	reads          []readResult
	closeCount     int

	closed bool
	done   chan struct{} // closed by Close, unblocks ReadMessage

	writeErr  error
	writeErrN int // return writeErr on the (N+1)-th write; 0 = never

	// gate arms one write to stop before it is recorded: gateWait is
	// signalled once that write has stopped, and gate is the release.
	// A gate rather than a blocking flag, because the test must be able to
	// observe "the writer is stuck" without guessing at timing.
	gate     chan struct{}
	gateWait chan struct{}
}

type writeCall struct {
	kind     string // "json", "message", "control"
	opcode   int
	data     []byte // for message/control
	payload  any    // for json
	deadline time.Time
}

type readResult struct {
	opcode int
	data   []byte
	err    error
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		done:   make(chan struct{}),
		reads:  make([]readResult, 0, 8),
		writes: make([]writeCall, 0, 16),
	}
}

func (f *fakeConn) SetReadLimit(n int64) {
	f.mu.Lock()
	f.readLimit = n
	f.mu.Unlock()
}

func (f *fakeConn) SetReadDeadline(t time.Time) error {
	f.mu.Lock()
	f.readDeadlines = append(f.readDeadlines, t)
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) SetWriteDeadline(t time.Time) error {
	f.mu.Lock()
	f.writeDeadlines = append(f.writeDeadlines, t)
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) SetPongHandler(h func(string) error) {
	f.mu.Lock()
	f.pong = h
	f.mu.Unlock()
}

// push queues an inbound message for the next ReadMessage.
func (f *fakeConn) push(r readResult) {
	f.mu.Lock()
	f.reads = append(f.reads, r)
	f.mu.Unlock()
}

func (f *fakeConn) ReadMessage() (int, []byte, error) {
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return 0, nil, io.EOF
		}
		if f.readPos < len(f.reads) {
			r := f.reads[f.readPos]
			f.readPos++
			f.mu.Unlock()
			return r.opcode, r.data, r.err
		}
		f.mu.Unlock()
		select {
		case <-f.done:
			return 0, nil, io.EOF
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// armGate makes the next write stop before it is recorded. It returns a
// channel that is signalled once that write has stopped (so the caller knows
// the lock is held) and a channel that releases it.
func (f *fakeConn) armGate() (waiting <-chan struct{}, release chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := make(chan struct{})
	w := make(chan struct{}, 1)
	f.gate = g
	f.gateWait = w
	return w, g
}

func (f *fakeConn) write(kind string, opcode int, data []byte, payload any, deadline time.Time) error {
	f.mu.Lock()
	g := f.gate
	if g != nil {
		f.gate = nil
		w := f.gateWait
		f.mu.Unlock()
		select {
		case w <- struct{}{}:
		default:
		}
		<-g
		f.mu.Lock()
	}
	idx := len(f.writes)
	f.writes = append(f.writes, writeCall{
		kind: kind, opcode: opcode, data: data,
		payload: payload, deadline: deadline,
	})
	err := f.writeErr
	if idx < f.writeErrN {
		err = nil
	}
	f.mu.Unlock()
	return err
}

func (f *fakeConn) WriteControl(messageType int, data []byte, deadline time.Time) error {
	return f.write("control", messageType, data, nil, deadline)
}

func (f *fakeConn) WriteJSON(v any) error {
	return f.write("json", 0, nil, v, time.Time{})
}

func (f *fakeConn) WriteMessage(messageType int, data []byte) error {
	return f.write("message", messageType, data, nil, time.Time{})
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCount++
	if !f.closed {
		f.closed = true
		close(f.done)
	}
	return nil
}

func (f *fakeConn) snapshotWrites() []writeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]writeCall(nil), f.writes...)
}

func (f *fakeConn) closeCount_() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closeCount
}

func (f *fakeConn) lastReadDeadline() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.readDeadlines) == 0 {
		return time.Time{}
	}
	return f.readDeadlines[len(f.readDeadlines)-1]
}

// ---------------------------------------------------------------------------
// construction and the default constants
// ---------------------------------------------------------------------------

func TestNew_Defaults(t *testing.T) {
	// 发现背景：拆分把 5 个内联常量集中进 defaults()；这里钉死默认值，
	// 任何一个漂移都会改变线上行为（无界内存 / 死会话泄漏 / 5 分钟挂起）。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	defer s.Close()

	fc.mu.Lock()
	rl := fc.readLimit
	fc.mu.Unlock()
	if rl != 3*64*1024 {
		t.Fatalf("ReadLimit = %d, want 196608 (3x64KB)", rl)
	}
	if d := time.Until(fc.lastReadDeadline()); d < 88*time.Second || d > 91*time.Second {
		t.Fatalf("ReadDeadline = %v from now, want 90s", d)
	}
	if s.ID() != "t" {
		t.Fatalf("ID = %q, want %q", s.ID(), "t")
	}
}

func TestNew_ZeroFieldsGetDefaults(t *testing.T) {
	// 发现背景：Options 用「零值即默认」合并，所以 Options{} 必须等价于
	// 拆分前的硬编码行为；这里只给一个字段，其余仍应取默认。
	fc := newFakeConn()
	s := New("t", fc, Options{ReadDeadline: 7 * time.Second})
	defer s.Close()

	d := time.Until(fc.lastReadDeadline())
	if d < 6500*time.Millisecond || d > 7100*time.Millisecond {
		t.Fatalf("ReadDeadline = %v from now, want 7s", d)
	}
	fc.mu.Lock()
	rl := fc.readLimit
	fc.mu.Unlock()
	if rl != 3*64*1024 {
		t.Fatalf("ReadLimit = %d, want the default 196608", rl)
	}
}

func TestDefaults(t *testing.T) {
	// 发现背景：defaults() 是行为不变的唯一守门点；逐字段钉住。
	o := defaults()
	if o.ReadLimit != 3*64*1024 || o.ReadDeadline != 90*time.Second ||
		o.WriteDeadline != 15*time.Second || o.PingInterval != 30*time.Second ||
		o.PingTimeout != 10*time.Second {
		t.Fatalf("defaults() = %+v, want the historical constants", o)
	}
}

// ---------------------------------------------------------------------------
// heartbeat and keep-alive
// ---------------------------------------------------------------------------

func TestHeartbeatPingWireShape(t *testing.T) {
	// 发现背景：M5 之前没有心跳，死掉的浏览器 tab 会永久泄漏 readLoop 和
	// 会话，pending fetch 挂满 5 分钟。这里断言 ping 的 opcode、payload
	// 和写超时与拆分前逐字一致。
	fc := newFakeConn()
	s := New("t", fc, Options{PingInterval: 15 * time.Millisecond, PingTimeout: 3 * time.Second})
	defer s.Close()

	deadline := time.Now().Add(3 * time.Second)
	var w writeCall
	for {
		for _, c := range fc.snapshotWrites() {
			if c.kind == "control" {
				w = c
				goto found
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("no ping was written within 3s")
		}
		time.Sleep(2 * time.Millisecond)
	}
found:
	if w.opcode != OpcodePing {
		t.Fatalf("ping opcode = 0x%02x, want 0x09", w.opcode)
	}
	if len(w.data) != 0 {
		t.Fatalf("ping payload = %q, want empty", w.data)
	}
	if d := time.Until(w.deadline); d < 2500*time.Millisecond || d > 3100*time.Millisecond {
		t.Fatalf("ping write deadline = %v from now, want PingTimeout 3s", d)
	}
}

func TestHeartbeatStopsOnWriteError(t *testing.T) {
	// 发现背景：ping 写失败说明连接已断，此时必须立刻退出而不是等
	// 读超时——否则心跳 goroutine 泄漏到进程结束。
	fc := newFakeConn()
	fc.writeErr = errors.New("boom")
	fc.writeErrN = 1 // the first ping succeeds, the second fails
	s := New("t", fc, Options{PingInterval: 5 * time.Millisecond, PingTimeout: time.Second})
	defer s.Close()

	awaitPings(t, fc, 2)
	time.Sleep(80 * time.Millisecond)
	if n := countPings(fc); n != 2 {
		t.Fatalf("heartbeat kept pinging after a write error: %d pings", n)
	}
}

func TestPongRenewsReadDeadline(t *testing.T) {
	// 发现背景：pong 续期读超时是 keep-alive 的另一半——浏览器对 ping
	// 自动回 pong，活着的对端靠协议本身续期，不需要额外的读定时器。
	fc := newFakeConn()
	s := New("t", fc, Options{ReadDeadline: 7 * time.Second})
	defer s.Close()

	fc.mu.Lock()
	h := fc.pong
	fc.mu.Unlock()
	if h == nil {
		t.Fatal("no pong handler was installed")
	}
	if err := h("payload"); err != nil {
		t.Fatalf("pong handler returned %v, want nil", err)
	}
	d := time.Until(fc.lastReadDeadline())
	if d < 6500*time.Millisecond || d > 7100*time.Millisecond {
		t.Fatalf("renewed read deadline = %v from now, want 7s", d)
	}
}

// ---------------------------------------------------------------------------
// inbound dispatch
// ---------------------------------------------------------------------------

func TestReadDispatchTextAndBinary(t *testing.T) {
	// 发现背景：text/binary 判定是传输层对帧协议的唯一贡献，前端靠
	// IsText 区分控制帧与数据帧。断言映射正确且 payload 原样透传。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	defer s.Close()

	got := make(chan Frame, 2)
	s.OnMessage(func(f Frame) { got <- f })

	txt := []byte(`{"type":"req","reqId":"r1","offset":0}`)
	bin := []byte{0x00, 0x7f, 0xff}
	fc.push(readResult{opcode: OpcodeText, data: txt})
	fc.push(readResult{opcode: OpcodeBinary, data: bin})

	a := awaitFrame(t, got)
	if !a.IsText || !bytes.Equal(a.Data, txt) {
		t.Fatalf("text frame: IsText=%v Data=%q, want IsText=true and the payload unchanged", a.IsText, a.Data)
	}
	b := awaitFrame(t, got)
	if b.IsText || !bytes.Equal(b.Data, bin) {
		t.Fatalf("binary frame: IsText=%v Data=%v, want IsText=false and the payload unchanged", b.IsText, b.Data)
	}
}

func TestReadErrorClosesSession(t *testing.T) {
	// 发现背景：任何读错误都必须结束会话——否则断连的对端永久占用会话
	// 槽位，dedupConn 也无法回收，最终连接表泄漏。
	fc := newFakeConn()
	s := New("t", fc, Options{})

	var (
		mu sync.Mutex
		n  int
	)
	done := make(chan struct{}, 1)
	s.OnClose(func() {
		mu.Lock()
		n++
		mu.Unlock()
		select {
		case done <- struct{}{}:
		default:
		}
	})
	fc.push(readResult{err: io.EOF})

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OnClose was not fired after a read error")
	}
	if fc.closeCount_() != 1 {
		t.Fatalf("conn.Close called %d times, want 1", fc.closeCount_())
	}
	if n != 1 {
		t.Fatalf("OnClose fired %d times, want 1", n)
	}
}

func TestOnMessageUnsetDeliversNothing(t *testing.T) {
	// 发现背景：readLoop 在回调未注册时静默丢弃，不能 panic——
	// 拆分前的行为如此，管理面会话在回调注册前就可能收到帧。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	defer s.Close()

	// no callback registered: the frame must be consumed and dropped
	// silently, without panicking or blocking the loop.
	fc.push(readResult{opcode: OpcodeText, data: []byte(`{"type":"req"}`)})
	awaitReads(t, fc, 1)

	// the loop is still alive and will dispatch to a callback registered later
	ch := make(chan Frame, 1)
	s.OnMessage(func(f Frame) { ch <- f })
	fc.push(readResult{opcode: OpcodeText, data: []byte(`{"type":"meta"}`)})
	f := awaitFrame(t, ch)
	if !bytes.Contains(f.Data, []byte(`"meta"`)) {
		t.Fatalf("late-registered callback got %+v", f)
	}
}

// ---------------------------------------------------------------------------
// outbound
// ---------------------------------------------------------------------------

func TestSendJSON(t *testing.T) {
	// 发现背景：SendJSON 走 WriteJSON 并附带写超时；写超时是 WS 唯一的
	// 背压手段（见 doc.go），这里断言两个字段都在。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	defer s.Close()

	v := map[string]any{"type": "done", "reqId": "r9"}
	if err := s.SendJSON(v); err != nil {
		t.Fatalf("SendJSON: %v", err)
	}
	w := fc.snapshotWrites()
	if len(w) != 1 {
		t.Fatalf("want 1 write, got %d", len(w))
	}
	if w[0].kind != "json" || !reflect.DeepEqual(w[0].payload, v) {
		t.Fatalf("write = %+v, want the payload handed to WriteJSON unchanged", w[0])
	}
	if len(fc.snapshotWriteDeadlines()) != 1 {
		t.Fatal("no write deadline was set")
	}
	d := time.Until(fc.snapshotWriteDeadlines()[0])
	if d < 14*time.Second || d > 16*time.Second {
		t.Fatalf("write deadline = %v from now, want WriteDeadline 15s", d)
	}
}

func TestSendFrameShapes(t *testing.T) {
	// 发现背景：SendFrame 的帧数取决于 body 长度——空 body 只发 header。
	// 这个分支差异是协议契约的一部分（接收方按帧序配对）。
	cases := []struct {
		body   []byte
		writes int
	}{
		{nil, 1},
		{[]byte{}, 1},
		{[]byte{0x01}, 2},
		{[]byte{0x01, 0x02}, 2},
	}
	for i, tc := range cases {
		t.Run("", func(t *testing.T) {
			fc := newFakeConn()
			s := New("t", fc, Options{})
			defer s.Close()
			if err := s.SendFrame(map[string]string{"type": "data"}, tc.body); err != nil {
				t.Fatalf("case %d: SendFrame: %v", i, err)
			}
			w := fc.snapshotWrites()
			if len(w) != tc.writes {
				t.Fatalf("case %d (len(body)=%d): want %d writes, got %d", i, len(tc.body), tc.writes, len(w))
			}
			if w[0].kind != "json" {
				t.Fatalf("case %d: first write kind = %q, want json header", i, w[0].kind)
			}
			if tc.writes == 2 {
				if w[1].kind != "message" || w[1].opcode != OpcodeBinary {
					t.Fatalf("case %d: body write = %+v, want binary message", i, w[1])
				}
				if !bytes.Equal(w[1].data, tc.body) {
					t.Fatalf("case %d: body payload changed", i)
				}
			}
		})
	}
}

func TestSendFrame_AtomicUnderConcurrency(t *testing.T) {
	// 发现背景：SendFrame 必须在同一把锁内写完 header+body——接收方按
	// 「一个 text 头 + 紧接着一个 binary body」配对，任何交错都会让对端
	// 把别人的 body 归到自己头上。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	defer s.Close()

	// stop the header write in flight: while it is stopped it holds sendMu
	waiting, release := fc.armGate()

	frame := make(chan error, 1)
	go func() { frame <- s.SendFrame("header", []byte("body")) }()
	select {
	case <-waiting:
	case <-time.After(3 * time.Second):
		t.Fatal("SendFrame did not reach the header write")
	}

	other := make(chan error, 1)
	go func() { other <- s.SendJSON("other") }()
	select {
	case <-other:
		t.Fatal("a concurrent SendJSON got sendMu while SendFrame held it")
	case <-time.After(50 * time.Millisecond):
	}

	close(release)
	if err := <-frame; err != nil {
		t.Fatalf("SendFrame: %v", err)
	}
	if err := <-other; err != nil {
		t.Fatalf("SendJSON: %v", err)
	}

	w := fc.snapshotWrites()
	if len(w) != 3 {
		t.Fatalf("want 3 writes, got %d: %+v", len(w), w)
	}
	if w[0].kind != "json" || w[1].kind != "message" || w[2].kind != "json" {
		t.Fatalf("write order = %q%q%q, want json,message,json (no interleaving)",
			w[0].kind, w[1].kind, w[2].kind)
	}
	if !reflect.DeepEqual(w[2].payload, "other") {
		t.Fatalf("third write payload = %v, want the second call's payload", w[2].payload)
	}
}

func TestSendFrame_HeaderFailureWritesNothingElse(t *testing.T) {
	// 发现背景：header 写失败要立即返回，绝不能再发 body——否则对端
	// 会收到一个孤儿 body 并错配到别的帧上。
	fc := newFakeConn()
	fc.writeErr = errors.New("write failed")
	s := New("t", fc, Options{})
	defer s.Close()

	if err := s.SendFrame("hdr", []byte("body")); err == nil {
		t.Fatal("SendFrame should return the header write error")
	}
	w := fc.snapshotWrites()
	if len(w) != 1 || w[0].kind != "json" {
		t.Fatalf("after a failed header, writes = %+v, want only the header attempt", w)
	}
}

func TestSendFrame_BodyFailureReturnsAfterHeaderOnWire(t *testing.T) {
	// 发现背景：body 写失败时 header 已在链路上——这是拆分前的既有
	// 行为，不「修复」成回滚，因为回滚会让对端多等一个完整超时。
	fc := newFakeConn()
	fc.writeErr = errors.New("body failed")
	fc.writeErrN = 1 // first write (header) succeeds, second fails
	s := New("t", fc, Options{})
	defer s.Close()

	if err := s.SendFrame("hdr", []byte("body")); err == nil {
		t.Fatal("SendFrame should return the body write error")
	}
	w := fc.snapshotWrites()
	if len(w) != 2 {
		t.Fatalf("writes = %d, want header + body attempts", len(w))
	}
}

// ---------------------------------------------------------------------------
// close semantics
// ---------------------------------------------------------------------------

func TestClose_Idempotent(t *testing.T) {
	// 发现背景：readLoop 的 defer 和外部调用方都会调 Close；OnClose 必须
	// 恰好触发一次，否则业务层会重复清理连接表条目。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	var n int
	done := make(chan struct{}, 1)
	s.OnClose(func() {
		n++
		select {
		case done <- struct{}{}:
		default:
		}
	})
	s.Close()
	s.Close()
	s.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OnClose never fired")
	}
	if fc.closeCount_() != 1 {
		t.Fatalf("conn.Close called %d times, want 1", fc.closeCount_())
	}
	if n != 1 {
		t.Fatalf("OnClose fired %d times, want 1", n)
	}
}

func TestClose_CallbackMaySend(t *testing.T) {
	// 发现背景：Close 若在持锁状态下回调，OnClose 里调 SendJSON 会自锁
	// 死锁。这里直接验证回调能在同一连接上再发一个帧。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	done := make(chan struct{}, 1)
	s.OnClose(func() {
		if err := s.SendJSON("from-close"); err != nil {
			t.Errorf("SendJSON from OnClose: %v", err)
		}
		close(done)
	})
	s.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("OnClose callback deadlocked on sendMu")
	}
}

func TestCloseFromMessageCallback(t *testing.T) {
	// 发现背景：readLoop 在回调里调 Close 是支持的用法（协议允许对端
	// 收到 close 帧后触发清理）。这里验证不会自锁死锁。
	fc := newFakeConn()
	s := New("t", fc, Options{})
	done := make(chan struct{}, 1)
	s.OnMessage(func(Frame) { s.Close(); close(done) })
	fc.push(readResult{opcode: OpcodeClose, data: []byte{0x03, 0xe8}})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close from OnMessage deadlocked")
	}
}

func TestSendJSONAfterClose(t *testing.T) {
	// 发现背景：关闭后的写应该返回错误而不是阻塞——管理面在会话关闭
	// 的瞬间仍在发消息，卡住会拖垮 goroutine。
	fc := newFakeConn()
	fc.writeErr = errors.New("closed")
	s := New("t", fc, Options{})
	s.Close()
	if err := s.SendJSON("after"); err == nil {
		t.Fatal("SendJSON after Close should fail")
	}
}

// ---------------------------------------------------------------------------
// concurrency
// ---------------------------------------------------------------------------

func TestConcurrentAccess(t *testing.T) {
	// 发现背景：gorilla 不允许并发写，sendMu 是唯一防线；-race 下用
	// 高频混合写/读/注册/关闭确认没有数据竞争。
	fc := newFakeConn()
	s := New("t", fc, Options{PingInterval: 200 * time.Millisecond})

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			switch i % 4 {
			case 0:
				_ = s.SendJSON(map[string]int{"i": i})
			case 1:
				_ = s.SendFrame(map[string]int{"i": i}, []byte{byte(i), byte(i >> 8)})
			case 2:
				s.OnMessage(func(Frame) {})
				s.OnClose(func() {})
			case 3:
				s.Close()
			}
		}(i)
	}
	// inbound traffic at the same time, so readLoop races the writers
	go func() {
		for i := 0; i < 20; i++ {
			fc.push(readResult{opcode: OpcodeText, data: []byte(`{"type":"req","i":1}`)})
		}
	}()
	wg.Wait()
	s.Close()

	if n := fc.closeCount_(); n != 1 {
		t.Fatalf("conn.Close called %d times under contention, want 1", n)
	}
}

func TestConcurrentHeartbeatAndSends(t *testing.T) {
	// 发现背景：heartbeatLoop 和 SendJSON 共享 sendMu；高频心跳下
	// 并发写会放大任何锁遗漏。
	fc := newFakeConn()
	s := New("t", fc, Options{PingInterval: 1 * time.Millisecond})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.SendJSON(map[string]int{"x": 1})
		}()
	}
	wg.Wait()
	// the 20 sends complete in microseconds, before a 1ms tick can fire, so
	// let the heartbeat run for a while and then stop it.
	time.Sleep(30 * time.Millisecond)
	s.Close()

	// control frames and json frames must be strictly interleaved, never
	// overlapping — but there is no overlap to check across calls; what
	// matters is that every write was fully recorded, i.e. none was lost.
	w := fc.snapshotWrites()
	var pings, jsons int
	for _, c := range w {
		switch c.kind {
		case "control":
			pings++
		case "json":
			jsons++
		}
	}
	if jsons != 20 {
		t.Fatalf("json writes = %d, want 20", jsons)
	}
	if pings == 0 {
		t.Fatal("no heartbeat ping was recorded")
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func awaitFrame(t *testing.T, ch <-chan Frame) Frame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a frame")
		return Frame{}
	}
}

func awaitReads(t *testing.T, fc *fakeConn, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		fc.mu.Lock()
		n := fc.readPos
		fc.mu.Unlock()
		if n >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("readLoop consumed %d of %d messages", n, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func countPings(fc *fakeConn) int {
	n := 0
	for _, w := range fc.snapshotWrites() {
		if w.kind == "control" {
			n++
		}
	}
	return n
}

func awaitPings(t *testing.T, fc *fakeConn, want int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for countPings(fc) < want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if n := countPings(fc); n < want {
		t.Fatalf("got %d pings, want %d", n, want)
	}
}

func (f *fakeConn) snapshotWriteDeadlines() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Time(nil), f.writeDeadlines...)
}
