// Package ech provides an ECH (Encrypted Client Hello) HTTP client.
//
// Background: Twitter's media CDN (video-cf.twimg.com) is blocked from mainland
// China, but it is behind Cloudflare. ECH domain fronting uses cloudflare-ech.com
// as a shell: TCP connects to cloudflare-ech.com (not blocked), and during the TLS
// handshake, the ECH-encrypted ClientHello tells the Cloudflare edge the real
// target domain (video-cf.twimg.com), which routes to the target. Because the
// SNI in the ClientHello is encrypted by ECH, the GFW only sees the shell domain
// and allows it.
//
// This package does not depend on wintools and is independently implemented (used by
// the peerdrive production media-node).
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

// ---- ECH config cache ----

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

// ---- DoH for fetching ECH config ----

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

// DefaultDoHURL is the default DoH endpoint (moonchan.xyz self-hosted DNS over HTTPS,
// returns SVCB/ECH records for cloudflare-ech.com).
const DefaultDoHURL = "https://moonchan.xyz/doh"

// Config is the client configuration.
type Config struct {
	// DoHURL is the endpoint for fetching ECH config (defaults to DefaultDoHURL).
	DoHURL string
	// ProxyURL is the HTTP proxy ("http://host:port"); empty reads HTTPS_PROXY env var.
	ProxyURL string
	// ShellDomain is the ECH shell domain (defaults to cloudflare-ech.com).
	ShellDomain string
}

// fetchECHConfig fetches ECH config via DoH (type=65 SVCB records), with TTL caching.
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

// proxyTransport constructs an http.Transport using a proxy (for DoH).
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

// effectiveProxy returns the proxy to use: explicit config takes priority, otherwise reads HTTPS_PROXY.
func effectiveProxy(explicit string) string {
	if explicit != "" {
		return explicit
	}
	return os.Getenv("HTTPS_PROXY")
}

// dialConn dials host:port, supporting HTTP proxy (CONNECT tunnel).
func dialConn(ctx context.Context, network, addr string, proxy string) (net.Conn, error) {
	if proxy == "" {
		d := &net.Dialer{Timeout: 10 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	// HTTP proxy: connect to the proxy first, then send CONNECT to establish the tunnel
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
	// Send CONNECT request
	connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", addr, addr)
	if _, err := conn.Write([]byte(connectReq)); err != nil {
		conn.Close()
		return nil, err
	}
	// Read the response (simplified: read the first line to check for 200)
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
	// Parse the status code (first line "HTTP/1.1 200")
	head := string(buf)
	rest := head
	// Strip any possible prefix before the status line
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

// Client is an ECH domain-fronting HTTP client.
// All requests TCP-connect to shellDomain; the inner TLS SNI is the real target domain.
type Client struct {
	inner *http.Client
}

// New initializes an ECH client. The first call fetches ECH config.
func New(cfg Config) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	echConfig, err := fetchECHConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("fetch ECH config: %w", err)
	}
	return newClient(echConfig, cfg), nil
}

// newTransport constructs an ECH domain-fronting transport.
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
			Timeout:   0, // No timeout for large files
		},
	}
}

// Do executes an HTTP request via ECH domain fronting.
// req.Host is set to the real target domain (inner SNI + HTTP Host).
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	return c.inner.Do(req)
}

// ---- Global default client ----

var defaultClient atomic.Pointer[Client]

// InitDefault explicitly initializes the global default client (call at program startup).
func InitDefault(cfg Config) error {
	c, err := New(cfg)
	if err != nil {
		return err
	}
	defaultClient.Store(c)
	go refreshLoop(cfg)
	return nil
}

// Do executes an ECH request using the global default client (auto-initializes on first use).
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

// refreshLoop refreshes ECH config every 5 minutes (Cloudflare rotates them).
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
