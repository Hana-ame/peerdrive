// Package nodestate stores the runtime state of a Peerdrive node (operator username, reg server connection),
// and provides statistics reporting. Both controller and service need to access this state, so it is split
// into a separate package to break circular imports.
package nodestate

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
)

var (
	mu        sync.Mutex
	operator  string
	regURL    string
	authToken string
	peerID    string
)

// Configure sets the node identity and registration server connection info. Called by NodeRegistrar.Start().
func Configure(op, url, token, pid string) {
	mu.Lock()
	defer mu.Unlock()
	operator = op
	regURL = url
	authToken = token
	peerID = pid
}

// SetOperator sets the node operator (empty string = anonymous).
func SetOperator(username string) {
	mu.Lock()
	defer mu.Unlock()
	operator = username
}

// GetOperator returns the node operator (empty string = anonymous).
func GetOperator() string {
	mu.Lock()
	defer mu.Unlock()
	return operator
}

// GetPeerID returns the node's peer ID.
func GetPeerID() string {
	mu.Lock()
	defer mu.Unlock()
	return peerID
}

// ReportStats reports transfer statistics to the registration server. Only effective when the node is authenticated.
func ReportStats(uploadBytes, downloadBytes int64) {
	mu.Lock()
	u := regURL
	t := authToken
	p := peerID
	mu.Unlock()

	if u == "" || t == "" || p == "" {
		return
	}
	if uploadBytes == 0 && downloadBytes == 0 {
		return
	}

	body, _ := json.Marshal(map[string]interface{}{
		"peer_id":        p,
		"upload_bytes":   uploadBytes,
		"download_bytes": downloadBytes,
	})

	client := localClient()
	req, _ := http.NewRequest("POST", u+"/auth/node/stats", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t)
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}

func localClient() *http.Client {
	transport := &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			host, _, _ := net.SplitHostPort(req.URL.Host)
			if host == "" {
				host = req.URL.Host
			}
			if host == "localhost" || strings.HasPrefix(host, "127.") || host == "::1" {
				return nil, nil
			}
			return http.ProxyFromEnvironment(req)
		},
		DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}
}

// ensure fmt is used
var _ = fmt.Sprintf
