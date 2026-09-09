// Package ech 提供 ECH (Encrypted Client Hello) HTTP 客户端。
//
// 背景：twitter 的媒体 CDN（video-cf.twimg.com）在中国大陆直连被墙，
// 但它在 Cloudflare 后面。ECH 域前置（domain fronting）利用
// cloudflare-ech.com 作为外壳：TCP 连 cloudflare-ech.com（不被墙），
// TLS 握手时通过 ECH 加密的 ClientHello 把真实目标域名
// （video-cf.twimg.com）告诉 Cloudflare 边缘，由边缘路由到目标。
// 因为 ClientHello 里的 SNI 被 ECH 加密，GFW 只看到外壳域名，放行。
//
// 本包不依赖 wintools，独立实现（peerdrive 生产版 media-node 用）。
package ech

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sync"
	"sync/atomic"
	"time"
)

// ---- ECH 配置缓存 ----

type echEntry struct {
	config []byte
	expiry time.Time
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*echEntry{}
)

const (
	minTTL = 60
	maxTTL = 24 * 3600
)

func getCachedECH(domain string) []byte {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	e, ok := cache[domain]
	if !ok || time.Now().After(e.expiry) {
		return nil
	}
	return e.config
}

func setCachedECH(domain string, config []byte, ttl int) {
	if ttl < minTTL {
		ttl = minTTL
	}
	if ttl > maxTTL {
		ttl = maxTTL
	}
	cacheMu.Lock()
	cache[domain] = &echEntry{config: config, expiry: time.Now().Add(time.Duration(ttl) * time.Second)}
	cacheMu.Unlock()
}

// ---- DoH 获取 ECH 配置 ----

type dohResponse struct {
	Answer []struct {
		Type int    `json:"type"`
		TTL  int    `json:"TTL"`
		Data string `json:"data"`
	} `json:"Answer"`
}

func parseSVCBWire(wire []byte) ([]byte, error) {
	if len(wire) < 2 {
		return nil, fmt.Errorf("wire too short")
	}
	off := 2
	for off < len(wire) {
		labelLen := int(wire[off])
		off++
		if labelLen == 0 {
			break
		}
		if off+labelLen > len(wire) {
			return nil, fmt.Errorf("truncated target name")
		}
		off += labelLen
	}
	for off+4 <= len(wire) {
		key := int(wire[off])<<8 | int(wire[off+1])
		valLen := int(wire[off+2])<<8 | int(wire[off+3])
		off += 4
		if off+valLen > len(wire) {
			return nil, fmt.Errorf("truncated SvcParam")
		}
		val := wire[off : off+valLen]
		off += valLen
		if key == 5 {
			return val, nil
		}
	}
	return nil, fmt.Errorf("ECH SvcParam not found")
}

var (
	echSvcParamRE = regexp.MustCompile(`ech="?([A-Za-z0-9+/=]+)"?`)
	wsvcWireRE    = regexp.MustCompile(`\\#\s+(\d+)\s+([0-9a-fA-F\s]+)`)
	nonHexRE      = regexp.MustCompile(`[^0-9a-fA-F]`)
)

// DefaultDoHURL 默认 DoH 端点（moonchan.xyz 自托管 DNS over HTTPS，
// 返回 cloudflare-ech.com 的 SVCB/ECH 记录）。
const DefaultDoHURL = "https://moonchan.xyz/doh"

// Config 客户端配置。
type Config struct {
	// DoHURL 获取 ECH 配置的端点（默认 DefaultDoHURL）。
	DoHURL string
	// ProxyURL HTTP 代理（"http://host:port"）；空则读 HTTPS_PROXY 环境变量。
	ProxyURL string
	// ShellDomain ECH 外壳域名（默认 cloudflare-ech.com）。
	ShellDomain string
}

// fetchECHConfig 通过 DoH 获取 ECH 配置（type=65 SVCB 记录），带 TTL 缓存。
func fetchECHConfig(ctx context.Context, cfg Config) ([]byte, error) {
	dohURL := cfg.DoHURL
	if dohURL == "" {
		dohURL = DefaultDoHURL
	}
	domain := cfg.ShellDomain
	if domain == "" {
		domain = "cloudflare-ech.com"
	}
	if cached := getCachedECH(domain); cached != nil {
		return cached, nil
	}

	u := fmt.Sprintf("%s?name=%s&type=65", dohURL, url.QueryEscape(domain))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")

	client := &http.Client{Timeout: 8 * time.Second, Transport: proxyTransport(cfg)}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("DoH %s: %w", dohURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH %s failed: %d", dohURL, resp.StatusCode)
	}

	var d dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("DoH %s decode: %w", dohURL, err)
	}

	for _, ans := range d.Answer {
		if ans.Type != 65 {
			continue
		}
		ttl := ans.TTL
		if ttl <= 0 {
			ttl = 300
		}
		var cfgBytes []byte
		if m := echSvcParamRE.FindStringSubmatch(ans.Data); len(m) > 1 {
			cfgBytes, err = base64.StdEncoding.DecodeString(m[1])
		} else if m := wsvcWireRE.FindStringSubmatch(ans.Data); len(m) > 1 {
			hexData := nonHexRE.ReplaceAllString(m[2], "")
			var wire []byte
			wire, err = hex.DecodeString(hexData)
			if err == nil {
				cfgBytes, err = parseSVCBWire(wire)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("ECH parse error: %w", err)
		}
		if len(cfgBytes) > 0 {
			setCachedECH(domain, cfgBytes, ttl)
			return cfgBytes, nil
		}
	}
	return nil, fmt.Errorf("no ECH config found for %s from %s", domain, dohURL)
}

// proxyTransport 构造走代理的 http.Transport（DoH 用）。
func proxyTransport(cfg Config) *http.Transport {
	tr := &http.Transport{
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        20,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 8 * time.Second,
	}
	if p := effectiveProxy(cfg.ProxyURL); p != "" {
		pu, err := url.Parse(p)
		if err == nil {
			tr.Proxy = http.ProxyURL(pu)
		}
	}
	return tr
}

// effectiveProxy 返回要使用的代理：显式配置优先，否则读 HTTPS_PROXY。
func effectiveProxy(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv("HTTPS_PROXY")
}

// dialConn 拨号到 host:port，支持 HTTP 代理（CONNECT 隧道）。
func dialConn(ctx context.Context, network, addr string, proxy string) (net.Conn, error) {
	if proxy == "" {
		d := &net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	// HTTP 代理：先连代理，再发 CONNECT 建隧道
	pu, err := url.Parse(proxy)
	if err != nil {
		return nil, err
	}
	proxyAddr := pu.Host
	d := &net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, network, proxyAddr)
	if err != nil {
		return nil, err
	}
	// 发送 CONNECT 请求
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, err
	}
	// 读取响应行（简化：读第一行判断 200）
	buf := make([]byte, 0, 256)
	tmp := make([]byte, 1)
	statusLine := ""
	for {
		n, err := conn.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			if len(buf) >= 4 && string(buf[len(buf)-4:]) == "\r\n\r\n" {
				break
			}
			if len(buf) > 4096 {
				break
			}
		}
		if err != nil {
			conn.Close()
			return nil, err
		}
		_ = statusLine
	}
	// 判断状态码（第一行 "HTTP/1.1 200"）
	head := string(buf)
	rest := head
	// 去掉状态行前的可能前缀
	idx := 0
	for idx < len(rest) && rest[idx] != ' ' {
		idx++
	}
	rest = rest[idx+1:]
	codeEnd := 0
	for codeEnd < len(rest) && rest[codeEnd] >= '0' && rest[codeEnd] <= '9' {
		codeEnd++
	}
	code := rest[:codeEnd]
	if code != "200" {
		conn.Close()
		return nil, fmt.Errorf("proxy CONNECT %s failed: %s", addr, head)
	}
	return conn, nil
}

// ---- Client ----

// Client 是 ECH 域前置 HTTP 客户端。
// 所有请求 TCP 连 shellDomain，TLS 内层 SNI 为真实目标域名。
type Client struct {
	inner *http.Client
}

// New 初始化 ECH 客户端。首次调用获取 ECH 配置。
func New(cfg Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	echConfig, err := fetchECHConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch ECH config: %w", err)
	}
	return newClient(echConfig, cfg), nil
}

// newTransport 构造 ECH 域前置 transport。
func newTransport(echConfig []byte, cfg Config) *http.Transport {
	shellDomain := cfg.ShellDomain
	if shellDomain == "" {
		shellDomain = "cloudflare-ech.com"
	}
	proxy := effectiveProxy(cfg.ProxyURL)
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			rawConn, err := dialConn(ctx, network, net.JoinHostPort(shellDomain, "443"), proxy)
			if err != nil {
				return nil, fmt.Errorf("dial shell: %w", err)
			}
			tlsCfg := &tls.Config{
				ServerName:                     host,
				EncryptedClientHelloConfigList: echConfig,
				MinVersion:                     tls.VersionTLS13,
				NextProtos:                     []string{"h2", "http/1.1"},
			}
			tlsConn := tls.Client(rawConn, tlsCfg)
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				rawConn.Close()
				return nil, fmt.Errorf("TLS handshake: %w", err)
			}
			return tlsConn, nil
		},
		ForceAttemptHTTP2:   true,
		MaxIdleConns:        100,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
}

func newClient(echConfig []byte, cfg Config) *Client {
	return &Client{
		inner: &http.Client{
			Transport: newTransport(echConfig, cfg),
			Timeout:   0, // 大文件不限时
		},
	}
}

// Do 执行 HTTP 请求，经 ECH 域前置发出。
// req.Host 被设为真实目标域名（内层 SNI + HTTP Host）。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	return c.inner.Do(req)
}

// ---- 全局默认客户端 ----

var defaultClient atomic.Pointer[Client]

// InitDefault 显式初始化全局默认客户端（程序启动时调用）。
func InitDefault(cfg Config) error {
	c, err := New(cfg)
	if err != nil {
		return err
	}
	defaultClient.Store(c)
	go refreshLoop(cfg)
	return nil
}

// Do 用全局默认客户端执行 ECH 请求（首次自动初始化）。
func Do(req *http.Request) (*http.Response, error) {
	c := defaultClient.Load()
	if c == nil {
		var err error
		c, err = New(Config{})
		if err != nil {
			return nil, err
		}
		if !defaultClient.CompareAndSwap(nil, c) {
			c = defaultClient.Load()
		}
	}
	return c.Do(req)
}

var refreshCtx, refreshCancel = context.WithCancel(context.Background())

// refreshLoop 每 5 分钟刷新 ECH 配置（Cloudflare 会轮换）。
func refreshLoop(cfg Config) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-refreshCtx.Done():
			return
		case <-ticker.C:
		}
		ctx, cancel := context.WithTimeout(refreshCtx, 10*time.Second)
		echConfig, err := fetchECHConfig(ctx, cfg)
		cancel()
		if err != nil {
			continue
		}
		c := newClient(echConfig, cfg)
		if old := defaultClient.Swap(c); old != nil {
			if tr, ok := old.inner.Transport.(*http.Transport); ok {
				tr.CloseIdleConnections()
			}
		}
	}
}