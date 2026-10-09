// ECH client — ported from ech-proxy echproxy/ech/client.go (commit fc2ccb1).
//
// Adds backward-compatible Config (DoHURL/ProxyURL/ShellDomain), IP family
// control (v4/v6/auto), DoH retry, HTTP proxy support, and redirect
// interception on top of the ech-proxy baseline.
//
// Differences from ech-proxy's original:
//   - ProxyURL is honoured for both DoH and upstream (CONNECT tunnel).
//   - ShellDomain is configurable (default cloudflare-ech.com).
//   - New(cfg) and InitDefault(cfg) accept Config for backward compatibility.
package echcore

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"
)

// Client is an HTTP client based on cloudflare-ech.com ECH domain fronting.
// All requests are dispatched through cloudflare-ech.com IPs, while the true
// target domain is transmitted encrypted to Cloudflare routing via ECH.
type Client struct {
	inner *http.Client
}

// ---- ECH Configuration Cache ----

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

// ---- DNS wire format parser ----

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

// doDohRequest performs a DoH HTTP request using netdial.Transport.
// When a bootstrap IP is configured, it dials the IP directly (bypassing
// local DNS). When a proxy is configured, it uses a proxy-aware transport.
func doDohRequest(ctx context.Context, urlStr string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/dns-json")

	tr := Transport()
	cfg := currentConfig()
	dialIP := cfg.dialIP
	proxyURL := cfg.proxyURL
	if dialIP != "" {
		tr = tr.Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			_, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			dialer := &net.Dialer{Timeout: dohTimeout}
			return dialer.DialContext(ctx, network, net.JoinHostPort(dialIP, port))
		}
		uParsed, _ := url.Parse(urlStr)
		if uParsed != nil {
			tr.TLSClientConfig = &tls.Config{ServerName: uParsed.Host}
		}
	} else if cfg.ipMode != "" {
		tr = tr.Clone()
		tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			return dialTCP(ctx, host, port, cfg.ipMode, dohTimeout)
		}
	} else if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		tr = tr.Clone()
		tr.Proxy = http.ProxyURL(u)
	}
	dohClient := &http.Client{Transport: tr, Timeout: dohTimeout}
	return dohClient.Do(req)
}

// ---- ECH config fetch with retry ----

// Regular expressions used by fetchECHConfig: compiled at package level to
// avoid recompiling on each DoH request (refresh runs every 5 minutes).
var (
	echSvcParamRE = regexp.MustCompile(`ech="?([A-Za-z0-9+/=]+)"?`)
	wsvcWireRE    = regexp.MustCompile(`\\#\s+(\d+)\s+([0-9a-fA-F\s]+)`)
	nonHexRE      = regexp.MustCompile(`[^0-9a-fA-F]`)
)

const (
	shellDomain     = "cloudflare-ech.com"
	dialTimeout     = OpTimeout
	dohTimeout      = OpTimeout
	defaultDohURL   = "https://moonchan.xyz/doh"
)

func fetchECHConfig(ctx context.Context, domain string) ([]byte, error) {
	if cached := getCachedECH(domain); cached != nil {
		return cached, nil
	}
	dohURL := currentConfig().dohURL
	cfg, err := Retry(ctx, RetryAttempts, RetryBackoff, func() ([]byte, error) {
		return fetchECHConfigOnce(ctx, domain, dohURL)
	})
	if err != nil {
		return nil, fmt.Errorf("DoH %s (after %d attempts): %w", dohURL, RetryAttempts, err)
	}
	return cfg, nil
}

// QueryDoH executes a unified DoH DNS query.
func QueryDoH(ctx context.Context, name string, qtype int) (*http.Response, error) {
	dohURL := currentConfig().dohURL
	u := fmt.Sprintf("%s?name=%s&type=%d", dohURL, url.QueryEscape(name), qtype)
	return doDohRequest(ctx, u)
}

func fetchECHConfigOnce(ctx context.Context, domain, dohURL string) ([]byte, error) {
	u := fmt.Sprintf("%s?name=%s&type=65", dohURL, url.QueryEscape(domain))
	resp, err := doDohRequest(ctx, u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("DoH %s failed: %d", dohURL, resp.StatusCode)
	}

	var d dohResponse
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("DoH %s decode: %w", dohURL, err)
	}

	echRe := echSvcParamRE
	wireRe := wsvcWireRE

	for _, ans := range d.Answer {
		if ans.Type != 65 {
			continue
		}
		ttl := ans.TTL
		if ttl <= 0 {
			ttl = 300
		}

		var cfg []byte
		if m := echRe.FindStringSubmatch(ans.Data); len(m) > 1 {
			cfg, err = base64.StdEncoding.DecodeString(m[1])
		} else if m := wireRe.FindStringSubmatch(ans.Data); len(m) > 1 {
			hexData := nonHexRE.ReplaceAllString(m[2], "")
			var wire []byte
			wire, err = hex.DecodeString(hexData)
			if err == nil {
				cfg, err = parseSVCBWire(wire)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("ECH parse error: %w", err)
		}
		if len(cfg) > 0 {
			setCachedECH(domain, cfg, ttl)
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("no ECH config found for %s from %s", domain, dohURL)
}

// ---- Config ----

// config stores mutable global configuration.
// Uses atomic.Pointer for lock-free safe read/write.
type config struct {
	dohURL    string
	dialIP    string
	ipMode    string
	proxyURL  string
	shellDom  string
}

var cfgPtr atomic.Pointer[config]

func currentConfig() *config {
	if c := cfgPtr.Load(); c != nil {
		return c
	}
	c := &config{dohURL: defaultDohURL, shellDom: shellDomain}
	cfgPtr.Store(c)
	return c
}

// SetIPMode sets the fallback IP protocol preference.
// mode is "v4", "v6", or "" (automatic).
func SetIPMode(mode string) {
	nc := *currentConfig()
	switch mode {
	case "v4", "v6":
		nc.ipMode = mode
	default:
		nc.ipMode = ""
	}
	cfgPtr.Store(&nc)

	clientsMu.Lock()
	defer clientsMu.Unlock()
	for _, c := range clients {
		if tr, ok := c.inner.Transport.(*http2.Transport); ok {
			tr.CloseIdleConnections()
		}
	}
	clients = map[string]*Client{}
}

// SetDohURL overrides the DoH URL entirely.
func SetDohURL(url string) {
	nc := *currentConfig()
	nc.dohURL = url
	nc.dialIP = ""
	cfgPtr.Store(&nc)
}

// SetDoHConfig sets DoH via host + bootstrap IP for direct-IP dialing.
func SetDoHConfig(host, bootstrapIP string) {
	nc := *currentConfig()
	nc.dohURL = fmt.Sprintf("https://%s/doh", host)
	nc.dialIP = bootstrapIP
	cfgPtr.Store(&nc)
}

// SetProxyURL sets the HTTP proxy URL for DoH and upstream connections.
func SetProxyURL(proxyURL string) {
	nc := *currentConfig()
	nc.proxyURL = proxyURL
	cfgPtr.Store(&nc)
}

// SetShellDomain sets the ECH shell domain (default cloudflare-ech.com).
func SetShellDomain(domain string) {
	if domain == "" {
		return
	}
	nc := *currentConfig()
	nc.shellDom = domain
	cfgPtr.Store(&nc)
}

func resolvePreferredIP(ctx context.Context, host, ipMode string) (string, error) {
	mode := ipMode
	if mode == "" || mode == "auto" {
		mode = currentConfig().ipMode
	}
	if mode == "" || mode == "auto" {
		return "", nil
	}
	ips, err := Dialer().Resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", host, err)
	}
	for _, addr := range ips {
		if mode == "v4" && addr.IP.To4() != nil {
			return addr.IP.String(), nil
		}
		if mode == "v6" && addr.IP.To4() == nil && addr.IP.To16() != nil {
			return addr.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no %s address for %s", mode, host)
}

// egressFamilyOnce logs the IP family the OS actually resolves for the
// upstream dial when IP_MODE is unset.
var egressFamilyOnce sync.Once

// dialTCP dials the shell domain according to ipMode preference. When
// ipMode is empty or "auto", it uses the default Go resolver. When v4/v6
// is specified, it resolves the preferred IP first. When proxyURL is set,
// it goes through an HTTP CONNECT tunnel.
func dialTCP(ctx context.Context, host, port, ipMode string, timeout time.Duration) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: timeout}
	mode := ipMode
	if mode == "" || mode == "auto" {
		mode = currentConfig().ipMode
	}

	// Proxy support: if proxyURL is set, connect through the proxy.
	proxyURL := currentConfig().proxyURL
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("parse proxy URL: %w", err)
		}
		proxyAddr := u.Host

		// If ipMode is specified, resolve the IP first for the CONNECT target.
		var targetAddr string
		if mode != "" && mode != "auto" {
			ip, err := resolvePreferredIP(ctx, host, mode)
			if err != nil {
				return nil, err
			}
			targetAddr = net.JoinHostPort(ip, port)
		} else {
			targetAddr = net.JoinHostPort(host, port)
		}

		rawConn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("proxy connect %s: %w", proxyAddr, err)
		}
		// HTTP CONNECT tunnel.
		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, proxyAddr)
		if _, err := io.WriteString(rawConn, connectReq); err != nil {
			rawConn.Close()
			return nil, err
		}
		br := bufio.NewReader(rawConn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			rawConn.Close()
			return nil, err
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			rawConn.Close()
			return nil, fmt.Errorf("proxy CONNECT: %s", resp.Status)
		}
		if addr, ok := rawConn.RemoteAddr().(*net.TCPAddr); ok {
			family := "IPv6"
			if addr.IP.To4() != nil {
				family = "IPv4"
			}
			egressFamilyOnce.Do(func() {
				log.Printf("Upstream egress via proxy: %s -> %s (target %s)", family, proxyAddr, targetAddr)
			})
		}
		return rawConn, nil
	}

	// Direct connection.
	if mode != "" && mode != "auto" {
		ip, err := resolvePreferredIP(ctx, host, mode)
		if err != nil {
			return nil, err
		}
		addr := net.JoinHostPort(ip, port)
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			return nil, err
		}
		if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			family := "IPv6"
			if tcpAddr.IP.To4() != nil {
				family = "IPv4"
			}
			egressFamilyOnce.Do(func() {
				log.Printf("Upstream egress: %s via %s (IP_MODE=%s)", family, tcpAddr.String(), mode)
			})
		}
		return conn, nil
	}

	// Auto mode: use default Go resolver.
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return nil, err
	}
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		family := "IPv6"
		if tcpAddr.IP.To4() != nil {
			family = "IPv4"
		}
		egressFamilyOnce.Do(func() {
			log.Printf("Upstream egress: %s via %s (IP_MODE=%s, family chosen by the OS)",
				family, tcpAddr.String(), mode)
			if mode == "" {
				log.Printf("Some upstreams reject IPv6 sources (e.g. pixiv returns a static 403 "+
					"'Access blocked'); set ip_mode: v4 in config to pin the family.")
			}
		})
	}
	return conn, nil
}

// CheckDualStack reports whether the probe hostname publishes A / AAAA records.
func CheckDualStack(ctx context.Context) (hasV4, hasV6 bool) {
	ips, err := Dialer().Resolver.LookupIPAddr(ctx, "moonchan.xyz")
	if err != nil {
		return false, false
	}
	for _, addr := range ips {
		if addr.IP.To4() != nil {
			hasV4 = true
		} else if addr.IP.To16() != nil {
			hasV6 = true
		}
	}
	return
}

// newTransport constructs an ECH domain fronting transport using utls with
// HelloChrome_120 to impersonate a real Chrome browser TLS fingerprint.
// Cloudflare checks JA3/JA4 fingerprints to detect non-browser clients;
// using crypto/tls directly exposes the Go runtime fingerprint.
// utls v1.8.2 supports EncryptedClientHelloConfigList, so ECH encryption
// is preserved.
func newTransport(echConfig []byte, ipMode string) *http2.Transport {
	return &http2.Transport{
		AllowHTTP: false,
		DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			host, _, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}

			shDom := currentConfig().shellDom
			uCfg := &utls.Config{
				ServerName:                     host,
				EncryptedClientHelloConfigList: echConfig,
				MinVersion:                     tls.VersionTLS13,
				NextProtos:                     []string{"h2", "http/1.1"},
			}

			// Retry dialing shell domain + TLS handshake to handle transient RST/timeouts.
			conn, err := Retry(ctx, RetryAttempts, RetryBackoff, func() (net.Conn, error) {
				rawConn, derr := dialTCP(ctx, shDom, "443", ipMode, dialTimeout)
				if derr != nil {
					return nil, derr
				}
				uConn := utls.UClient(rawConn, uCfg, utls.HelloChrome_120)
				if herr := uConn.HandshakeContext(ctx); herr != nil {
					rawConn.Close()
					return nil, herr
				}
				if uConn.ConnectionState().NegotiatedProtocol != "h2" {
					rawConn.Close()
					return nil, fmt.Errorf("upstream did not negotiate h2")
				}
				return uConn, nil
			})
			if err != nil {
				return nil, fmt.Errorf("dial shell: %w", err)
			}
			return conn, nil
		},
	}
}

// newClient constructs an ECH client.
// No global Timeout is set: avoiding prematurely terminating >30s large
// file/video downloads.
func newClient(echConfig []byte, ipMode string) *Client {
	return &Client{
		inner: &http.Client{
			Transport: newTransport(echConfig, ipMode),
			Timeout:   0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ---- Config struct for backward compatibility ----

// Config is the client configuration. Backward-compatible with the old
// back/ech Config, with new fields for IP mode, bootstrap IP, and shell domain.
type Config struct {
	// DoHURL is the endpoint for fetching ECH config (defaults to DefaultDoHURL).
	DoHURL string
	// ProxyURL is the HTTP proxy ("http://host:port"); empty reads HTTPS_PROXY env var.
	ProxyURL string
	// ShellDomain is the ECH shell domain (defaults to cloudflare-ech.com).
	ShellDomain string
	// IPMode is the IP family preference: "v4", "v6", or "" (auto).
	IPMode string
	// BootstrapIP is the DoH bootstrap IP for direct-IP dialing.
	BootstrapIP string
}

// DefaultDoHURL is the default DoH endpoint.
const DefaultDoHURL = defaultDohURL

// applyConfig applies a Config to the global config state.
func applyConfig(cfg Config) {
	cur := *currentConfig()
	if cfg.DoHURL != "" {
		cur.dohURL = cfg.DoHURL
		cur.dialIP = ""
	}
	if cfg.BootstrapIP != "" {
		cur.dialIP = cfg.BootstrapIP
	}
	if cfg.ShellDomain != "" {
		cur.shellDom = cfg.ShellDomain
	}
	if cfg.IPMode != "" {
		cur.ipMode = cfg.IPMode
	}
	if cfg.ProxyURL != "" {
		cur.proxyURL = cfg.ProxyURL
	}
	cfgPtr.Store(&cur)
}

// ---- Public API ----

// New creates an ECH domain-fronting HTTP client with the given Config.
// Backward-compatible with the old ech.New(cfg). On first call, it
// retrieves the shell domain's ECH keys via DoH and caches them.
func New(cfg Config) (*Client, error) {
	applyConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), dohTimeout)
	defer cancel()

	shDom := currentConfig().shellDom
	echConfig, err := fetchECHConfig(ctx, shDom)
	if err != nil {
		return nil, fmt.Errorf("fetch ECH config: %w", err)
	}
	return newClient(echConfig, ""), nil
}

// NewDefault creates an ECH client using the global config (no Config arg).
// This is the ech-proxy-style API: configure globals via SetIPMode/SetProxyURL
// etc., then call NewDefault().
func NewDefault() (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), dohTimeout)
	defer cancel()

	shDom := currentConfig().shellDom
	echConfig, err := fetchECHConfig(ctx, shDom)
	if err != nil {
		return nil, fmt.Errorf("fetch ECH config: %w", err)
	}
	return newClient(echConfig, ""), nil
}

// Do executes an HTTP request dispatched via ECH domain fronting.
// req.Host is set to the true target domain for Inner SNI.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	return c.inner.Do(req)
}

// DoWithAddr is like Do, but allows specifying a target domain (for
// direct-IP scenarios).
func (c *Client) DoWithAddr(req *http.Request, host string) (*http.Response, error) {
	req.Host = host
	return c.inner.Do(req)
}

// ---- Convenience Functions ----

var (
	clientsMu     sync.Mutex
	clients       = map[string]*Client{}
	lastECHConfig []byte
)

func getECHClient(ipMode string) (*Client, error) {
	mode := strings.ToLower(strings.TrimSpace(ipMode))
	if mode == "" || mode == "auto" {
		mode = currentConfig().ipMode
	}
	if mode != "v4" && mode != "v6" {
		mode = "auto"
	}

	clientsMu.Lock()
	defer clientsMu.Unlock()

	if c, ok := clients[mode]; ok {
		return c, nil
	}

	if len(lastECHConfig) == 0 {
		ctx, cancel := context.WithTimeout(context.Background(), dohTimeout)
		defer cancel()
		shDom := currentConfig().shellDom
		cfg, err := fetchECHConfig(ctx, shDom)
		if err != nil {
			return nil, fmt.Errorf("fetch ECH config: %w", err)
		}
		lastECHConfig = cfg
	}

	c := newClient(lastECHConfig, mode)
	clients[mode] = c
	return c, nil
}

// Do executes an ECH request using a client configured for the specified
// ipMode ("v4", "v6", or "auto"). If ipMode is omitted, it falls back to
// the global configuration.
func Do(req *http.Request, ipMode ...string) (*http.Response, error) {
	mode := ""
	if len(ipMode) > 0 {
		mode = ipMode[0]
	}
	c, err := getECHClient(mode)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

// InitDefault initializes the global default ECH client with the given Config.
// Backward-compatible with the old ech.InitDefault(ech.Config{...}).
// Pass Config{} for defaults (uses global setters if previously called).
func InitDefault(cfg Config) error {
	applyConfig(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), dohTimeout)
	defer cancel()
	shDom := currentConfig().shellDom
	echCfg, err := fetchECHConfig(ctx, shDom)
	if err != nil {
		return err
	}
	clientsMu.Lock()
	lastECHConfig = echCfg
	clientsMu.Unlock()

	_, err = getECHClient("")
	if err != nil {
		return err
	}
	go refreshLoop()
	return nil
}

var refreshCtx, refreshCancel = context.WithCancel(context.Background())

func refreshLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-refreshCtx.Done():
			return
		case <-ticker.C:
		}

		ctx, cancel := context.WithTimeout(refreshCtx, dohTimeout)
		shDom := currentConfig().shellDom
		echConfig, err := fetchECHConfig(ctx, shDom)
		cancel()
		if err != nil {
			continue
		}

		clientsMu.Lock()
		lastECHConfig = echConfig
		oldClients := make([]*Client, 0, len(clients))
		for mode, old := range clients {
			oldClients = append(oldClients, old)
			clients[mode] = newClient(echConfig, mode)
		}
		clientsMu.Unlock()

		for _, old := range oldClients {
			if tr, ok := old.inner.Transport.(*http2.Transport); ok {
				tr.CloseIdleConnections()
			}
		}
	}
}

// StopRefresh cancels the ECH config refresh loop.
func StopRefresh() {
	refreshCancel()
}
