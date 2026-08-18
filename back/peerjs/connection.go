package peerjs

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// defaultBufferLowThreshold 发送缓冲低水位阈值。
// SendFrame 内置流控：bufferedAmount 超过阈值时等待低水位事件再发下一块。
// 坑：pion 的 OnBufferedAmountLow 是替换式回调——若每个并发发送方各自注册，
// 只有最后一个注册者能收到事件，其余死等（曾导致并发 serveFile 卡死）。
// 因此回调必须在 attach 时注册一次（全局一份），等待统一走 lowWater 通道。
const defaultBufferLowThreshold = 512 * 1024

// Connection 表示一条 WebRTC DataConnection（主动发起或被动接收）。
// SDP 交换与 ICE 候选收发经由信令服务器完成，数据面为 DataChannel。
//
// 扩展性：数据面依赖 DataChannel 接口（transport.go），不绑定 pion 具体类型；
// 业务帧协议（verb）由上层定义，本类型只提供传输原语。
type Connection struct {
	ID      string // connectionId（信令消息路由键）
	PeerID  string // 远端 peer id
	Label   string
	Offered bool // 本端是否为 offerer

	// M9：dc 在 attach（pion OnDataChannel 回调/本地 CreateDataChannel 后）
	// 无锁写入，而 Open/Send/SendFrame/Close 从任意 goroutine 并发读——
	// 数据竞争（-race 必现，极端下读到 nil 半初始化）。dcMu 保护 dc 的读写；
	// attach 只调用一次，锁开销可忽略。
	dcMu sync.RWMutex
	pc   *webrtc.PeerConnection
	dc   DataChannel
	ice  []webrtc.ICEServer

	sendMu sync.Mutex // 保证 data 头与二进制块连续发送（SendFrame）

	lowWater chan struct{} // 低水位事件广播（attach 时注册一次，容量 1 防堆积）

	peer *Peer

	// handlerMu 保护 onOpen/onMessage/onClose：注册方（业务 goroutine 调
	// OnOpen/OnMessage/OnClose）与触发方（attach 注册的 pion 回调闭包在
	// PC goroutine 读）并发——-race 必现的读写竞争（发现背景：自托管集成
	// 测试 -race 连跑必挂，真实网络下时序慢不易暴露）。回调体直接调用，
	// 闭包内只取快照。
	handlerMu sync.Mutex
	onOpen    func(*Connection)
	onMessage func(Frame)
	onClose   func(*Connection)

	closeOnce sync.Once
	done      chan struct{}
}

// Done 返回连接关闭通知（Close 或对端断开时触发）。
func (c *Connection) Done() <-chan struct{} { return c.done }

// OnOpen 注册连接就绪（DataChannel open）回调。
func (c *Connection) OnOpen(f func(*Connection)) {
	c.handlerMu.Lock()
	c.onOpen = f
	c.handlerMu.Unlock()
}

// OnMessage 注册数据消息回调（Frame：文本/二进制帧）。
func (c *Connection) OnMessage(f func(Frame)) {
	c.handlerMu.Lock()
	c.onMessage = f
	c.handlerMu.Unlock()
}

// OnClose 注册连接关闭回调。
func (c *Connection) OnClose(f func(*Connection)) {
	c.handlerMu.Lock()
	c.onClose = f
	c.handlerMu.Unlock()
}

// Open 返回 DataChannel 是否已就绪。
func (c *Connection) Open() bool {
	c.dcMu.RLock()
	defer c.dcMu.RUnlock()
	return c.dc != nil && c.dc.Open()
}

// Send 发送二进制数据。
// 注意：pion 的 dc.Send([]byte) 发送的是二进制帧（SCTP PPID 53），
// 与文本帧（PPID 51）在接收端可区分——协议依赖此区分「数据块 vs 控制头」。
func (c *Connection) Send(data []byte) error {
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	return dc.Send(data)
}

// SendText 发送文本帧（JSON 控制头专用）。
// 坑：必须用文本帧。若用 Send([]byte) 发送 JSON 头，对端（peerjs 浏览器端
// / 本库对端）会把头误判为二进制数据块而丢弃/错配。
func (c *Connection) SendText(s string) error {
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	return dc.SendText(s)
}

// SendJSON 发送 JSON 文本消息（等价 SendText(json(v))）。
func (c *Connection) SendJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.SendText(string(b))
}

// SendFrame 原子发送「JSON 头 + 二进制体」帧（头与体连续发送），内置写缓冲流控。
// 为什么：接收端按「data 头 → 紧随的二进制块」的状态机路由数据，
// 若多个 goroutine 并发发送时头体交织，数据块会挂到错误的请求上。
// sendMu 保证一帧的头+体原子落线，多请求并发安全；同时作为背压闸门——
// 对端消费慢时所有发送方在此排队（有界等待，连接关闭立即退出）。
// 坑：流控事件依赖 attach 时注册的全局回调（lowWater），不能在此处注册
// （替换式回调会被并发发送方覆盖 → 死等）。
func (c *Connection) SendFrame(header any, body []byte) error {
	hb, err := json.Marshal(header)
	if err != nil {
		return err
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	// M9：持 sendMu 期间读一次 dc 快照，后续全部用局部变量（避免每次取锁）
	c.dcMu.RLock()
	dc := c.dc
	c.dcMu.RUnlock()
	if dc == nil || !dc.Open() {
		return fmt.Errorf("peerjs: connection not open")
	}
	if err := dc.SendText(string(hb)); err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	// 低危 1 修复：流控等待加整体超时上限（30s）——之前只等 done/lowWater，
	// 慢消费者时依赖 ICE disconnected（~30s）兜底。有界但不快，现在显式封顶
	flowWait := time.NewTimer(30 * time.Second)
	defer flowWait.Stop()
	for dc.BufferedAmount() > defaultBufferLowThreshold {
		select {
		case <-c.lowWater:
		case <-c.done: // 连接关闭：立即退出，不悬挂调用方
			return fmt.Errorf("peerjs: connection closed during flow control")
		case <-flowWait.C: // 慢消费者：超时返回错误（上层会断开/重试）
			return fmt.Errorf("peerjs: flow control timeout (slow consumer)")
		}
	}
	return dc.Send(body)
}

// DataChannel 返回底层数据通道（高级用法：流控、关闭等）。
func (c *Connection) DataChannel() DataChannel {
	c.dcMu.RLock()
	defer c.dcMu.RUnlock()
	return c.dc
}

// Close 关闭连接并从信令层注销。
// M9：Close 可能被 pion 内部回调（OnDataChannel 的 OnClose）触发，与 attach
// 写入 dc 并发——读取 dc 需持 dcMu。
func (c *Connection) Close() {
	c.closeOnce.Do(func() {
		if c.pc != nil {
			_ = c.pc.Close()
		}
		c.dcMu.RLock()
		dc := c.dc
		c.dcMu.RUnlock()
		if dc != nil {
			dc.Close()
		}
		c.peer.forgetConnection(c.ID)
		close(c.done)
		c.handlerMu.Lock()
		h := c.onClose
		c.handlerMu.Unlock()
		if h != nil {
			h(c)
		}
	})
}

// handleMessage 处理该连接相关的信令消息（ANSWER/CANDIDATE）。
// SDP/候选的解析错误被静默忽略：对端可能发来乱序/过期的候选，
// 失败仅意味着本轮协商失败，由 ICE 状态回调负责最终清理。
func (c *Connection) handleMessage(m Message) {
	switch m.Type {
	case MsgAnswer:
		var payload AnswerPayload
		if err := json.Unmarshal(m.Payload, &payload); err != nil || payload.SDP == nil {
			return
		}
		_ = c.pc.SetRemoteDescription(*payload.SDP)
	case MsgCandidate:
		var payload CandidatePayload
		if err := json.Unmarshal(m.Payload, &payload); err != nil {
			return
		}
		_ = c.pc.AddICECandidate(payload.Candidate)
	}
}

// newConnection 创建连接（offerer 时立即建立 PC 与 DataChannel）。
// connID 为空时生成新 ID；answerer 必须沿用 offerer 提供的 connectionId。
// 坑：曾因 answerer 新生成 ID 导致 ANSWER 消息在信令路由（按 connectionId）
// 时找不到对端 conn，ICE 永远停在 checking，DataChannel 永不 open——
// peerjs 协议规定 connectionId 由 offerer 定义、双方共用。
func (p *Peer) newConnection(dst, label string, offered bool, iceServers []webrtc.ICEServer, connID string) (*Connection, error) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: iceServers})
	if err != nil {
		return nil, fmt.Errorf("peerjs: new pc: %w", err)
	}
	if connID == "" {
		connID = randHex(16)
	}
	conn := &Connection{
		ID:       connID,
		PeerID:   dst,
		Label:    label,
		Offered:  offered,
		pc:       pc,
		peer:     p,
		done:     make(chan struct{}),
		lowWater: make(chan struct{}, 1),
	}
	// ICE 失败/断开即清理连接，避免泄漏（peerjs-client negotiator 同款行为）
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		switch state {
		case webrtc.ICEConnectionStateClosed,
			webrtc.ICEConnectionStateFailed,
			webrtc.ICEConnectionStateDisconnected:
			conn.Close()
		}
	})
	// 收集本地 ICE 候选并经信令转发（peerjs negotiator.onicecandidate 等价实现）。
	// 坑：pion 不会自动发送候选，必须手动 OnICECandidate + 信令 CANDIDATE，
	// 否则双方停在 checking 永远连不上。
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		payload := CandidatePayload{
			Candidate:    c.ToJSON(),
			Type:         ConnData,
			ConnectionID: conn.ID,
		}
		_ = p.Send(NewMessage(MsgCandidate, dst, payload))
	})
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		conn.attach(newPionChannel(dc))
	})

	p.registerConnection(conn)

	if offered {
		ordered := true
		dc, err := pc.CreateDataChannel(label, &webrtc.DataChannelInit{Ordered: &ordered})
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("peerjs: create dc: %w", err)
		}
		conn.attach(newPionChannel(dc))
		if err := conn.makeOffer(); err != nil {
			conn.Close()
			return nil, err
		}
	}
	return conn, nil
}

// attach 绑定数据通道并设置 open/消息/流控回调。
// offerer 侧在 CreateDataChannel 后立即调用（状态为 connecting），
// answerer 侧在对端 offer 触发 pc.OnDataChannel 时调用。
func (c *Connection) attach(dc DataChannel) {
	// M9：dc 写入持锁（Close/Send/Open 并发读；pion 的 OnDataChannel 回调
	// 在 PC goroutine，可能刚好与首次 Send 竞争）。
	c.dcMu.Lock()
	c.dc = dc
	c.dcMu.Unlock()
	// 流控回调全局注册一次（替换式回调，多个发送方各自注册会互相覆盖→死等）
	dc.SetBufferedAmountLowThreshold(defaultBufferLowThreshold)
	dc.OnBufferedAmountLow(func() {
		select {
		case c.lowWater <- struct{}{}:
		default:
		}
	})
	dc.OnOpen(func() {
		// 快照后回调：onOpen 可能晚于 open 事件注册（attach 时仍为 nil），
		// 只读快照避免与 OnOpen 注册并发写（handlerMu 保护）。
		c.handlerMu.Lock()
		h := c.onOpen
		c.handlerMu.Unlock()
		if h != nil {
			h(c)
		}
	})
	dc.OnMessage(func(f Frame) {
		c.handlerMu.Lock()
		h := c.onMessage
		c.handlerMu.Unlock()
		if h != nil {
			h(f)
		}
	})
	// 远端主动关闭 dc 时清理本端（Close 幂等）：否则本端连接悬挂，
	// 依赖 ICE disconnected 兜底（秒级~分钟级，太慢）。
	dc.OnClose(func() {
		c.Close()
	})
}

// makeOffer 创建并发送 OFFER（peerjs negotiator._makeOffer 的等价实现）。
func (c *Connection) makeOffer() error {
	offer, err := c.pc.CreateOffer(nil)
	if err != nil {
		return fmt.Errorf("peerjs: create offer: %w", err)
	}
	if err := c.pc.SetLocalDescription(offer); err != nil {
		return fmt.Errorf("peerjs: set local offer: %w", err)
	}
	payload := OfferPayload{
		SDP:           &offer,
		Type:          ConnData,
		ConnectionID:  c.ID,
		Label:         c.Label,
		Reliable:      true,
		Serialization: "raw",
	}
	return c.peer.Send(NewMessage(MsgOffer, c.PeerID, payload))
}

// handleOffer 对端发起 OFFER：设置远端 SDP 并回 ANSWER。
func (c *Connection) handleOffer(sdp *webrtc.SessionDescription) error {
	if err := c.pc.SetRemoteDescription(*sdp); err != nil {
		return fmt.Errorf("peerjs: set remote offer: %w", err)
	}
	answer, err := c.pc.CreateAnswer(nil)
	if err != nil {
		return fmt.Errorf("peerjs: create answer: %w", err)
	}
	if err := c.pc.SetLocalDescription(answer); err != nil {
		return fmt.Errorf("peerjs: set local answer: %w", err)
	}
	payload := AnswerPayload{SDP: &answer, Type: ConnData, ConnectionID: c.ID}
	return c.peer.Send(NewMessage(MsgAnswer, c.PeerID, payload))
}
