package webrtc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hana-ame/peerdrive/peerjs"
	"github.com/pion/logging"
	pion "github.com/pion/webrtc/v4"
)

// ErrClosed 表示连接或服务已关闭。
var ErrClosed = errors.New("webrtc: 已关闭")

// Config 是服务端配置。
type Config struct {
	// PeerJS 透传给 peerjs.Dial；零值即 0.peerjs.com。
	PeerJS peerjs.Config
	// ICEServers 默认与 peerjs 一致（STUN + PeerJS 公共 TURN）。
	ICEServers []pion.ICEServer
	// Logger 默认为 stderr。
	Logger *log.Logger
	// OnConn 在一条 DataChannel 就绪时触发。
	OnConn func(*Conn)
	// PionLog 让 pion 自己的日志（DTLS / SCTP / datachannel 状态机）落到
	// 同一个 logger 上。默认关闭；排查握手问题时置 true 很值。
	PionLog bool
}

// Server 是一个等浏览器连进来的接收端。
//
// 它只借 peerjs 的信令：wss 收 OFFER → 自己建 PeerConnection → 回 ANSWER。
// 不监听任何本地端口。
type Server struct {
	cfg  Config
	log  *log.Logger
	sig  *peerjs.Client
	mu   sync.Mutex
	live map[*Conn]struct{}
	// pending：connectionID → 已建好的 PeerConnection（等 DataChannel）。
	pending map[string]*pion.PeerConnection
	// cands：候选早于 PeerConnection 到达时的暂存。
	cands map[string][]pion.ICECandidateInit
	// loggerFactory 非空时，pion 内部日志也走同一个 writer。
	loggerFactory *logging.DefaultLoggerFactory
	stop          sync.Once
	done          chan struct{}
}

// Listen 注册到 PeerJS 信令并开始接收连接。返回后即可从 ID() 取到标识符。
func Listen(ctx context.Context, cfg Config) (*Server, error) {
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "", log.LstdFlags|log.Lmicroseconds)
	}
	if cfg.ICEServers == nil {
		cfg.ICEServers = peerjs.NewICEServers()
	}
	if cfg.PeerJS.Logger == nil {
		cfg.PeerJS.Logger = cfg.Logger
	}

	sig, err := peerjs.Dial(ctx, cfg.PeerJS)
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:     cfg,
		log:     cfg.Logger,
		sig:     sig,
		live:    make(map[*Conn]struct{}),
		pending: make(map[string]*pion.PeerConnection),
		cands:   make(map[string][]pion.ICECandidateInit),
		done:    make(chan struct{}),
	}
	if cfg.PionLog {
		f := logging.NewDefaultLoggerFactory()
		f.Writer = cfg.Logger.Writer()
		// DEBUG 会被 mdns / turnc / sctp 刷屏，把真正关心的状态迁移冲掉。
		// 但只放 ERROR 又太低：SCTP/DCEP 握手不上时，pion 往往只在 WARN
		// 上说话，DC 就那样静悄悄停在 connecting。默认 WARN，需要时用
		// PION_LOG_LEVEL 覆盖（error / warn / info / debug）。
		f.DefaultLogLevel = logging.LogLevelWarn
		switch os.Getenv("PION_LOG_LEVEL") {
		case "error":
			f.DefaultLogLevel = logging.LogLevelError
		case "info":
			f.DefaultLogLevel = logging.LogLevelInfo
		case "debug":
			f.DefaultLogLevel = logging.LogLevelDebug
		}
		s.loggerFactory = f
	}
	sig.RegisterTransport(s)
	return s, nil
}

// ID 返回本端在 PeerJS 上的标识符（就是打在日志里的那条串）。
func (s *Server) ID() string { return s.sig.ID() }

// newPC 建一条 PeerConnection。开启 PionLog 时挂上自定义 SettingEngine，
// 让 pion 的 SCTP/DTLS/datachannel 日志也进 webrtc.log。
func (s *Server) newPC() (*pion.PeerConnection, error) {
	cfg := pion.Configuration{ICEServers: s.cfg.ICEServers}
	if s.loggerFactory == nil {
		return pion.NewPeerConnection(cfg)
	}
	se := pion.SettingEngine{}
	se.LoggerFactory = s.loggerFactory
	api := pion.NewAPI(pion.WithSettingEngine(se))
	return api.NewPeerConnection(cfg)
}

// Accept 实现 peerjs.Transport。
func (s *Server) Accept(o peerjs.Offer, sig peerjs.Signal) {
	pc, err := s.newPC()
	if err != nil {
		s.log.Printf("创建 PeerConnection 失败: %v", err)
		return
	}

	conn := &Conn{
		pc:     pc,
		remote: o.Remote,
		id:     o.ConnectionID,
		log:    s.log,
		done:   make(chan struct{}),
		onMsg:  make(chan Frame, 64),
	}
	conn.role = RoleServer

	pc.OnICECandidate(func(cand *pion.ICECandidate) {
		if cand == nil {
			return
		}
		init := cand.ToJSON()
		if err := sig.SendCandidate(o.Remote, o.ConnectionID, init); err != nil {
			s.log.Printf("回 CANDIDATE 失败: %v", err)
		}
	})

	pc.OnDataChannel(func(dc *pion.DataChannel) {
		s.log.Printf("收到 DataChannel · %s · label=%s", o.ConnectionID, dc.Label())
		conn.attach(dc)
		s.track(conn)
		if s.cfg.OnConn != nil {
			s.cfg.OnConn(conn)
		}
	})

	pc.OnConnectionStateChange(func(st pion.PeerConnectionState) {
		s.log.Printf("连接状态 · %s · %s", o.ConnectionID, st)
		switch st {
		case pion.PeerConnectionStateFailed, pion.PeerConnectionStateClosed:
			conn.fail(ErrClosed)
			sig.Release(o.ConnectionID)
		}
	})

	pc.OnICEConnectionStateChange(func(st pion.ICEConnectionState) {
		s.log.Printf("ICE 状态 · %s · %s", o.ConnectionID, st)
	})

	if err := pc.SetRemoteDescription(pion.SessionDescription{
		Type: pion.SDPTypeOffer,
		SDP:  o.SDP,
	}); err != nil {
		s.log.Printf("SetRemoteDescription 失败: %v", err)
		_ = pc.Close()
		return
	}
	// SCTP 段是 DataChannel 的前提。ICE 能通但 dc 永远 connecting 时，
	// 最可能的就是 offer 里 m=application 那几行没被正确解析（sctp-port、
	// max-message-size、fmtp）。把原始行打出来，一眼能看出是缺了还是畸形。
	s.log.Printf("offer 的 SCTP 段 · %s:", o.ConnectionID)
	for _, line := range strings.Split(o.SDP, "\r\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "m=application") || strings.HasPrefix(t, "a=sctp") ||
			strings.HasPrefix(t, "a=max-message-size") || strings.HasPrefix(t, "a=fmtp") ||
			strings.HasPrefix(t, "a=group") || t == "m=application" {
			s.log.Printf("   %s", t)
		}
	}
	// remote description 就位后补加之前暂存的候选。
	s.flushCandidates(o.ConnectionID, pc)

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		s.log.Printf("CreateAnswer 失败: %v", err)
		_ = pc.Close()
		return
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		s.log.Printf("SetLocalDescription 失败: %v", err)
		_ = pc.Close()
		return
	}

	if err := sig.SendAnswer(o.Remote, o.ConnectionID, pc.LocalDescription().SDP); err != nil {
		s.log.Printf("回 ANSWER 失败: %v", err)
		_ = pc.Close()
		return
	}
	s.log.Printf("已应答 offer · %s · label=%s · ser=%s", o.Remote, o.Label, o.Serialization)
	s.diag(o.ConnectionID, "已应答 offer，等候选/通道")
}

// diag 周期性把一条连接的内部状态打到日志里。
//
// ICE 握不上手时，外部能看到的只有「页面说 open、Go 侧没反应」。这里每
// 2s 打一次 signaling/ice/gathering 状态与已收候选数，直到通道打开，
// 失败时日志尾部就能直接看出卡在 ice 还是 gathering。
func (s *Server) diag(connectionID string, phase string) {
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for i := 0; i < 20; i++ {
			select {
			case <-t.C:
			case <-s.done:
				return
			}
			s.mu.Lock()
			pc := s.pending[connectionID]
			var dc *pion.DataChannel
			for c := range s.live {
				if c.id == connectionID {
					pc, dc = c.pc, c.dc
					break
				}
			}
			s.mu.Unlock()
			if pc == nil {
				continue
			}
			// 以前这里只要「已 track 且 conn connected」就直接 return，
			// 结果最需要看的 dc.ReadyState() 一次都没打出来过——日志里
			// 只剩 OnDataChannel 和「接入」，看不出通道究竟开没开。
			s.log.Printf("诊断 · %s · %s · sig=%s ice=%s gather=%s conn=%s",
				phase, connectionID,
				pc.SignalingState(), pc.ICEConnectionState(),
				pc.ICEGatheringState(), pc.ConnectionState())
			if dc == nil {
				s.log.Printf("   dc=<nil>（OnDataChannel 还没回）")
				continue
			}
			// pion 的 dc.Send 只认这个 ReadyState，不是 open 就立刻返回
			// io.ErrClosedPipe，不会等。它不是 open 就是发不出去。
			s.log.Printf("   dc=%s id=%v", dc.ReadyState(), dc.ID())
			if dc.ReadyState() == pion.DataChannelStateOpen {
				return
			}
		}
	}()
}

// Offer 实现 peerjs.Transport：收到 ICE candidate。
//
// 候选可能早于 DataChannel 到达：此时 live 里还没有这条连接，按 id 直接
// 找不到目标。以前这种候选被静默丢弃，浏览器侧的 ICE 就永远握不上手
// ——表现为页面显示 datachannel open、但 Go 侧一个字节都读不到。
// 现在先把候选按 connectionID 暂存，等 Accept 里建好 PeerConnection 再补加。
func (s *Server) Offer(connectionID string, cand pion.ICECandidateInit) {
	s.mu.Lock()
	var target *Conn
	for c := range s.live {
		if c.id == connectionID {
			target = c
			break
		}
	}
	pc := s.pending[connectionID]
	if target == nil && pc == nil {
		// 两者都没有：连 Accept 都还没跑到，先存着。
		s.cands[connectionID] = append(s.cands[connectionID], cand)
		s.mu.Unlock()
		s.log.Printf("候选提前到达，暂存 · conn=%s（已有 %d 条）", connectionID, len(s.cands[connectionID]))
		return
	}
	s.mu.Unlock()

	if target != nil {
		pc = target.pc
	}
	if err := pc.AddICECandidate(cand); err != nil {
		s.log.Printf("AddICECandidate · conn=%s: %v", connectionID, err)
		return
	}
	s.log.Printf("候选已加入 · conn=%s", connectionID)
}

// flushCandidates 把暂存的候选补加到刚建好的 PeerConnection 上。
func (s *Server) flushCandidates(connectionID string, pc *pion.PeerConnection) {
	s.mu.Lock()
	pending := s.cands[connectionID]
	delete(s.cands, connectionID)
	s.pending[connectionID] = pc
	s.mu.Unlock()
	if len(pending) == 0 {
		return
	}
	s.log.Printf("补加暂存候选 · conn=%s · %d 条", connectionID, len(pending))
	for _, c := range pending {
		if err := pc.AddICECandidate(c); err != nil {
			s.log.Printf("补加候选失败 · conn=%s: %v", connectionID, err)
		}
	}
}

func (s *Server) track(c *Conn) {
	s.mu.Lock()
	s.live[c] = struct{}{}
	delete(s.pending, c.id)
	s.mu.Unlock()
	go func() {
		<-c.done
		s.mu.Lock()
		delete(s.live, c)
		delete(s.pending, c.id)
		delete(s.cands, c.id)
		s.mu.Unlock()
	}()
}

// Conns 返回当前活跃连接数。
func (s *Server) Conns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.live)
}

// Close 关闭所有连接与信令。
func (s *Server) Close() error {
	s.stop.Do(func() {
		close(s.done)
		s.mu.Lock()
		for c := range s.live {
			_ = c.Close()
		}
		s.live = map[*Conn]struct{}{}
		s.pending = map[string]*pion.PeerConnection{}
		s.cands = map[string][]pion.ICECandidateInit{}
		s.mu.Unlock()
	})
	return s.sig.Close()
}

// Role 表示本端在会话中的角色。
type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
)

// Conn 是一条已建立（或正在建立）的 DataChannel 封装。
//
// 收：读完一帧就交给 Frame()/Messages()。
// 发：Send* 系列自动完成分片。
type Conn struct {
	pc     *pion.PeerConnection
	dc     *pion.DataChannel
	remote string
	id     string
	role   Role
	log    *log.Logger

	streamGen uint32
	reasm     Reassembler

	onMsg  chan Frame
	closed atomic.Bool
	done   chan struct{}
	once   sync.Once
	err    error
	mu     sync.Mutex
}

func (c *Conn) attach(dc *pion.DataChannel) {
	c.mu.Lock()
	c.dc = dc
	c.mu.Unlock()

	// 记下挂回调那一刻的状态。pion 的 handleOpen 顺序是
	//   setReadyState(Open) → onOpen() → go readLoop()
	// 所以如果这里打印的不是 connecting，说明 handleOpen 已经跑过、
	// onOpen 已经错过了我们（handler 当时还是 nil）。这一行是判断
	// 「回调挂晚了」还是「回调挂了但没被触发」的唯一直接证据。
	c.log.Printf("   attach · %s · 注册时 dc.ReadyState=%s", c.remote, dc.ReadyState())

	// 这里只注册回调，不做任何阻塞操作。
	//
	// 教训：曾经在这个位置直接 sendHello()，而 pion 的 dc.Send 在通道
	// 还没 open 时是「立刻返回 io.ErrClosedPipe」，我们的重试循环一跑
	// 就是 2s。attach 是在 pion 的 OnDataChannel 回调里同步执行的，这一
	// 卡直接让后面的 OnOpen / OnMessage 晚了 2s 才挂上，SCTP 读循环也跟
	// 着停摆——表现就是「页面显示 open，go 侧一个字节都读不到」。
	dc.OnOpen(func() {
		c.log.Printf("DataChannel 就绪 · %s · %s", c.remote, dc.ReadyState())
		c.sendHello()
	})

	dc.OnClose(func() {
		c.log.Printf("DataChannel 关闭 · %s", c.remote)
		c.fail(ErrClosed)
	})

	// 通道已经是 open 状态时 OnOpen 会被立即回调（pion 内部判断
	// ReadyState == open 就 go handler），所以不需要再手动补一次。

	dc.OnMessage(func(m pion.DataChannelMessage) {
		// 进来就留痕。之前这里第一句是 ParseChunk，解析失败只打「坏帧」
		// 或干脆 return，于是「读循环到底有没有收到字节」和「收到了但
		// 解不开」在日志上完全一样，没法区分。
		c.log.Printf("   ← %d 字节 · %s", len(m.Data), hexHead(m.Data, 6))
		h, err := ParseChunk(m.Data)
		if err != nil {
			c.log.Printf("坏帧 · %s: %v", c.remote, err)
			return
		}
		f, err := c.reasm.Push(h)
		if errors.Is(err, ErrChunkPending) {
			return
		}
		if err != nil {
			c.log.Printf("重组失败 · %s: %v", c.remote, err)
			return
		}
		select {
		case c.onMsg <- f:
		case <-c.done:
			// 连接已结束，onMsg 已关闭，不能再往里写。
		default:
			// 消费不过来时丢弃并留痕，避免阻塞 DataChannel 的读循环。
			c.log.Printf("接收队列已满，丢弃 %s stream=%d len=%d", f.Kind, f.Stream, len(f.Data))
		}
	})
}

// sendHello 声明角色与协议版本，对端据此判断能不能用扩展帧。
//
// 只在 OnOpen 里调用：那时 pion 的 ReadyState 已经是 open，dc.Send 不会
// 返回 io.ErrClosedPipe，也就不需要再套一层重试。
func (c *Conn) sendHello() {
	if err := c.SendJSON(KindHello, 0, map[string]any{
		"role":    string(c.role),
		"version": 1,
		"proto":   "peerdrive/webrtc",
	}); err != nil {
		c.log.Printf("发 hello 失败 · %s: %v", c.remote, err)
	}
}

// Remote 返回对端 PeerJS id。
func (c *Conn) Remote() string { return c.remote }

// ID 返回 PeerJS 的 connectionId。
func (c *Conn) ID() string { return c.id }

// Messages 返回接收通道。通道本身不会被关闭，配合 Done() 判断结束。
func (c *Conn) Messages() <-chan Frame { return c.onMsg }

// Done 在连接关闭时关闭。
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err 返回连接结束的原因。
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Next 阻塞取一帧。连接结束或 ctx 取消时返回错误。
func (c *Conn) Next(ctx context.Context) (Frame, error) {
	select {
	case <-c.done:
		return Frame{}, ErrClosed
	default:
	}
	select {
	case f := <-c.onMsg:
		return f, nil
	case <-c.done:
		return Frame{}, ErrClosed
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	}
}

func (c *Conn) nextStream() uint32 {
	return atomic.AddUint32(&c.streamGen, 1)
}

// Send 发送一帧，自动分片。
func (c *Conn) Send(f Frame) error {
	if c.closed.Load() {
		return ErrClosed
	}
	c.mu.Lock()
	dc := c.dc
	c.mu.Unlock()
	if dc == nil {
		return errors.New("webrtc: DataChannel 尚未就绪")
	}
	frames, err := EncodeFrame(f)
	if err != nil {
		return err
	}
	for _, b := range frames {
		if err := dc.Send(b); err != nil {
			return fmt.Errorf("webrtc: 发送: %w", err)
		}
	}
	return nil
}

// SendJSON 把一个 Go 值编成 JSON 后按给定 Kind 发出，返回所用的 stream id。
func (c *Conn) SendJSON(kind Kind, stream uint32, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if stream == 0 {
		stream = c.nextStream()
	}
	return c.Send(Frame{Kind: kind, Stream: stream, Data: b})
}

// SendBytes 发送一段二进制（KindData）。
func (c *Conn) SendBytes(stream uint32, b []byte) error {
	if stream == 0 {
		stream = c.nextStream()
	}
	return c.Send(Frame{Kind: KindData, Stream: stream, Data: b})
}

// SendControl 发送一条控制信号。
func (c *Conn) SendControl(stream uint32, action string, extra map[string]any) error {
	body := map[string]any{"action": action, "at": time.Now().UnixMilli()}
	for k, v := range extra {
		body[k] = v
	}
	return c.SendJSON(KindControl, stream, body)
}

func (c *Conn) fail(err error) {
	c.once.Do(func() {
		c.closed.Store(true)
		c.mu.Lock()
		c.err = err
		c.mu.Unlock()
		// 不关闭 onMsg：DataChannel 的 OnMessage 可能正处在一个 send 上，
		// 关掉会 panic。结束语义统一用 done 表达，Next 里两个都监听。
		close(c.done)
	})
}

// Close 关闭连接。
func (c *Conn) Close() error {
	c.fail(ErrClosed)
	c.mu.Lock()
	pc := c.pc
	c.mu.Unlock()
	if pc != nil {
		return pc.Close()
	}
	return nil
}

// hexHead 取前 n 个字节的十六进制，用于日志里辨认帧类型。
//
// 排查「页面发了、Go 侧收不到」这类问题时，光有长度不够——首字节是
// Kind，第 6 字节是分片标志，看到这两处就能判断是没收到还是解错了。
func hexHead(b []byte, n int) string {
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return "(空)"
	}
	return hex.EncodeToString(b[:n])
}
