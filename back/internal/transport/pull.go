// Package transport — pull: lets a node fetch a URL on behalf of the caller and ingest it.
//
// Why a separate verb instead of reusing upload: upload is "caller has content, push to me";
// pull is "caller only has an address, let the node fetch it itself". The consumer is a
// static panel that may only have a link — fetching it back can only be done by a node with
// network capability.
//
// This is the first "passive outbound request" in this node, so it's inherently a risk
// surface: anyone who can connect can specify a URL, equivalent to using the node as a proxy.
// Three constraints are all mandatory:
//  1. Must pass the PSK gate (gate.go servedVerbs) — nodes with keys configured can only be
//     used by key holders;
//  2. SSRF protection (guardPullURL): reject non-http(s), reject internal/local/link-local
//     addresses, and every redirect hop must be re-validated (otherwise a 302 bypasses the
//     first-hop check);
//  3. Size and timeout limits: streaming truncation to MaxUploadBytes, timeout enforced by
//     context.
package transport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"peerdrive/internal/log"
)

// pullMaxBytes fallback limit when MaxUploadBytes is not configured (100MB).
// Without this limit, a 10GB link could fill up the node's disk — pulling doesn't give
// the caller pace control like upload does; only the receiver can self-enforce.
const pullMaxBytes = 100 * 1024 * 1024

// pullTimeout overall timeout for a single pull (including redirects and reading body).
const pullTimeout = 5 * time.Minute

// pullMaxRedirects maximum redirect hops. Each hop must go through guardPullURL; too many
// hops waste resources and increase the bypass surface.
const pullMaxRedirects = 5

// servePull handles pull: this node downloads URL content and registers it in the local file
// index.
// Request {type:"pull", url, name?, reqId} → Response {type:"pulled", hash,size,name,path,reqId} | err
func (s *PeerJSService) servePull(c Session, r dcResp) {
	if strings.TrimSpace(r.URL) == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "url required", ReqID: r.ReqID})
		return
	}
	u, err := url.Parse(r.URL)
	if err != nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "pull: cannot parse URL: " + err.Error(), ReqID: r.ReqID})
		return
	}
	if err := pullGuard(u); err != nil {
		// Reason must be clear: operators troubleshooting SSRF blocks rely entirely on this
		// message; a vague "forbidden" would be mistaken for a network issue, leading to
		// repeated retries.
		_ = c.SendJSON(dcResp{Type: "err", Msg: "pull: " + err.Error(), ReqID: r.ReqID})
		return
	}

	fi, err := s.fetchIntoIndex(u.String(), pullName(r.Name, u), s.pullMaxBytes())
	if err != nil {
		log.LogWarn("peerjs: pull %s failed: %v", u.Host, err)
		_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: r.ReqID})
		return
	}
	log.LogInfo("peerjs: pulled url=%s hash=%s size=%d", u.String(), fi.Hash, fi.Size)
	_ = c.SendJSON(dcResp{
		Type:  "pulled",
		Hash:  fi.Hash,
		Total: fi.Size,
		Name:  fi.Name,
		Path:  fi.Path,
		ReqID: r.ReqID,
	})
}

// pullMaxBytes gets the configured limit (uses fallback constant when not configured).
func (s *PeerJSService) pullMaxBytes() int64 {
	if s.cfg != nil && s.cfg.MaxUploadBytes > 0 {
		return s.cfg.MaxUploadBytes
	}
	return pullMaxBytes
}

// pullName determines the ingestion filename: uses caller-provided name if given
// (WriteFile internally sanitizes); otherwise takes the last path segment of the URL — if
// that's not available, falls back to host, ensuring no unnamed entries in the index.
func pullName(name string, u *url.URL) string {
	if n := strings.TrimSpace(name); n != "" {
		return n
	}
	if base := path.Base(u.Path); base != "" && base != "." && base != "/" {
		return base
	}
	return u.Host
}

// fetchIntoIndex pulls a URL and writes it into the local file index, returning the
// registration result.
//
// Entirely streaming (no full in-memory buffer): URL size is not under our control; keeping
// everything in memory would let a 4GB link OOM the node. Truncate on exceed, no half-files.
func (s *PeerJSService) fetchIntoIndex(rawURL, name string, maxBytes int64) (*FileInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout)
	defer cancel()

	client := &http.Client{
		Timeout: pullTimeout,
		// Per-hop redirect validation: checking only the first hop lets a public URL 302
		// to 127.0.0.1 penetrate into the internal network.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= pullMaxRedirects {
				return fmt.Errorf("redirect exceeds %d hops, aborting", pullMaxRedirects)
			}
			if err := guardPullURL(req.URL); err != nil {
				return fmt.Errorf("redirect target rejected: %w", err)
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("pull: request construction failed: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pull: request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("pull: HTTP %d", resp.StatusCode)
	}
	if cl := resp.ContentLength; cl > maxBytes {
		return nil, fmt.Errorf("pull: content %d bytes exceeds this node's limit of %d bytes", cl, maxBytes)
	}
	return s.fileIndex.WriteFile(name, &limitedPuller{r: resp.Body, limit: maxBytes})
}

// limitedPuller wraps an io.Reader: returns an error when reading exceeds the limit.
// io.LimitReader only returns EOF at the limit (silent truncation); ingestion must expose
// this as an error — otherwise an oversized URL would be ingested as "success" with the
// hash actually pointing to a truncated file.
type limitedPuller struct {
	r     io.Reader
	limit int64
	seen  int64
	fail  error
}

func (l *limitedPuller) Read(p []byte) (int, error) {
	if l.fail != nil {
		return 0, l.fail
	}
	n, err := l.r.Read(p)
	l.seen += int64(n)
	if l.seen > l.limit {
		l.fail = fmt.Errorf("pull: content exceeds this node's limit of %d bytes (abort on exceed, no half-files)", l.limit)
		return 0, l.fail
	}
	return n, err
}

// pullGuard SSRF check hook actually used by servePull. Default is guardPullURL.
//
// Reason for existence (not just for convenience): httptest servers can only bind 127.0.0.1,
// and guardPullURL naturally rejects loopback — without this hook, the success path, 404,
// over-limit, and redirect **test cases that actually exercise HTTP can't be tested at all**
// (all blocked at the first step at the door).
// Tests only allow "letting through a specific host", everything else still goes through the
// real check, so replacement implementations can't take shortcuts with `return nil`.
var pullGuard = guardPullURL

// GuardExternalURL is the SSRF guard for **any** caller-supplied outbound URL.
//
// Exported 2026-10-04: it used to be unexported (guardPullURL) and reachable only from the P2P
// pull verb, so the HTTP surface POST /files/register_url silently had **no** guard at all —
// measured: {"url":"http://127.0.0.1:<node port>/peerjs/share"} was fetched and ingested as a
// file, i.e. the node could be made to read its own admin endpoints (and, on a cloud host,
// 169.254.169.254). Now both paths share one guard so the two surfaces cannot drift apart again.
//
// Boundary: checks scheme, user-info, localhost, and every resolved IP (loopback, private,
// link-local, multicast, plus IPv4-mapped IPv6 unwrapping). It does **not** restrict certificate
// trust — a separate concern. Callers must also apply it per redirect hop.
func GuardExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	return guardPullURL(u)
}

// guardPullURL SSRF protection: only allows public http(s), rejects internal/local/link-local
// addresses.
//
// Why it must be done: servePull is "passive outbound" — the target is provided externally.
// Without this layer, any peer who can connect to the node could use it to scan the internal
// network (http://192.168.1.1/, cloud provider metadata 169.254.169.254 are all in this
// surface). Certificate trustworthiness is not restricted (that's a separate concern); only
// the resolved IP is checked.
//
// ⚠️ Production paths should only call via pullGuard (see below).
func guardPullURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("empty URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("only http/https supported (got %q)", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL must not contain user info (http://user@host is often used to bypass host checks)")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL missing hostname")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("pulling from localhost is forbidden")
	}
	// Both literal IPs and DNS must be blocked: beyond 127.0.0.1 there's also ::1,
	// 169.254.169.254, and DNS names resolving to internal addresses — so resolve first,
	// then check each IP individually, rather than doing hostname string matching.
	if ip := net.ParseIP(host); ip != nil {
		return guardPullIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("DNS resolved to no addresses")
	}
	for _, ia := range ips {
		if err := guardPullIP(ia.IP); err != nil {
			return err
		}
	}
	return nil
}

// guardPullIP checks whether a single IP falls within the forbidden pull range.
func guardPullIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("pulling from internal/local address is forbidden (%s)", ip.String())
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) would bypass the above checks; explicitly unwrap
	// one layer.
	if v4 := ip.To4(); v4 != nil {
		private := v4[0] == 127 || v4[0] == 10 || v4[0] == 0 ||
			(v4[0] == 172 && v4[1]&0xf0 == 16) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) ||
			v4[0] >= 224
		if private {
			return fmt.Errorf("pulling from internal/local address is forbidden (%s)", v4.String())
		}
	}
	return nil
}
