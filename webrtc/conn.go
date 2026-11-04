package webrtc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hana-ame/peerdrive/peerjs"
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
	stop sync.Once
	done chan struct{}
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
		cfg:  cfg,
		log:  cfg.Logger,
		sig:  sig,
		live: make(map[*Conn]struct{}),
		done: make(chan struct{}),
	}
	sig.RegisterTransport(s)
	return s, nil
}

// ID 返回本端在 PeerJS 上的标识符（就是打在日志里的那条串）。
func (s *Server) ID() string { return s.sig.ID() }

// Accept 实现 peerjs.Transport。
func (s *Server) Accept(o peerjs.Offer, sig peerjs.Signal) {
	pc, err := pion.NewPeerConnection(pion.Configuration{ICEServers: s.cfg.ICEServers})
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
		conn.attach(dc)
		s.track(conn)
		if s.cfg.OnConn != nil {
			s.cfg.OnConn(conn)
		}
	})

	pc.OnConnectionStateChange(func(st pion.PeerConnectionState) {
		switch st {
		case pion.PeerConnectionStateFailed, pion.PeerConnectionStateClosed:
			conn.fail(ErrClosed)
			sig.Release(o.ConnectionID)
		}
	})

	if err := pc.SetRemoteDescription(pion.SessionDescription{
		Type: pion.SDPTypeOffer,
		SDP:  o.SDP,
	}); err != nil {
		s.log.Printf("SetRemoteDescription 失败: %v", err)
		_ = pc.Close()
		return
	}

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
}

// Offer 实现 peerjs.Transport：收到 ICE candidate。
func (s *Server) Offer(connectionID string, cand pion.ICECandidateInit) {
	s.mu.Lock()
	var target *Conn
	for c := range s.live {
		if c.id == connectionID {
			target = c
			break
		}
	}
	s.mu.Unlock()
	if target == nil {
		return
	}
	if err := target.pc.AddICECandidate(cand); err != nil {
		s.log.Printf("AddICECandidate: %v", err)
	}
}

func (s *Server) track(c *Conn) {
	s.mu.Lock()
	s.live[c] = struct{}{}
	s.mu.Unlock()
	go func() {
		<-c.done
		s.mu.Lock()
		delete(s.live, c)
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

	onMsg chan Frame
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

	dc.OnOpen(func() {
		c.log.Printf("DataChannel 就绪 · %s", c.remote)
		// 握手：声明角色与协议版本，对端据此判断能不能用扩展帧。
		_ = c.SendJSON(KindHello, 0, map[string]any{
			"role":    string(c.role),
			"version": 1,
			"proto":   "peerdrive/webrtc",
		})
	})

	dc.OnMessage(func(m pion.DataChannelMessage) {
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

	dc.OnClose(func() {
		c.log.Printf("DataChannel 关闭 · %s", c.remote)
		c.fail(ErrClosed)
	})
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
