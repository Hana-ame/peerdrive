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

package service

import (
	"bytes"
	"encoding/json"
	"net/http"
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
}

// NewRelayRegistry creates a new RelayRegistry.  If the P2P service is not
// enabled the registry will be a no-op.
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
	}
}

// Start registers this node as a relay immediately and launches a background
// goroutine that sends a heartbeat every 60 seconds.
func (r *RelayRegistry) Start() {
	if r.regURL == "" || r.peerID == "" {
		log.LogInfo("relay-registry: skipping registration (regURL=%q, peerID=%q)", r.regURL, r.peerID)
		return
	}

	r.register()

	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			r.heartbeat()
		}
	}()
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
