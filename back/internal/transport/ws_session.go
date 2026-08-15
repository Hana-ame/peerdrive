package transport

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// nowPlus 写超时时间点。
func nowPlus(sec int) time.Time { return time.Now().Add(time.Duration(sec) * time.Second) }

// Session 会话抽象：一条承载帧协议的通道。
// 两种实现语义完全一致（同一 reqId 状态机 / 帧协议复用）：
//   - *peerjs.Connection：WebRTC DataChannel（远端节点 / 浏览器经公共云信令直连）
//   - *WSSession：本地 WebSocket（浏览器直连本节点，无打洞/信令开销）
//
// 帧协议见 doc/REFACTOR.md 第 4 节：文本帧=控制头（JSON），二进制帧=数据块，
// SendFrame 保证头+体原子连续。
type Session interface {
	ID() string
	SendJSON(v any) error
	SendFrame(header any, body []byte) error
	OnMessage(f func(peerjs.Frame))
	OnClose(f func())
	Close()
}

// WSSession 把本地 WebSocket 适配为 Session（帧协议与 DataChannel 完全一致）。
// 语义复用：浏览器端同一套 req/meta/data/done/err 帧，本地走 WS、远端走
// WebRTC DataChannel——前端只需一套协议编解码。
type WSSession struct {
	id   string
	conn *websocket.Conn

	sendMu    sync.Mutex // gorilla 不允许并发写
	onMessage func(peerjs.Frame)
	onClose   func()
	closeOnce sync.Once
}

// NewWSSession 包装已升级的 WebSocket 连接，并启动读循环与保活。
// M5 修复：
//   - SetReadLimit：之前无读限制，恶意/故障浏览器发超大帧无限占内存
//   - ping/pong 保活：之前无 ReadDeadline，浏览器标签页死掉 → readLoop
//     goroutine + 会话常驻，连接 map 永不清理，pending fetch 挂 5 分钟
func NewWSSession(id string, conn *websocket.Conn) *WSSession {
	// 数据块 ≤64KB + JSON 控制头余量（两倍留余）
	conn.SetReadLimit(3 * 64 * 1024)
	conn.SetReadDeadline(nowPlus(90))
	conn.SetPongHandler(func(string) error {
		// 收到 pong 刷新读超时（浏览器对 ping 自动回 pong，协议层行为）
		conn.SetReadDeadline(nowPlus(90))
		return nil
	})
	s := &WSSession{id: id, conn: conn}
	go s.readLoop()
	go s.heartbeatLoop()
	return s
}

// heartbeatLoop 周期性 ping 保活：死连接 90s 内无 pong → 读超时 →
// ReadMessage 报错 → readLoop 退出 → Close 清理会话。ping 失败（连接已关）
// 直接退出，无泄漏。
func (s *WSSession) heartbeatLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		s.sendMu.Lock()
		err := s.conn.WriteControl(websocket.PingMessage, nil, nowPlus(10))
		s.sendMu.Unlock()
		if err != nil {
			return
		}
	}
}

// ID 返回会话标识（本地会话为 "local"）。
func (s *WSSession) ID() string { return s.id }

// SendJSON 发送文本帧（JSON 控制头）。
func (s *WSSession) SendJSON(v any) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.conn.SetWriteDeadline(nowPlus(15))
	return s.conn.WriteJSON(v)
}

// SendFrame 原子发送「JSON 头 + 二进制体」帧（同 DataChannel 约束）。
func (s *WSSession) SendFrame(header any, body []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	s.conn.SetWriteDeadline(nowPlus(15))
	if err := s.conn.WriteJSON(header); err != nil {
		return err
	}
	if len(body) > 0 {
		s.conn.SetWriteDeadline(nowPlus(15))
		return s.conn.WriteMessage(websocket.BinaryMessage, body)
	}
	return nil
}

// OnMessage 注册帧回调（文本/二进制帧区分与 DataChannel 一致）。
func (s *WSSession) OnMessage(f func(peerjs.Frame)) {
	s.sendMu.Lock()
	s.onMessage = f
	s.sendMu.Unlock()
}

// OnClose 注册关闭回调。
func (s *WSSession) OnClose(f func()) {
	s.sendMu.Lock()
	s.onClose = f
	s.sendMu.Unlock()
}

// Close 关闭会话。
func (s *WSSession) Close() {
	s.closeOnce.Do(func() {
		_ = s.conn.Close()
		s.sendMu.Lock()
		f := s.onClose
		s.onClose = nil
		s.sendMu.Unlock()
		if f != nil {
			f()
		}
	})
}

// readLoop 读取帧并分发（文本→IsText=true，二进制→IsText=false）。
func (s *WSSession) readLoop() {
	defer s.Close()
	for {
		mt, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		s.sendMu.Lock()
		f := s.onMessage
		s.sendMu.Unlock()
		if f != nil {
			f(peerjs.Frame{IsText: mt == websocket.TextMessage, Data: data})
		}
	}
}
