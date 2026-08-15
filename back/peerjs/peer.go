package peerjs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
)

// version 仿照 peerjs-client 的 version 查询参数。
const version = "1.5.4"

// ConnectionHandler 接收新建的 WebRTC 数据连接（被动接收时回调）。
type ConnectionHandler func(c *Connection)

// Peer 是 PeerJS 信令客户端：注册到信令服务器，收发 OFFER/ANSWER/CANDIDATE
// 消息，支持主动发起连接（Connect）与被动接收（OnConnection）。
// 数据面由 Connection 封装，业务层处理 Frame 消息。
//
// 扩展性：
//   - 换信令：NewPeerWithSignaller 注入自定义 Signaller
//   - 换传输：Connection 只依赖 DataChannel 接口（见 transport.go）
//   - 加消息：MessageType 为开放 string 类型，自定义类型直接发送
type Peer struct {
	opts       Options
	signaller  Signaller
	onConn     ConnectionHandler
	iceServers []webrtc.ICEServer

	mu     sync.Mutex
	conns  map[string]*Connection
	closed chan struct{}
}

// DefaultOptions 返回公共云信令（0.peerjs.com）默认配置。
func DefaultOptions() Options {
	return Options{
		Host:         "0.peerjs.com",
		Port:         "443",
		Secure:       true,
		Path:         "/",
		Key:          "peerjs",
		PingInterval: 5 * time.Second,
	}
}

// NewPeer 创建 PeerJS 信令客户端。
// id 为空时服务端分配随机 ID；token 为空时随机生成。
func NewPeer(id string, opts Options) *Peer {
	opts = normalizeOptions(opts)
	p := &Peer{
		opts:   opts,
		conns:  make(map[string]*Connection),
		closed: make(chan struct{}),
	}
	p.iceServers = opts.ICEServers // Options 注入 ICE 服务器（SetICEServers 已并入 Options）
	p.signaller = newPeerJSSignaller(id, opts, p.route)
	return p
}

// NewPeerWithSignaller 使用自定义信令创建 Peer（扩展点）。
func NewPeerWithSignaller(s Signaller) *Peer {
	p := &Peer{
		conns:  make(map[string]*Connection),
		closed: make(chan struct{}),
	}
	p.signaller = s
	s.OnMessage(p.route)
	return p
}

// OnConnection 注册被动连接回调（对端发起 OFFER 时调用）。
func (p *Peer) OnConnection(h ConnectionHandler) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onConn = h
}

// SetICEServers 设置 ICE 服务器（STUN/TURN）。
// 注意：NewPeer 走 Options.ICEServers；NewPeerWithSignaller（自定义信令）
// 必须调用本方法，否则 WebRTC 只有局域网 host 候选。
func (p *Peer) SetICEServers(servers []webrtc.ICEServer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.iceServers = servers
}

// ID 返回 peer 的标识（Dial 成功后有效）。
func (p *Peer) ID() string { return p.signaller.ID() }

// Connected 返回信令是否已连接。
func (p *Peer) Connected() bool { return p.signaller != nil && p.signaller.ID() != "" }

// Dial 注册并连接信令服务器。ctx 取消中止连接。
func (p *Peer) Dial(ctx context.Context) error {
	return p.signaller.Dial(ctx)
}

// Done 返回信令断线通知（透传 Signaller.Done；H7 重连循环依赖）。
func (p *Peer) Done() <-chan struct{} { return p.signaller.Done() }

// Send 发送信令消息（扩展点：自定义消息类型）。
func (p *Peer) Send(m Message) error { return p.signaller.Send(m) }

// Connect 主动发起与远端 peer 的 DataConnection（offerer 角色）。
// 连接就绪通过 conn.OnOpen 通知；ctx 用于取消协商。
func (p *Peer) Connect(ctx context.Context, dst, label string) (*Connection, error) {
	if dst == "" {
		return nil, fmt.Errorf("peerjs: empty remote id")
	}
	return p.newConnection(dst, label, true, p.iceServers, "")
}

// Close 关闭信令连接并清理所有 WebRTC 连接。
func (p *Peer) Close() {
	p.mu.Lock()
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
	conns := make([]*Connection, 0, len(p.conns))
	for _, c := range p.conns {
		conns = append(conns, c)
	}
	p.conns = make(map[string]*Connection)
	p.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	if p.signaller != nil {
		_ = p.signaller.Close()
	}
}

// route 分发信令消息。
// OFFER/ANSWER/CANDIDATE 都是「连接类」消息：ANSWER/CANDIDATE 按 connectionId
// 路由到既有连接，OFFER 创建新连接（answerer）。
func (p *Peer) route(m Message) error {
	switch m.Type {
	case MsgOffer:
		p.handleOffer(m)
		return nil
	case MsgHeartbeat:
		// 服务端心跳：客户端主动 ping（heartbeatLoop）已保持活跃，无需应答
		return nil
	case MsgLeave:
		p.handleLeave(m)
		return nil
	case MsgAnswer, MsgCandidate:
		connID := payloadConnectionID(m)
		if connID == "" {
			return nil
		}
		p.mu.Lock()
		conn := p.conns[connID]
		p.mu.Unlock()
		if conn != nil {
			conn.handleMessage(m)
			return nil
		}
	case MsgExpire:
		// EXPIRE：OFFER 在信令服务器入队后过期（对端未及时上线）。
		// 关闭连接让上层 connectLoop 重连（否则永远等 OnOpen）。
		p.mu.Lock()
		connID := payloadConnectionID(m)
		conn := p.conns[connID]
		p.mu.Unlock()
		if conn != nil {
			conn.Close()
		}
	case MsgError, MsgIDTaken:
		// 低危 7 修复：ID 被占用/服务端错误之前被静默忽略——两节点同 ID 时
		// 双双失联且无任何痕迹。记日志便于排查（同 ID 是配置错误，重连无解）
		var payload struct {
			Msg string `json:"msg"`
		}
		_ = json.Unmarshal(m.Payload, &payload)
		if m.Type == MsgIDTaken {
			log.Printf("peerjs: ID-TAKEN: id %q already in use by another peer", m.Src)
		} else {
			log.Printf("peerjs: signalling error: %s", payload.Msg)
		}
	}
	return nil
}

// handleOffer 对端发起连接：创建 answerer Connection 并回 ANSWER。
func (p *Peer) handleOffer(m Message) {
	var payload OfferPayload
	if err := json.Unmarshal(m.Payload, &payload); err != nil {
		return
	}
	if payload.Type != ConnData || payload.SDP == nil {
		return
	}

	p.mu.Lock()
	old := p.conns[payload.ConnectionID]
	servers := p.iceServers
	onConn := p.onConn
	p.mu.Unlock()

	if old != nil {
		// 必须走完整 Close（closeOnce 幂等）：只关 pc 会泄漏——旧连接残留在
		// conns map、done 永不关闭（connectLoop 永久阻塞）、onClose 不触发
		// （上层 pending 请求挂起到超时）。
		// 注意：必须在 p.mu 解锁后调用——Close→forgetConnection 需要同一把锁，
		// 持锁调用会死锁（Go mutex 非重入）。
		old.Close()
	}

	conn, err := p.newConnection(m.Src, payload.Label, false, servers, payload.ConnectionID)
	if err != nil {
		return
	}
	// newConnection 已注册 conn；回 ANSWER 前设置远端 SDP。
	if err := conn.handleOffer(payload.SDP); err != nil {
		conn.Close()
		return
	}
	if onConn != nil {
		onConn(conn)
	}
}

func (p *Peer) handleLeave(m Message) {
	// 收集后解锁再 Close：Close→forgetConnection 需要 p.mu，持锁调用死锁。
	p.mu.Lock()
	var toClose []*Connection
	for _, c := range p.conns {
		if c.PeerID == m.Src {
			toClose = append(toClose, c)
		}
	}
	p.mu.Unlock()
	for _, c := range toClose {
		c.Close()
	}
}

// registerConnection 注册 WebRTC 连接。
func (p *Peer) registerConnection(c *Connection) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.conns[c.ID] = c
}

// forgetConnection 注销 WebRTC 连接。
func (p *Peer) forgetConnection(connectionID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, connectionID)
}

// payloadConnectionID 从消息 payload 中提取 connectionId。
func payloadConnectionID(m Message) string {
	var probe struct {
		ConnectionID string `json:"connectionId"`
	}
	if len(m.Payload) == 0 {
		return ""
	}
	if err := json.Unmarshal(m.Payload, &probe); err != nil {
		return ""
	}
	return probe.ConnectionID
}

// validID 校验 PeerJS ID 规则：首尾必须是字母数字，中间允许 - _ 空格。
func validID(id string) bool {
	if len(id) < 1 || len(id) > 256 {
		return false
	}
	isAlnum := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
	}
	if !isAlnum(id[0]) || !isAlnum(id[len(id)-1]) {
		return false
	}
	for i := 1; i < len(id)-1; i++ {
		c := id[i]
		if !isAlnum(c) && c != '-' && c != '_' && c != ' ' {
			return false
		}
	}
	return true
}

// randomToken 生成随机 token（字母数字，仿 peerjs util.randomToken）。
func randomToken() string {
	return randHex(16)
}

// randHex 生成 n 字节随机 hex。
func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// normalizeOptions 填充默认值。
func normalizeOptions(opts Options) Options {
	if opts.Port == "" {
		opts.Port = "443"
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	if opts.Key == "" {
		opts.Key = "peerjs"
	}
	if opts.PingInterval == 0 {
		opts.PingInterval = 5 * time.Second
	}
	if opts.Token == "" {
		opts.Token = randomToken()
	}
	return opts
}

// peerJSSignaller 是 Signaller 的 PeerJS 公共云实现。
type peerJSSignaller struct {
	id    string
	token string
	opts  Options
	route MessageHandler

	mu        sync.Mutex
	conn      *websocket.Conn
	connected bool
	closed    chan struct{}
	done      chan struct{} // H7：信令断线通知（readLoop 网络错误/EOF 退出时关闭）
	doneOnce  sync.Once

	// writeMu 串行化 WriteJSON：gorilla/websocket 不允许并发写，
	// 多 goroutine（心跳/ICE 候选/ANSWER）并发发送会 panic
	// （3 节点互通集成测试真实触发过）。
	writeMu sync.Mutex
}

func newPeerJSSignaller(id string, opts Options, route MessageHandler) Signaller {
	if opts.Token == "" {
		opts.Token = randomToken()
	}
	return &peerJSSignaller{
		id:     id,
		token:  opts.Token,
		opts:   opts,
		route:  route,
		closed: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// ID 返回节点 ID。
func (s *peerJSSignaller) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Done 返回信令断线通知（H7）。
func (s *peerJSSignaller) Done() <-chan struct{} { return s.done }

// Dial 注册并连接信令服务器。指定 ID 时直接连 WS；未指定先获取随机 ID。
func (s *peerJSSignaller) Dial(ctx context.Context) error {
	s.mu.Lock()
	if s.id == "" {
		id, err := s.retrieveID(ctx)
		if err != nil {
			s.mu.Unlock()
			return fmt.Errorf("peerjs: retrieve id: %w", err)
		}
		s.id = id
	}
	s.mu.Unlock()
	return s.dialWS(ctx)
}

func (s *peerJSSignaller) dialWS(ctx context.Context) error {
	scheme := "ws"
	if s.opts.Secure {
		scheme = "wss"
	}
	path := s.opts.Path
	if path[len(path)-1] != '/' {
		path += "/"
	}
	u := url.URL{
		Scheme: scheme,
		Host:   s.opts.Host + ":" + s.opts.Port,
		Path:   path + "peerjs",
		RawQuery: "key=" + url.QueryEscape(s.opts.Key) +
			"&id=" + url.QueryEscape(s.id) +
			"&token=" + url.QueryEscape(s.token) +
			"&version=" + version,
	}

	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	conn, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("peerjs: dial %s: %w", u.Host, err)
	}

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	s.mu.Unlock()

	go s.readLoop(conn)
	go s.heartbeatLoop()
	return nil
}

// readLoop 读取服务端消息并分发给 route。
func (s *peerJSSignaller) readLoop(conn *websocket.Conn) {
	// M7：信令消息（SDP/ICE 文案）很小，1MB 上限足以覆盖合法负载；
	// 云端信令若被攻破回超大帧，不设限会直接 OOM。
	conn.SetReadLimit(1 << 20)
	defer func() {
		s.mu.Lock()
		matches := s.conn == conn
		if matches {
			s.conn = nil
			s.connected = false
		}
		s.mu.Unlock()
		_ = conn.Close()
		if matches {
			// H7：非主动关闭（readLoop 网络错误/EOF 退出，conn 仍是当前连接）
			// → 通知上层触发重连。主动 Close() 先置 s.conn=nil 再关 conn，
			// 这里不命中 → done 不关闭，由 startLoop 的 ctx/closed 分支收尾
			// （避免 Close 后误触发重连循环）
			s.doneOnce.Do(func() { close(s.done) })
		}
	}()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var m Message
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		if s.route != nil {
			_ = s.route(m)
		}
	}
}

// heartbeatLoop 仿照 peerjs-client 每 PingInterval 发送一次 HEARTBEAT 保活。
func (s *peerJSSignaller) heartbeatLoop() {
	t := time.NewTicker(s.opts.PingInterval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := s.Send(NewMessage(MsgHeartbeat, "", nil)); err != nil {
				// H7：信令已断（readLoop 退出 → done 关闭，重连循环接管），
				// 心跳空转无意义，退出等待下一轮
				return
			}
		case <-s.closed:
			return
		case <-s.done:
			return
		}
	}
}

// Send 发送消息到信令服务器（服务端会覆盖 src 为客户端 id）。
func (s *peerJSSignaller) Send(m Message) error {
	s.mu.Lock()
	conn := s.conn
	s.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("peerjs: not connected")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return conn.WriteJSON(m)
}

// OnMessage 注册信令消息回调（框架内部注入 p.route；使用者无需调用）。
func (s *peerJSSignaller) OnMessage(h MessageHandler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.route = h
}

// Close 关闭信令连接。
func (s *peerJSSignaller) Close() error {
	s.mu.Lock()
	if s.conn == nil {
		s.mu.Unlock()
		return nil
	}
	conn := s.conn
	s.conn = nil
	s.connected = false
	select {
	case <-s.closed:
	default:
		close(s.closed)
	}
	s.mu.Unlock()
	return conn.Close()
}

// retrieveID 通过 HTTP 获取服务端分配的随机 ID。
func (s *peerJSSignaller) retrieveID(ctx context.Context) (string, error) {
	scheme := "http"
	if s.opts.Secure {
		scheme = "https"
	}
	path := s.opts.Path
	if path[len(path)-1] != '/' {
		path += "/"
	}
	u := fmt.Sprintf("%s://%s:%s%sid?ts=%d%d&version=%s",
		scheme, s.opts.Host, s.opts.Port, path,
		time.Now().UnixMilli(), time.Now().Nanosecond()%100000, version)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256))
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(b))
	if !validID(id) {
		return "", fmt.Errorf("server returned invalid id %q", id)
	}
	return id, nil
}
