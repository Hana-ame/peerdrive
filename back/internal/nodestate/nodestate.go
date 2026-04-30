// Package nodestate 存储 Peerdrive 节点的运行时状态（operator 用户名、reg server 连接），
// 并提供统计报告功能。controller 和 service 都需要访问此状态，故独立为一个包以打破循环导入。
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
	mu          sync.Mutex
	operator    string
	regURL      string
	authToken   string
	peerID      string
)

// Configure 设置节点身份和注册服务器连接信息。由 NodeRegistrar.Start() 调用。
func Configure(op, url, token, pid string) {
	mu.Lock()
	defer mu.Unlock()
	operator = op
	regURL = url
	authToken = token
	peerID = pid
}

// SetOperator 设置节点运营者（空字符串 = 匿名）。
func SetOperator(username string) {
	mu.Lock()
	defer mu.Unlock()
	operator = username
}

// GetOperator 获取节点运营者（空字符串 = 匿名）。
func GetOperator() string {
	mu.Lock()
	defer mu.Unlock()
	return operator
}

// GetPeerID 获取节点 peer ID。
func GetPeerID() string {
	mu.Lock()
	defer mu.Unlock()
	return peerID
}

// ReportStats 向注册服务器报告传输统计。仅在节点已认证时有效。
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
