// Package service provides relay node auto-registration with the Peerdrive
// registration server.
//
// When PEERDRIVE_REG_SERVER_URL is set, the relay registry contacts the
// registration server to announce this node as a relay and sends periodic
// heartbeats so the server knows it is still alive.
//
// Usage (in cmd/server/main.go):
//
//	if cfg.RegServerURL != "" && p2pSvc.IsEnabled() {
//	    registry := service.NewRelayRegistry(p2pSvc, cfg.RegServerURL, cfg.RelayStorageMB, cfg.RelayVersion)
//	    registry.Start()
//	}

package legacy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// RelayRegistry handles registering this node as a relay with the
// registration server and sending periodic heartbeats.
type RelayRegistry struct {
	p2pSvc    *P2PService
	regURL    string
	storageMB int
	version   string
	client    *http.Client
	peerID    string
	addrs     []string
	stop      chan struct{} // L5:心跳 goroutine 停止信号
	stopOnce  sync.Once
}

// NewRelayRegistry 创建中继注册器实例，P2P 未启用时注册器为空操作。
func NewRelayRegistry(p2pSvc *P2PService, regURL string, storageMB int, version string) *RelayRegistry {
	id, addrs := p2pSvc.GetNodeInfo()
	return &RelayRegistry{
		p2pSvc:    p2pSvc,
		regURL:    regURL,
		storageMB: storageMB,
		version:   version,
		client:    &http.Client{Timeout: 30 * time.Second},
		peerID:    id.String(),
		addrs:     addrs,
		stop:      make(chan struct{}),
	}
}

// Start 立即注册本节点为中继并启动后台心跳协程（每 60 秒）。
func (r *RelayRegistry) Start() {
	if r.regURL == "" || r.peerID == "" {
		log.LogInfo("relay-registry: skipping registration (regURL=%q, peerID=%q)", r.regURL, r.peerID)
		return
	}

	r.register()

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				r.heartbeat()
			}
		}
	}()
}

// Stop 停止心跳 goroutine（L5：原实现无停止机制，进程关闭时泄漏 goroutine）。
func (r *RelayRegistry) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// register sends a POST request to the registration server to register
// this node as a relay.
func (r *RelayRegistry) register() {
	body := map[string]interface{}{
		"peer_id":    r.peerID,
		"addrs":      r.addrs,
		"storage_mb": r.storageMB,
		"version":    r.version,
	}
	data, err := json.Marshal(body)
	if err != nil {
		log.LogWarn("relay-registry: failed to marshal register body: %v", err)
		return
	}

	resp, err := r.client.Post(r.regURL+"/p2p/relay/register", "application/json", bytes.NewReader(data))
	if err != nil {
		log.LogWarn("relay-registry: register failed: %v", err)
		return
	}
	resp.Body.Close()

	log.LogInfo("relay-registry: registered as relay node (peer_id=%s)", r.peerID)
}

// heartbeat sends a POST request to the registration server to update
// this relay's last heartbeat timestamp.
func (r *RelayRegistry) heartbeat() {
	body := map[string]interface{}{
		"peer_id":  r.peerID,
		"load_pct": 0.0,
	}
	data, err := json.Marshal(body)
	if err != nil {
		return
	}

	resp, err := r.client.Post(r.regURL+"/p2p/relay/heartbeat", "application/json", bytes.NewReader(data))
	if err != nil {
		log.LogWarn("relay-registry: heartbeat failed: %v", err)
		return
	}
	resp.Body.Close()
}
