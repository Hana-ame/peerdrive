// Package controller provides HTTP handlers for P2P network related endpoints, including node info, peer management, BT DHT, dual network, port forwarding, etc.
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/nodestate"
	"peerdrive/internal/service"
	"peerdrive/internal/transport"

	"github.com/gin-gonic/gin"
)

var pinSvc *service.PinService
var forwardPeer *transport.PeerJSService // PeerJS-based port forwarding (forward v2, see transport/forward.go)

func InitPinController(svc *service.PinService) {
	pinSvc = svc
}

func InitForwardController(svc *transport.PeerJSService) {
	log.LogDebug("ctrl-p2p: InitForwardController")
	forwardPeer = svc
}

// AuthStatus handles GET /p2p/auth/status, returns the current node's authentication status.
func AuthStatus(c *gin.Context) {
	authenticated, _ := c.Get("authenticated")
	username, _ := c.Get("username")
	role, _ := c.Get("role")

	isAuth := false
	if a, ok := authenticated.(bool); ok {
		isAuth = a
	}

	uname := ""
	if u, ok := username.(string); ok {
		uname = u
	}

	r := ""
	if rl, ok := role.(string); ok {
		r = rl
	}

	// operator and username are not the same thing: username comes from the request context (the caller of this request),
	// operator is the operator account registered after the node logs into regserver (anonymized collection Owner uses this value).
	// The frontend needs it to determine whether the "only me" tier is available (Owner empty = no one can read).
	c.JSON(http.StatusOK, gin.H{
		"authenticated": isAuth,
		"username":      uname,
		"role":          r,
		"operator":      nodestate.GetOperator(),
	})
}

// ─── IPFS Compat Handlers ─────────────────────────────────────────

// InitIPFSCompatController injects IPFSCompatLayer instance for IPFS compatibility endpoints.

// IPFSCompatStatus handles GET /ipfs, returns IPFS compatibility layer status info.

// IPFSCompatToggle handles POST /ipfs/toggle, enables or disables IPFS compatibility mode.

// --- IPFS Pin & Gateway Handlers ------------------------------------

// PinCID handles POST /ipfs/pin/:cid, downloads CID from IPFS gateway and caches permanently.
func PinCID(c *gin.Context) {
	log.LogDebug("ctrl-p2p: PinCID")
	cidParam := c.Param("cid")
	if cidParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cid is required"})
		return
	}

	if ipfsGatewayProvider == nil || len(ipfsGatewayProvider.Gateways) == 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "IPFS gateway not configured"})
		return
	}

	// Check if already pinned.
	existing, _ := pinSvc.Get(cidParam)
	if existing != nil {
		c.JSON(http.StatusOK, gin.H{"status": "already_pinned", "pin": existing})
		return
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	data, err := ipfsGatewayProvider.FetchByCID(ctx, cidParam)
	if err != nil {
		log.LogError("ctrl-p2p: PinCID fetch %s failed: %v", cidParam, err)
		c.JSON(http.StatusNotFound, gin.H{"error": "failed to fetch CID from gateways: " + err.Error()})
		return
	}

	// Compute SHA256 and cache locally.
	h := sha256.Sum256(data)
	hashStr := hex.EncodeToString(h[:])

	// Get storage dir from context.
	storageDir := ""
	if d, ok := c.Get("storageDir"); ok {
		storageDir, _ = d.(string)
	}

	if storageDir != "" {
		relPath := filepath.Join(hashStr[:2], hashStr)
		fullPath := filepath.Join(storageDir, relPath)
		_ = os.MkdirAll(filepath.Dir(fullPath), 0755)
		_ = os.WriteFile(fullPath, data, 0644)

		_ = pinSvc.InsertMeta(hashStr, cidParam, int64(len(data)), relPath)

	}

	// Record the pin.
	_ = pinSvc.Insert(cidParam, hashStr, cidParam, int64(len(data)))

	log.LogInfo("ctrl-p2p: PinCID %s -> hash=%s size=%d", cidParam, hashStr, len(data))
	c.JSON(http.StatusOK, gin.H{
		"status": "pinned",
		"cid":    cidParam,
		"hash":   hashStr,
		"size":   len(data),
	})
}

// UnpinCID handles DELETE /ipfs/pin/:cid, unpins a CID.
func UnpinCID(c *gin.Context) {
	log.LogDebug("ctrl-p2p: UnpinCID")
	cidParam := c.Param("cid")
	if cidParam == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cid is required"})
		return
	}

	pin, err := pinSvc.Get(cidParam)
	if err != nil {
		log.LogError("ctrl-p2p: UnpinCID lookup failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pin == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}

	if err := pinSvc.Remove(cidParam); err != nil {
		log.LogError("ctrl-p2p: UnpinCID remove failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	log.LogInfo("ctrl-p2p: UnpinCID %s removed", cidParam)
	c.JSON(http.StatusOK, gin.H{"status": "unpinned", "cid": cidParam})
}

// ListPins handles GET /ipfs/pins, lists all pinned CIDs.
func ListPins(c *gin.Context) {
	log.LogDebug("ctrl-p2p: ListPins")
	pins, err := pinSvc.List()
	if err != nil {
		log.LogError("ctrl-p2p: ListPins failed: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if pins == nil {
		pins = []model.IPFSPin{}
	}
	c.JSON(http.StatusOK, gin.H{"pins": pins, "count": len(pins)})
}

// gwStatus reports the health of an IPFS gateway.
type gwStatus struct {
	URL     string `json:"url"`
	Healthy bool   `json:"healthy"`
	Latency string `json:"latency,omitempty"`
}

// IPFSGatewayStatus handles GET /ipfs/gateways, checks health of each configured gateway.
func IPFSGatewayStatus(c *gin.Context) {
	log.LogDebug("ctrl-p2p: IPFSGatewayStatus")
	if ipfsGatewayProvider == nil || len(ipfsGatewayProvider.Gateways) == 0 {
		c.JSON(http.StatusOK, gin.H{"gateways": []interface{}{}})
		return
	}

	results := make([]gwStatus, len(ipfsGatewayProvider.Gateways))
	for i, gw := range ipfsGatewayProvider.Gateways {
		results[i] = checkGateway(gw)
	}

	c.JSON(http.StatusOK, gin.H{"gateways": results})
}

// checkGateway pings a single IPFS gateway to check its health.
func checkGateway(gw string) gwStatus {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	start := time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, strings.TrimRight(gw, "/")+"/ipfs/QmUNLLsPACCz1vLxQVkXqqLX5R1X345qqfHbsf67hvA3Nn", nil)
	if err != nil {
		return gwStatus{URL: gw, Healthy: false}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return gwStatus{URL: gw, Healthy: false}
	}
	resp.Body.Close()

	latency := time.Since(start)
	healthy := resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound
	return gwStatus{URL: gw, Healthy: healthy, Latency: latency.String()}
}

// ─── Node Operator (who runs this node) ───

// GetNodeOperator handles GET /p2p/node/operator
