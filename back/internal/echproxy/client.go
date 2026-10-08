package echproxy

import (
	"context"
	"crypto/sha1"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// xVersionSecret is the X-Version signing constant used by the iwara API
// file-resolution endpoint. It is hardcoded in the open-source iwara
// downloaders (IwaraDownloadTool, iwaradl, iwara_spider_lib); it is part
// of iwara's public client API surface, not a server secret.
//
// The X-Version header is computed as:
//
//	SHA1(fileID + "_" + expires + "_" + xVersionSecret)
//
// where expires is the "expires" query parameter of the fileUrl returned by
// the /video/{id} endpoint. The header authenticates the resolution request
// so that iwara's CDN URL is issued to a legitimate client.
const xVersionSecret = "mSvL05GfEmeEmsEYfGCnVpEjYgTJraJN"

// IwaraClient resolves iwara video IDs to direct CDN download URLs and
// injects the user's iwara login cookie into every request.
//
// The API surface (https://api.iwara.tv) is the same one used by the
// open-source iwara downloaders:
//
//	GET /video/{id}                    → VideoInfo.FileUrl
//	GET {FileUrl}  (X-Version header) → []ResolutionInfo
//	GET "https:" + ResolutionInfo.Src.Download  → the video bytes
//
// The download URL itself is a Cloudflare-fronted CDN URL; it is returned
// as-is so callers can decide whether to fetch it directly or through
// ech-proxy (see Rewriter).
type IwaraClient struct {
	cfg    *ModuleConfig
	client *http.Client
	// baseURL is the API base URL (default "https://api.iwara.tv"). Override
	// for testing.
	baseURL string
	// RewriteHosts, when non-nil, rewrites iwara.tv hosts in every request
	// URL before sending (routes the API calls through the local ech-proxy).
	// nil = direct API access (the default when ech-proxy is not running).
	RewriteHosts *Rewriter
}

// NewIwaraClient builds an IwaraClient from a ModuleConfig.
//
// The HTTP client is constructed with the iwara User-Agent and a TLS config
// that skips certificate verification for the ech-proxy entry host
// (l.moonchan.xyz), which is only reachable through the local proxy whose
// certificate is not in the system trust store. Direct api.iwara.tv access
// (no ech-proxy) keeps the default system roots, because that host is
// Cloudflare-fronted with a valid cert.
func NewIwaraClient(cfg *ModuleConfig) *IwaraClient {
	if cfg == nil {
		def := NewModuleConfig()
		cfg = &def
	}
	cfg.Normalize()
	return &IwaraClient{
		cfg:     cfg,
		client:  newEchProxyHTTPClient(cfg.ListenAddr),
		baseURL: "https://api.iwara.tv",
	}
}

// SetBaseURL overrides the API base URL (for testing).
func (c *IwaraClient) SetBaseURL(baseURL string) {
	c.baseURL = baseURL
}

// SetRewriter enables routing all iwara API requests through the local
// ech-proxy by rewriting the request URL host. Pass nil to disable.
func (c *IwaraClient) SetRewriter(r *Rewriter) {
	c.RewriteHosts = r
}

// newEchProxyHTTPClient builds an http.Client that:
//   - dials 127.0.0.1:<port> (the local ech-proxy) instead of the real host,
//     setting the TLS ServerName to the original URL host (SNI = entry host);
//   - skips TLS verification only for the ech-proxy entry host suffix
//     (InsecureSkipVerify is scoped via GetCertificate, not globally).
//
// When the URL host is not an ech-proxy entry host (e.g. api.iwara.tv
// accessed directly), the default transport behaviour is preserved: system
// certificate roots, no host rewrite.
func newEchProxyHTTPClient(listenAddr string) *http.Client {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil || port == "" {
		port = DefaultEntryPort
	}
	dialAddr := net.JoinHostPort(host, port)

	tr := &http.Transport{
		ForceAttemptHTTP2: true,
		MaxIdleConns:      32,
		IdleConnTimeout:   90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			// addr is "<host>:<port>" as the caller resolved it. If the host
			// is an ech-proxy entry host, redirect the TCP dial to the local
			// proxy but keep the SNI as the entry host (ech-proxy presents a
			// cert for *.l.moonchan.xyz).
			connHost, connPort, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				connHost = addr
				connPort = ""
			}
			if isEchProxyEntryHost(connHost, DefaultEntrySuffix) {
				raw, dialErr := net.DialTimeout("tcp", dialAddr, 10*time.Second)
				if dialErr != nil {
					return nil, fmt.Errorf("echproxy dial %s: %w", dialAddr, dialErr)
				}
				return tls.Client(raw, &tls.Config{
					ServerName:         connHost,
					InsecureSkipVerify: true, // local proxy, self-signed
					MinVersion:         tls.VersionTLS12,
				}), nil
			}
			// Direct upstream: normal dial, system roots.
			d := &net.Dialer{Timeout: 10 * time.Second}
			raw, dialErr := d.DialContext(ctx, network, net.JoinHostPort(connHost, connPort))
			if dialErr != nil {
				return nil, dialErr
			}
			return tls.Client(raw, &tls.Config{
				ServerName: connHost,
				MinVersion: tls.VersionTLS12,
			}), nil
		},
	}

	return &http.Client{
		Transport: tr,
		Timeout:   0, // no overall timeout: large file downloads stream
	}
}

// isEchProxyEntryHost reports whether host belongs to the ech-proxy entry
// namespace (e.g. "iwara-api.l.moonchan.xyz" ends with ".l.moonchan.xyz").
func isEchProxyEntryHost(host, entrySuffix string) bool {
	return strings.HasSuffix(strings.ToLower(host), "."+entrySuffix)
}

// Do sends an HTTP request to an iwara URL, injecting the iwara headers
// (Origin/X-Site/Referer) and the user's login cookie. The request URL is
// rewritten through the local ech-proxy when c.RewriteHosts is set.
func (c *IwaraClient) Do(ctx context.Context, method, rawURL string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, fmt.Errorf("iwara: new request: %w", err)
	}

	// Rewire the host through the local ech-proxy (optional).
	if c.RewriteHosts != nil {
		rewritten, _ := c.RewriteHosts.Rewrite(req.URL.String())
		if rewritten != req.URL.String() {
			if nu, perr := url.Parse(rewritten); perr == nil {
				req.URL = nu
				req.Host = nu.Host // update the Host header to match
			}
		}
	}

	// Standard iwara headers (mirror the ech-proxy upstream.json headers).
	for k, v := range IwaraHeaders(c.cfg.IWARAHost) {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")

	// Cookie import: inject the user's iwara login cookie into every request.
	if c.cfg.IWARACookie != "" {
		req.Header.Set("Cookie", c.cfg.IWARACookie)
	}

	return c.client.Do(req)
}

// ---- iwara API types (mirror the open-source iwara downloaders) ----

// VideoInfo is the response of GET https://api.iwara.tv/video/{id}.
// Only the fields the module needs are declared.
type VideoInfo struct {
	ID      string `json:"id"`
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Status  string `json:"status"`
	Rating  string `json:"rating"`
	File    struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Path string `json:"path"`
	} `json:"file"`
	FileUrl string `json:"fileUrl"`
}

// ResolutionInfo is one entry of the resolution list returned by the
// file-resolution endpoint. Src.Download is a protocol-relative CDN URL
// (e.g. "//v-f007-...-v.ihstatic.com/...").
type ResolutionInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"` // "Source" = highest quality
	Src  struct {
		View     string `json:"view"`
		Download string `json:"download"`
	} `json:"src"`
}

// ResolveDownloadURL resolves an iwara video ID to a direct CDN download URL.
//
// Flow (mirrors the open-source iwaradl / iwara_spider_lib implementations):
//  1. GET https://api.iwara.tv/video/{videoID} → VideoInfo.FileUrl
//  2. GET {FileUrl} with header X-Version = SHA1(fileID + "_" + expires + "_"
//     + xVersionSecret) → []ResolutionInfo
//  3. download URL = "https:" + resolution("Source").Src.Download
//
// expires is the "expires" query parameter of FileUrl (a Unix timestamp).
// The X-Version header binds the request to that timestamp so the CDN URL
// is issued to a legitimate, time-bounded client.
//
// Returns the download URL (scheme-prefixed, e.g. "https://v-...ihstatic.com/...")
// and the resolution name ("Source" when the highest quality is available).
func (c *IwaraClient) ResolveDownloadURL(ctx context.Context, videoID string) (string, string, error) {
	if strings.TrimSpace(videoID) == "" {
		return "", "", fmt.Errorf("iwara: empty video ID")
	}

	// Step 1: video info.
	videoURL, _ := url.Parse(c.baseURL + "/video/" + url.PathEscape(videoID))
	resp, err := c.Do(ctx, http.MethodGet, videoURL.String(), nil)
	if err != nil {
		return "", "", fmt.Errorf("iwara: video info: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("iwara: video info: HTTP %d", resp.StatusCode)
	}
	var info VideoInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return "", "", fmt.Errorf("iwara: video info decode: %w", err)
	}
	if info.FileUrl == "" {
		return "", "", fmt.Errorf("iwara: video %q has no fileUrl", videoID)
	}
	if info.File.ID == "" {
		// Fallback: the API sometimes omits file.id; derive it from the path.
		if i := strings.LastIndex(info.FileUrl, "/"); i >= 0 {
			info.File.ID = info.FileUrl[i+1:]
		}
	}

	// Step 2: resolve the CDN URL.
	parsed, err := url.Parse(info.FileUrl)
	if err != nil {
		return "", "", fmt.Errorf("iwara: fileUrl parse: %w", err)
	}
	expires := parsed.Query().Get("expires")
	if expires == "" {
		return "", "", fmt.Errorf("iwara: fileUrl has no expires parameter")
	}
	xversion := SHA1Hex(info.File.ID + "_" + expires + "_" + xVersionSecret)

	xreq, err := http.NewRequestWithContext(ctx, http.MethodGet, info.FileUrl, nil)
	if err != nil {
		return "", "", fmt.Errorf("iwara: resolution request: %w", err)
	}
	// Send the resolution request through the same path (cookie + headers +
	// optional ech-proxy rewrite).
	if c.RewriteHosts != nil {
		rewritten, _ := c.RewriteHosts.Rewrite(xreq.URL.String())
		if rewritten != xreq.URL.String() {
			if nu, perr := url.Parse(rewritten); perr == nil {
				xreq.URL = nu
			}
		}
	}
	for k, v := range IwaraHeaders(c.cfg.IWARAHost) {
		xreq.Header.Set(k, v)
	}
	if c.cfg.IWARACookie != "" {
		xreq.Header.Set("Cookie", c.cfg.IWARACookie)
	}
	xreq.Header.Set("X-Version", xversion)
	xreq.Header.Set("Accept", "application/json, text/plain, */*")
	xreq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.0.0 Safari/537.36")

	xresp, err := c.client.Do(xreq)
	if err != nil {
		return "", "", fmt.Errorf("iwara: resolution: %w", err)
	}
	defer xresp.Body.Close()
	if xresp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("iwara: resolution: HTTP %d", xresp.StatusCode)
	}
	var resolutions []ResolutionInfo
	if err := json.NewDecoder(xresp.Body).Decode(&resolutions); err != nil {
		return "", "", fmt.Errorf("iwara: resolution decode: %w", err)
	}

	// Step 3: pick the "Source" resolution (highest quality); fall back to
	// the first entry when the list is empty or has no "Source" entry.
	var dl string
	var name string
	for _, r := range resolutions {
		if r.Name == "Source" {
			dl = "https:" + r.Src.Download
			name = r.Name
			break
		}
	}
	if dl == "" && len(resolutions) > 0 {
		dl = "https:" + resolutions[0].Src.Download
		name = resolutions[0].Name
	}
	if dl == "" {
		return "", "", fmt.Errorf("iwara: no download URL in resolution list (length %d)", len(resolutions))
	}
	return dl, name, nil
}

// ParseVideoID extracts an iwara video ID from a www.iwara.tv/videos/{id}
// URL (or a bare video ID). Returns the ID and true on success.
func ParseVideoID(rawURL string) (string, bool) {
	if rawURL == "" {
		return "", false
	}
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host := strings.ToLower(u.Host)
		// Match www.iwara.tv, iwara.tv (bare), and any *.iwara.tv subdomain.
		if host == DefaultIWARAHost || host == DefaultUpstreamSuffix || strings.HasSuffix(host, "."+DefaultUpstreamSuffix) {
			path := strings.TrimPrefix(u.Path, "/videos/")
			if path != u.Path {
				id := strings.SplitN(path, "/", 2)[0]
				if id != "" {
					return id, true
				}
			}
			return "", false
		}
	}
	// Bare video ID (alphanumeric, possibly with hyphens).
	if strings.ContainsAny(rawURL, "/?#") {
		return "", false
	}
	if len(rawURL) >= 3 && len(rawURL) <= 64 {
		return rawURL, true
	}
	return "", false
}

// SHA1Hex computes the SHA1 hex digest of s. (X-Version uses SHA1, not
// SHA256 — see the open-source iwara downloaders.)
func SHA1Hex(s string) string {
	h := sha1.New()
	h.Write([]byte(s))
	return hex.EncodeToString(h.Sum(nil))
}

// IntFromTime converts a Unix timestamp string to int64, for error messages.
func IntFromTime(s string) (int64, error) {
	return strconv.ParseInt(s, 10, 64)
}

// Close releases the client's idle connections.
func (c *IwaraClient) Close() {
	if tr, ok := c.client.Transport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
}
