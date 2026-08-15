// NodeRegistrar 在 Peerdrive 节点启动时向注册服务器登记节点身份。
// 匿名节点不调用此功能 — 仅当 PEERDRIVE_AUTH_TOKEN 配置后才启用。
// 注册服务器通过此绑定知道 "peer X 属于用户 Y"。
package legacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/nodestate"
)

// localHTTPClient returns an HTTP client that bypasses proxy for localhost/loopback.
func localHTTPClient() *http.Client {
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			host, _, _ := net.SplitHostPort(req.URL.Host)
			if host == "" {
				host = req.URL.Host
			}
			if host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" || host == "0.0.0.0" {
				return nil, nil // bypass proxy for local addresses
			}
			return http.ProxyFromEnvironment(req)
		},
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	return &http.Client{Transport: transport, Timeout: 30 * time.Second}
}

type NodeRegistrar struct {
	p2pSvc    *P2PService
	regURL    string
	authToken string
	version   string
	client    *http.Client
	peerID    string
	addrs     []string
	username  string
	stop      chan struct{} // L5:心跳 goroutine 停止信号
	stopOnce  sync.Once
}

// NewNodeRegistrar 创建节点注册器。如果 regURL 或 authToken 为空，返回 nil。
func NewNodeRegistrar(p2pSvc *P2PService, regURL, authToken, version string) *NodeRegistrar {
	if regURL == "" || authToken == "" {
		return nil
	}
	id, addrs := p2pSvc.GetNodeInfo()
	return &NodeRegistrar{
		p2pSvc:    p2pSvc,
		regURL:    regURL,
		authToken: authToken,
		version:   version,
		client:    localHTTPClient(),
		peerID:    id.String(),
		addrs:     addrs,
		stop:      make(chan struct{}),
	}
}

// Start 验证 token、设置 operator 并向注册服务器登记节点身份。
func (r *NodeRegistrar) Start() {
	// 1. Validate token against reg server and get username
	username, err := r.whoami()
	if err != nil {
		log.LogWarn("node-registrar: auth failed, node will be anonymous: %v", err)
		return
	}
	r.username = username
	// Always set operator (even without P2P peer_id) so /p2p/node/operator works
	nodestate.Configure(username, r.regURL, r.authToken, r.peerID)
	log.LogInfo("node-registrar: authenticated as %s (peer_id=%s)", username, r.peerID)

	// 2. Register node with reg server (only if we have a peer_id)
	if r.peerID != "" {
		r.register()
	}

	// 3. Periodic heartbeat (every 120s)
	go func() {
		ticker := time.NewTicker(120 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				if r.peerID != "" {
					r.heartbeat()
				}
			}
		}
	}()
}

// Stop 停止心跳 goroutine（L5：原实现无停止机制，进程关闭时泄漏 goroutine）。
func (r *NodeRegistrar) Stop() {
	r.stopOnce.Do(func() { close(r.stop) })
}

func (r *NodeRegistrar) whoami() (string, error) {
	req, _ := http.NewRequest("GET", r.regURL+"/auth/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+r.authToken)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("whoami request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("whoami returned %d", resp.StatusCode)
	}
	var result struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("whoami decode: %w", err)
	}
	return result.Username, nil
}

func (r *NodeRegistrar) register() {
	body := map[string]interface{}{
		"peer_id": r.peerID,
		"addrs":   r.addrs,
		"version": r.version,
	}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", r.regURL+"/auth/node/register", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.authToken)
	resp, err := r.client.Do(req)
	if err != nil {
		log.LogWarn("node-registrar: register failed: %v", err)
		return
	}
	resp.Body.Close()
	log.LogInfo("node-registrar: registered node %s as %s", r.peerID, r.username)
}

func (r *NodeRegistrar) heartbeat() {
	body := fmt.Sprintf(`{"peer_id":"%s"}`, r.peerID)
	req, _ := http.NewRequest("POST", r.regURL+"/auth/node/heartbeat", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.authToken)
	resp, err := r.client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
