// Package controller provides HTTP handlers for port forwarding endpoints.
package controller

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/log"
	"github.com/gin-gonic/gin"
)

// fwdListener local proxy listener (established by connect endpoint): each local
// TCP connection received on the loopback port -> one forwarding tunnel (OpenForward).
type fwdListener struct {
	key        string
	targetPeer string
	ln         net.Listener
}

var (
	fwdListenersMu sync.Mutex
	fwdListeners   = map[string]*fwdListener{} // key: "<target_peer>:<local_port>"
)

// CreateForwardSession handles POST /p2p/forward/create, registers this node's forwarding authorization rule
// (key -> port whitelist, runtime in-memory table; static rules go through config PEERDRIVE_FORWARD_RULES).
func CreateForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CreateForwardSession")
	if forwardPeer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}
	var req struct {
		Key  string `json:"key"`
		Port int    `json:"port"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key is required"})
		return
	}
	if err := forwardPeer.AddForwardRule(req.Key, req.Port); err != nil {
		log.LogError("ctrl-p2p: CreateForwardSession failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	log.LogInfo("ctrl-p2p: CreateForwardSession rule added port=%d", req.Port)
	c.JSON(http.StatusOK, gin.H{"status": "rule-added", "port": req.Port})
}

// ConnectForwardSession handles POST /p2p/forward/connect: starts a local loopback listener,
// each local TCP connection goes through a forwarding tunnel to the target peer's authorized port (when port=0, the target is determined by rules).
func ConnectForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ConnectForwardSession")
	if forwardPeer == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "forward service not enabled"})
		return
	}
	var req struct {
		Key        string `json:"key"`
		TargetPeer string `json:"target_peer"`
		LocalPort  int    `json:"local_port"`
		Port       int    `json:"port"` // Target port (optional: 0 = target node's unique port by rules)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession invalid request: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Key == "" || req.TargetPeer == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "key and target_peer are required"})
		return
	}
	if req.LocalPort <= 0 || req.LocalPort > 65535 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid local_port"})
		return
	}
	if req.Port != 0 && (req.Port <= 0 || req.Port > 65535) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid port"})
		return
	}
	// Idempotent: if a listener already exists for the same key+port, return directly (frontend retries/duplicate clicks won't break)
	key2 := fmt.Sprintf("%s:%d", req.TargetPeer, req.LocalPort)
	fwdListenersMu.Lock()
	if _, ok := fwdListeners[key2]; ok {
		fwdListenersMu.Unlock()
		c.JSON(http.StatusOK, gin.H{"status": "connected", "local_port": req.LocalPort})
		return
	}
	fwdListenersMu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", req.LocalPort))
	if err != nil {
		log.LogWarn("ctrl-p2p: ConnectForwardSession listen failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	fl := &fwdListener{key: req.Key, targetPeer: req.TargetPeer, ln: ln}
	fwdListenersMu.Lock()
	fwdListeners[key2] = fl
	fwdListenersMu.Unlock()
	go acceptForwardTunnels(fl, req.Port)
	log.LogInfo("ctrl-p2p: ConnectForwardSession listening 127.0.0.1:%d -> %s", req.LocalPort, req.TargetPeer)
	c.JSON(http.StatusOK, gin.H{"status": "connected", "local_port": req.LocalPort})
}

// acceptForwardTunnels opens a forwarding tunnel for each local TCP connection and pipes bidirectionally.
// Semantics: if connection establishment fails (bad key/insufficient permissions/tunnel occupied), only that connection is dropped; listening continues.
func acceptForwardTunnels(fl *fwdListener, targetPort int) {
	defer fl.ln.Close()
	for {
		tcp, err := fl.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			tun, err := forwardPeer.OpenForward(ctx, fl.targetPeer, fl.key, targetPort)
			if err != nil {
				log.LogWarn("ctrl-p2p: forward tunnel to %s failed: %v", fl.targetPeer, err)
				tcp.Close()
				return
			}
			go pipeTCPForward(tcp, tun)
		}()
	}
}

// pipeTCPForward pipes bidirectionally between local TCP <-> forwarding tunnel. Either side EOF/error closes both directions:
// tunnel-side close triggers the pump's fwd-close notification to the peer to clear the slot (see transport/forward.go).
func pipeTCPForward(tcp net.Conn, tun net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(tcp, tun)
		done <- struct{}{}
	}()
	io.Copy(tun, tcp)
	done <- struct{}{}
	<-done
	tcp.Close()
	tun.Close()
}

// ListForwardSessions handles GET /p2p/forward/list, returns local active listeners and tunnels.
func ListForwardSessions(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ListForwardSessions")
	listeners := []gin.H{}
	fwdListenersMu.Lock()
	for k, fl := range fwdListeners {
		listeners = append(listeners, gin.H{"id": k, "target_peer": fl.targetPeer, "local_port": fl.ln.Addr().String()})
	}
	fwdListenersMu.Unlock()
	tunnels := []gin.H{}
	if forwardPeer != nil {
		for _, t := range forwardPeer.ListForwardStreams() {
			tunnels = append(tunnels, gin.H{
				"peer_id":    t.PeerID,
				"port":       t.Port,
				"key_id":     t.KeyID,
				"bytes_in":   t.BytesIn,
				"bytes_out":  t.BytesOut,
				"created_at": t.CreatedAt,
			})
		}
	}
	c.JSON(http.StatusOK, gin.H{"listeners": listeners, "tunnels": tunnels})
}

// CloseForwardSession handles POST /p2p/forward/close, closes the local listener for the specified key
// (active tunnels are naturally closed at both ends; use peer_id to disconnect all tunnels to a specified peer).
func CloseForwardSession(c *gin.Context) {
	log.LogDebug("ctrl-p2p: CloseForwardSession")
	var req struct {
		Key    string `json:"key"`
		PeerID string `json:"peer_id,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	closed := 0
	fwdListenersMu.Lock()
	for k, fl := range fwdListeners {
		if req.Key != "" && fl.key != req.Key {
			continue
		}
		fl.ln.Close()
		delete(fwdListeners, k)
		closed++
	}
	fwdListenersMu.Unlock()
	if forwardPeer != nil && req.PeerID != "" {
		forwardPeer.CloseForwardStream(req.PeerID)
	}
	if closed == 0 && req.PeerID == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "no matching forward session"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "closed", "closed_listeners": closed})
}

