// Package service provides the peer-to-peer port forwarding service.
//
// ForwardService allows sharing a local TCP port with a remote peer via libp2p
// streams, authenticated with a shared secret key.
//
// Usage:
//   - Peer A calls CreateForward(key, port) to expose a local service.
//   - Peer B calls ConnectForward(ctx, peerA, key, localPort) to access it.
//   - Data flows: TCP client ↔ B's local listener ↔ libp2p stream ↔ A's handler ↔ A's local service.
package service

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"peerdrive/internal/log"

	"github.com/libp2p/go-libp2p/core/host"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ForwardProtocol is the libp2p protocol ID for port forwarding.
const ForwardProtocol = "/peerdrive/forward/1.0.0"

// ForwardSession represents an active port forwarding session.
// On the source side (CreateForward), it holds the mapping from shared key
// to the local service port. On the client side (ConnectForward), it also
// holds the local TCP listener.
type ForwardSession struct {
	Key        string    `json:"key"`
	SourcePeer peer.ID   `json:"source_peer"`
	LocalPort  int       `json:"local_port"`
	CreatedAt  time.Time `json:"created_at"`
	Clients    int       `json:"clients"` // populated on ListSessions

	// internal state
	listener    net.Listener
	streamCount int32
}

// ForwardService manages port forwarding sessions over libp2p.
type ForwardService struct {
	host     host.Host
	sessions map[string]*ForwardSession
	mu       sync.RWMutex
	enabled  bool
}

// NewForwardService 创建端口转发服务，启用时注册 libp2p 流处理器。
func NewForwardService(h host.Host, enabled bool) *ForwardService {
	svc := &ForwardService{
		host:     h,
		sessions: make(map[string]*ForwardSession),
		enabled:  enabled,
	}
	if enabled && h != nil {
		h.SetStreamHandler(protocol.ID(ForwardProtocol), svc.handleStream)
		log.LogInfo("forward: service initialized, protocol=%s", ForwardProtocol)
	}
	return svc
}

// IsEnabled 返回转发服务是否已启用且 host 可用。
func (f *ForwardService) IsEnabled() bool {
	return f.enabled && f.host != nil
}

// CreateForward 在源端注册本地服务端口，供远程对端通过 sharedKey 认证后转发访问。
func (f *ForwardService) CreateForward(sharedKey string, localPort int) error {
	if !f.IsEnabled() {
		return fmt.Errorf("forward service not enabled")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.sessions[sharedKey]; exists {
		return fmt.Errorf("forward session with key %q already exists", sharedKey)
	}

	f.sessions[sharedKey] = &ForwardSession{
		Key:        sharedKey,
		SourcePeer: f.host.ID(),
		LocalPort:  localPort,
		CreatedAt:  time.Now(),
	}

	log.LogInfo("forward: created session port=%d", localPort)
	return nil
}

// ConnectForward 在客户端打开到远程对端的转发连接，认证后启动本地 TCP 监听器，将入站连接通过 libp2p 流转发到远程。
func (f *ForwardService) ConnectForward(ctx context.Context, targetPeer peer.ID, sharedKey string, localPort int) error {
	if !f.IsEnabled() {
		return fmt.Errorf("forward service not enabled")
	}

	// Validate by opening a test stream and authenticating.
	stream, err := f.host.NewStream(ctx, targetPeer, protocol.ID(ForwardProtocol))
	if err != nil {
		return fmt.Errorf("open stream to %s: %w", targetPeer.String(), err)
	}

	if _, err := fmt.Fprintf(stream, "KEY %s\n", sharedKey); err != nil {
		stream.Close()
		return fmt.Errorf("send auth: %w", err)
	}

	reader := bufio.NewReader(stream)
	resp, err := reader.ReadString('\n')
	stream.Close()
	if err != nil {
		return fmt.Errorf("read auth response: %w", err)
	}
	resp = strings.TrimSpace(resp)

	if strings.HasPrefix(resp, "ERR") {
		return fmt.Errorf("auth rejected: %s", resp)
	}
	if !strings.HasPrefix(resp, "OK") {
		return fmt.Errorf("unexpected response: %s", resp)
	}

	// Start local TCP listener.
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		return fmt.Errorf("listen 127.0.0.1:%d: %w", localPort, err)
	}

	f.mu.Lock()
	if _, exists := f.sessions[sharedKey]; exists {
		listener.Close()
		f.mu.Unlock()
		return fmt.Errorf("forward session with key %q already exists", sharedKey)
	}
	sess := &ForwardSession{
		Key:        sharedKey,
		SourcePeer: targetPeer,
		LocalPort:  localPort,
		CreatedAt:  time.Now(),
		listener:   listener,
	}
	f.sessions[sharedKey] = sess
	f.mu.Unlock()

	go f.acceptConnections(sess, targetPeer, sharedKey)

	log.LogInfo("forward: connected to %s local_port=%d", targetPeer.String(), localPort)
	return nil
}

// acceptConnections runs a loop accepting TCP connections on the client-side
// listener and forwarding each one over a libp2p stream.
func (f *ForwardService) acceptConnections(sess *ForwardSession, targetPeer peer.ID, sharedKey string) {
	for {
		tcpConn, err := sess.listener.Accept()
		if err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") &&
				!strings.Contains(err.Error(), "closed") {
				log.LogWarn("forward: accept error on port %d: %v", sess.LocalPort, err)
			}
			return
		}
		go f.forwardClientConn(context.Background(), tcpConn, targetPeer, sharedKey)
	}
}

// forwardClientConn opens a libp2p stream to the target peer, authenticates,
// and bidirectionally pipes the TCP connection with the stream.
func (f *ForwardService) forwardClientConn(ctx context.Context, tcpConn net.Conn, targetPeer peer.ID, sharedKey string) {
	defer tcpConn.Close()

	stream, err := f.host.NewStream(ctx, targetPeer, protocol.ID(ForwardProtocol))
	if err != nil {
		log.LogWarn("forward: open stream to %s: %v", targetPeer.String(), err)
		return
	}
	defer stream.Close()

	if _, err := fmt.Fprintf(stream, "KEY %s\n", sharedKey); err != nil {
		log.LogWarn("forward: send auth: %v", err)
		return
	}

	reader := bufio.NewReader(stream)
	resp, err := reader.ReadString('\n')
	if err != nil {
		log.LogWarn("forward: read auth response: %v", err)
		return
	}
	resp = strings.TrimSpace(resp)
	if strings.HasPrefix(resp, "ERR") {
		log.LogWarn("forward: auth failed: %s", resp)
		return
	}

	log.LogDebug("forward: piping TCP<->stream target=%s", targetPeer.String())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(stream, tcpConn)
	}()
	go func() {
		defer wg.Done()
		io.Copy(tcpConn, stream)
	}()
	wg.Wait()
}

// handleStream handles an incoming libp2p stream for the forward protocol.
// It reads the KEY line, looks up the session, and pipes data between the
// stream and the local TCP service.
func (f *ForwardService) handleStream(stream network.Stream) {
	remotePeer := stream.Conn().RemotePeer()
	defer stream.Close()

	reader := bufio.NewReader(stream)
	line, err := reader.ReadString('\n')
	if err != nil {
		log.LogDebug("forward: read from %s: %v", remotePeer.String(), err)
		return
	}
	line = strings.TrimSpace(line)

	if !strings.HasPrefix(line, "KEY ") {
		fmt.Fprintf(stream, "ERR bad protocol\n")
		return
	}

	sharedKey := strings.TrimPrefix(line, "KEY ")

	f.mu.RLock()
	sess, ok := f.sessions[sharedKey]
	f.mu.RUnlock()

	if !ok {
		log.LogWarn("forward: unauthorized attempt from %s", remotePeer.String())
		fmt.Fprintf(stream, "ERR unauthorized\n")
		return
	}

	// Confirm the connection and send back the port number.
	fmt.Fprintf(stream, "OK %d\n", sess.LocalPort)

	// Connect to the local service port.
	addr := fmt.Sprintf("127.0.0.1:%d", sess.LocalPort)
	localConn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		log.LogWarn("forward: dial %s: %v", addr, err)
		return
	}
	defer localConn.Close()

	atomic.AddInt32(&sess.streamCount, 1)
	defer atomic.AddInt32(&sess.streamCount, -1)

	log.LogDebug("forward: piping stream<->%s from %s", addr, remotePeer.String())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(localConn, stream)
	}()
	go func() {
		defer wg.Done()
		io.Copy(stream, localConn)
	}()
	wg.Wait()
}

// CloseForward 删除指定 key 的转发会话并关闭监听器（如有）。
func (f *ForwardService) CloseForward(sharedKey string) error {
	f.mu.Lock()
	sess, ok := f.sessions[sharedKey]
	if !ok {
		f.mu.Unlock()
		return fmt.Errorf("forward session with key %q not found", sharedKey)
	}
	delete(f.sessions, sharedKey)
	f.mu.Unlock()

	if sess.listener != nil {
		sess.listener.Close()
	}

	log.LogInfo("forward: closed session")
	return nil
}

// ListSessions 返回所有活跃转发会话的快照（含当前客户端连接数）。
func (f *ForwardService) ListSessions() []ForwardSession {
	f.mu.RLock()
	defer f.mu.RUnlock()

	result := make([]ForwardSession, 0, len(f.sessions))
	for _, s := range f.sessions {
		cp := *s
		cp.Clients = int(atomic.LoadInt32(&s.streamCount))
		result = append(result, cp)
	}
	return result
}
