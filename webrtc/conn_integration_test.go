package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	pion "github.com/pion/webrtc/v4"

	"github.com/Hana-ame/peerdrive/peerjs"
)

// 本文件是一组进程内集成测试：用真实的 pion PeerConnection 当浏览器，
// 通过一个假的 Signal 与我们自己的 Server.Accept 交换 SDP 与候选，
// 把整条链路跑通到 DataChannel 上传帧。
//
// 为什么不直接依赖 e2e：e2e 要起 Chromium、连公网 PeerJS，一轮十几分钟，
// 失败时只能看到日志。这里几秒就能复现「offer→answer→ICE→dc→帧」
// 的完整路径，而且能把桩打在任意一步上（候选早到、OnConn 阻塞等）。

// fakeSignal 记录 Server 回写的 answer 与候选。
type fakeSignal struct {
	mu       sync.Mutex
	answers  map[string]string
	cands    map[string][]pion.ICECandidateInit
	released []string
	answerCh chan string
	candCh   chan pion.ICECandidateInit
}

func newFakeSignal() *fakeSignal {
	return &fakeSignal{
		answers:  map[string]string{},
		cands:    map[string][]pion.ICECandidateInit{},
		answerCh: make(chan string, 8),
		candCh:   make(chan pion.ICECandidateInit, 64),
	}
}

func (f *fakeSignal) SendAnswer(remote, connectionID, sdp string) error {
	f.mu.Lock()
	f.answers[connectionID] = sdp
	f.mu.Unlock()
	f.answerCh <- sdp
	return nil
}

func (f *fakeSignal) SendCandidate(remote, connectionID string, cand pion.ICECandidateInit) error {
	f.mu.Lock()
	f.cands[connectionID] = append(f.cands[connectionID], cand)
	f.mu.Unlock()
	select {
	case f.candCh <- cand:
	default:
	}
	return nil
}

func (f *fakeSignal) Release(connectionID string) {
	f.mu.Lock()
	f.released = append(f.released, connectionID)
	f.mu.Unlock()
}

func (f *fakeSignal) waitAnswer(t *testing.T, d time.Duration) string {
	t.Helper()
	select {
	case sdp := <-f.answerCh:
		return sdp
	case <-time.After(d):
		t.Fatalf("%v 内没收到 answer", d)
		return ""
	}
}

func (f *fakeSignal) takeCandidates() []pion.ICECandidateInit {
	var out []pion.ICECandidateInit
	for {
		select {
		case c := <-f.candCh:
			out = append(out, c)
		default:
			return out
		}
	}
}

// newTestServer 造一个不连 PeerJS 的 Server。
//
// 不调 Listen：那会去 Dial 0.peerjs.com。Accept 内部用的是传进来的 sig
// 参数，s.sig 只在连接进入 Failed/Closed 时才被用于 Release，测试里
// 我们主动在收尾时清理，不会碰到它。
func newTestServer(cfg Config) *Server {
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	// 不给 STUN/TURN：进程内直连只需要 host 候选，省掉对外网的依赖。
	if cfg.ICEServers == nil {
		cfg.ICEServers = []pion.ICEServer{}
	}
	return &Server{
		cfg:     cfg,
		log:     cfg.Logger,
		live:    map[*Conn]struct{}{},
		pending: map[string]*pion.PeerConnection{},
		cands:   map[string][]pion.ICECandidateInit{},
		done:    make(chan struct{}),
	}
}

// peer 是「浏览器」侧的最小替身。
type peer struct {
	pc *pion.PeerConnection
	dc *pion.DataChannel
}

// newPeer 建一条带 DataChannel 的 PeerConnection 并生成 offer。
func newPeer(t *testing.T, label string) *peer {
	t.Helper()
	pc, err := pion.NewPeerConnection(pion.Configuration{ICEServers: []pion.ICEServer{}})
	if err != nil {
		t.Fatalf("NewPeerConnection: %v", err)
	}
	dc, err := pc.CreateDataChannel(label, nil)
	if err != nil {
		_ = pc.Close()
		t.Fatalf("CreateDataChannel: %v", err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		_ = pc.Close()
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := pc.SetLocalDescription(offer); err != nil {
		_ = pc.Close()
		t.Fatalf("SetLocalDescription: %v", err)
	}
	return &peer{pc: pc, dc: dc}
}

// setup 把 Server 与 peer 连起来，返回一条已 open 的 Conn。
//
// 这里刻意按真实顺序走：先 Accept 拿到 answer，再回设给 peer，然后
// 把两边的候选互相灌进去。
func setup(t *testing.T, srv *Server, sig *fakeSignal, p *peer, connID string) *Conn {
	t.Helper()

	connCh := make(chan *Conn, 1)
	srv.cfg.OnConn = func(c *Conn) { connCh <- c }

	// 候选回调必须在 Accept 之前挂好：pion 在 SetLocalDescription 之后
	// 立刻开始 gather，晚挂会漏掉最早那几条 host 候选。
	p.pc.OnICECandidate(func(c *pion.ICECandidate) {
		if c == nil {
			return
		}
		srv.Offer(connID, c.ToJSON())
	})

	offer := p.pc.LocalDescription()
	o := peerjs.Offer{
		Remote:        "peer-test",
		ConnectionID:  connID,
		Label:         p.dc.Label(),
		Serialization: "raw",
		SDP:           offer.SDP,
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		srv.Accept(o, sig)
	}()

	sdp := sig.waitAnswer(t, 10*time.Second)
	if err := p.pc.SetRemoteDescription(pion.SessionDescription{
		Type: pion.SDPTypeAnswer,
		SDP:  sdp,
	}); err != nil {
		t.Fatalf("peer SetRemoteDescription: %v", err)
	}

	// 把 Server 侧已经 gather 到的候选灌回去。
	for _, c := range sig.takeCandidates() {
		if err := p.pc.AddICECandidate(c); err != nil {
			t.Fatalf("peer AddICECandidate: %v", err)
		}
	}

	<-done

	select {
	case c := <-connCh:
		return c
	case <-time.After(20 * time.Second):
		t.Fatal("20s 内 OnConn 没被调用")
		return nil
	}
}

func TestAcceptCompletesHandshake(t *testing.T) {
	// 这是整条链路的核心用例：真 pion 客户端 → Accept → dc open → 双向帧。
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_test")
	defer p.pc.Close()

	conn := setup(t, srv, sig, p, "conn-1")
	defer conn.Close()

	if conn.ID() != "conn-1" {
		t.Errorf("ID = %q", conn.ID())
	}
	if conn.Remote() != "peer-test" {
		t.Errorf("Remote = %q", conn.Remote())
	}

	// 服务端回的 hello 必须能在对端读到。
	hello := readFrame(t, p.dc, 10*time.Second)
	if hello.Kind != KindHello {
		t.Errorf("首帧 kind = %s，want hello", hello.Kind)
	}
	var body map[string]any
	if err := json.Unmarshal(hello.Data, &body); err != nil {
		t.Fatalf("hello 不是合法 JSON: %v", err)
	}
	if body["role"] != "server" {
		t.Errorf("hello.role = %v，want server", body["role"])
	}
	if body["proto"] != "peerdrive/webrtc" {
		t.Errorf("hello.proto = %v", body["proto"])
	}
}

func TestAcceptBidirectionalFrames(t *testing.T) {
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_bidi")
	defer p.pc.Close()

	conn := setup(t, srv, sig, p, "conn-2")
	defer conn.Close()

	// 浏览器 → 服务端：一条 control。
	ping, _ := EncodeFrame(Frame{Kind: KindControl, Stream: 7, Data: []byte(`{"action":"ping"}`)})
	for _, b := range ping {
		if err := p.dc.Send(b); err != nil {
			t.Fatalf("peer Send: %v", err)
		}
	}

	got := nextFrameOf(t, conn, KindControl, 10*time.Second)
	if got.Stream != 7 {
		t.Errorf("stream = %d，want 7", got.Stream)
	}
	if string(got.Data) != `{"action":"ping"}` {
		t.Errorf("payload = %q", got.Data)
	}

	// 服务端 → 浏览器：一条 ack。
	if err := conn.SendJSON(KindAck, got.Stream, map[string]any{"ok": true}); err != nil {
		t.Fatalf("SendJSON: %v", err)
	}
	ack := readFrame(t, p.dc, 10*time.Second)
	if ack.Kind != KindAck {
		t.Errorf("kind = %s，want ack", ack.Kind)
	}
	if ack.Stream != 7 {
		t.Errorf("ack stream = %d，want 7", ack.Stream)
	}
}

func TestAcceptLargePayloadAcrossChunks(t *testing.T) {
	// 超过单帧上限的数据必须在对端被完整重组回来。这是 8 MiB 传输的
	// 缩小版：中间任何一边的切块/重组不一致，这里就会立刻对不上。
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_big")
	defer p.pc.Close()

	conn := setup(t, srv, sig, p, "conn-3")
	defer conn.Close()

	data := make([]byte, MaxFramePayload*3+1234)
	for i := range data {
		data[i] = byte(i * 7 % 256)
	}
	if err := conn.SendBytes(5, data); err != nil {
		t.Fatalf("SendBytes: %v", err)
	}

	got := readUntilKindData(t, p.dc, 5, 30*time.Second)
	if !bytes.Equal(got, data) {
		t.Fatalf("重组结果不一致：收到 %d 字节，want %d", len(got), len(data))
	}
}

func TestAcceptAppliesEarlyCandidate(t *testing.T) {
	// 候选早于 PeerConnection 到达时必须暂存并在 SetRemoteDescription
	// 之后补加。丢了就会 ICE 握不上手，表现为「页面说 open、Go 侧一个
	// 字节都读不到」——这是线上真实踩过的坑。
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_early")
	defer p.pc.Close()

	// 先把一条候选塞进暂存表，模拟它比 Offer 先到。
	early := pion.ICECandidateInit{Candidate: "candidate:1 1 udp 2130706431 127.0.0.1 9 typ host"}
	srv.cands["conn-4"] = []pion.ICECandidateInit{early}

	conn := setup(t, srv, sig, p, "conn-4")
	defer conn.Close()

	// 补加之后暂存表应被清空，并登记到 pending。
	srv.mu.Lock()
	left := len(srv.cands["conn-4"])
	srv.mu.Unlock()
	if left != 0 {
		t.Errorf("暂存候选在处理后应清空，还剩 %d 条", left)
	}
}

func TestAcceptOnConnIsAsync(t *testing.T) {
	// OnConn 必须异步调用。同步的话它一旦是死循环（cmd 里的 handle 就是），
	// pion 的 OnDataChannel 回调永不返回，handleOpen 不执行，dc 永远
	// connecting——这正是之前 e2e 红了十几轮的根因。
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_async")
	defer p.pc.Close()

	block := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv.cfg.OnConn = func(c *Conn) {
		entered <- struct{}{}
		<-block // 模拟用户回调长时间不返回
	}

	// 候选回调同样要早于 Accept 挂上。
	p.pc.OnICECandidate(func(c *pion.ICECandidate) {
		if c == nil {
			return
		}
		srv.Offer("conn-5", c.ToJSON())
	})

	offer := p.pc.LocalDescription()
	go srv.Accept(peerjs.Offer{
		Remote:       "peer-test",
		ConnectionID: "conn-5",
		Label:        p.dc.Label(),
		SDP:          offer.SDP,
	}, sig)

	sdp := sig.waitAnswer(t, 10*time.Second)
	if err := p.pc.SetRemoteDescription(pion.SessionDescription{
		Type: pion.SDPTypeAnswer,
		SDP:  sdp,
	}); err != nil {
		t.Fatalf("SetRemoteDescription: %v", err)
	}
	for _, c := range sig.takeCandidates() {
		_ = p.pc.AddICECandidate(c)
	}

	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("OnConn 没被调用")
	}

	// 关键点：OnConn 还卡着，但对端的 DataChannel 必须已经 open——
	// 说明它的执行没有阻塞 pion 的 handleOpen。
	if err := waitOpen(p.dc, 15*time.Second); err != nil {
		t.Fatalf("OnConn 阻塞期间通道应仍能打开: %v", err)
	}
	close(block)
}

func TestServerConnsTracksLifecycle(t *testing.T) {
	srv := newTestServer(Config{})
	sig := newFakeSignal()
	p := newPeer(t, "dc_life")
	defer p.pc.Close()

	conn := setup(t, srv, sig, p, "conn-6")
	if n := srv.Conns(); n != 1 {
		t.Errorf("连接建立后 Conns = %d，want 1", n)
	}

	_ = conn.Close()
	// track 的收尾 goroutine 在 done 关闭后才删，给一点时间。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if srv.Conns() == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("连接关闭后 Conns = %d，want 0", srv.Conns())
}

func TestServerOfferForUnknownConnIsBuffered(t *testing.T) {
	// 完全没见过的 connectionID：候选要进暂存表，不能丢也不能 panic。
	srv := newTestServer(Config{})
	cand := pion.ICECandidateInit{Candidate: "candidate:2 1 udp 1 127.0.0.1 10 typ host"}
	srv.Offer("ghost", cand)

	srv.mu.Lock()
	n := len(srv.cands["ghost"])
	srv.mu.Unlock()
	if n != 1 {
		t.Errorf("暂存 %d 条，want 1", n)
	}
}

func TestAcceptBadOfferDoesNotPanic(t *testing.T) {
	// 畸形 SDP 必须安全失败：不能 panic，也不能留下半截状态。
	srv := newTestServer(Config{})
	sig := newFakeSignal()

	go srv.Accept(peerjs.Offer{
		Remote:       "peer-bad",
		ConnectionID: "conn-bad",
		Label:        "x",
		SDP:          "这不是 SDP",
	}, sig)

	// 不该有 answer 回出去。
	select {
	case sdp := <-sig.answerCh:
		t.Errorf("畸形 offer 不该回 answer，实得 %q", sdp)
	case <-time.After(2 * time.Second):
	}

	// pending 里不应留下东西。
	srv.mu.Lock()
	_, ok := srv.pending["conn-bad"]
	srv.mu.Unlock()
	if ok {
		t.Error("失败的 offer 不应留在 pending 里")
	}
}

// ---- 以下是小工具 ----

// readFrame 从浏览器侧 DataChannel 读一整条消息（自动重组分片）。
func readFrame(t *testing.T, dc *pion.DataChannel, d time.Duration) Frame {
	t.Helper()
	got, err := readFrameErr(dc, d)
	if err != nil {
		t.Fatalf("读帧: %v", err)
	}
	return got
}

func readFrameErr(dc *pion.DataChannel, d time.Duration) (Frame, error) {
	rawCh := make(chan []byte, 16)
	dc.OnMessage(func(m pion.DataChannelMessage) {
		select {
		case rawCh <- m.Data:
		default:
		}
	})

	var r Reassembler
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case raw := <-rawCh:
			h, err := ParseChunk(raw)
			if err != nil {
				return Frame{}, err
			}
			f, err := r.Push(h)
			if errors.Is(err, ErrChunkPending) {
				continue
			}
			if err != nil {
				return Frame{}, err
			}
			return f, nil
		case <-deadline.C:
			return Frame{}, errors.New("读帧超时")
		}
	}
}

// nextFrameOf 在 Conn 上等到指定 kind 的帧，跳过其它（比如 hello）。
func nextFrameOf(t *testing.T, c *Conn, want Kind, d time.Duration) Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	for {
		f, err := c.Next(ctx)
		if err != nil {
			t.Fatalf("等 %s 帧时出错: %v", want, err)
		}
		if f.Kind == want {
			return f
		}
	}
}

// readUntilKindData 收集指定 stream 上所有 KindData 的内容直到结束。
func readUntilKindData(t *testing.T, dc *pion.DataChannel, stream uint32, d time.Duration) []byte {
	t.Helper()
	rawCh := make(chan []byte, 1024)
	dc.OnMessage(func(m pion.DataChannelMessage) {
		select {
		case rawCh <- m.Data:
		default:
		}
	})

	var r Reassembler
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	for {
		select {
		case raw := <-rawCh:
			h, err := ParseChunk(raw)
			if err != nil {
				t.Fatalf("ParseChunk: %v", err)
			}
			f, err := r.Push(h)
			if errors.Is(err, ErrChunkPending) {
				continue
			}
			if err != nil {
				t.Fatalf("Push: %v", err)
			}
			if f.Kind == KindData && f.Stream == stream {
				return f.Data
			}
		case <-deadline.C:
			t.Fatal("等 data 帧超时")
		}
	}
}

func waitOpen(dc *pion.DataChannel, d time.Duration) error {
	opened := make(chan struct{}, 1)
	dc.OnOpen(func() {
		select {
		case opened <- struct{}{}:
		default:
		}
	})
	if dc.ReadyState() == pion.DataChannelStateOpen {
		return nil
	}
	select {
	case <-opened:
		return nil
	case <-time.After(d):
		return errors.New("通道没打开")
	}
}
