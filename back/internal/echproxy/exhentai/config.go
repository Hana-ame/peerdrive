// Package exhentai is an **optional** peerdrive module (default OFF) that makes
// ExHentai content reachable through an operator-controlled mirror backend, with
// the entire routing table pushed down from a remote URL at runtime.
//
// Why this module exists
// ----------------------
// exhentai.org / e-hentai.org are unreachable from mainland China. The
// 4545810.xyz station group hosts an ExHentai-compatible mirror at
// ex.4545810.xyz that serves the *stock* ExHentai URL scheme (/g/<id>/<token>/,
// /api.php, /t/<hash>/g/<gid>/), verified 2026-10-09 with plain curl:
//
//	GET https://ex.4545810.xyz/                     -> 200, stock ExHentai home
//	GET https://ex.4545810.xyz/g/4241178/c0eea5d273/ -> 200, 15.5 KB gallery page
//	GET https://ex.4545810.xyz/api.php              -> 200, {"error":"Empty JSON Request"}
//
// so a gallery URL only needs its authority changed. But the mirror is *not*
// self-contained: on that same page the thumbnails come from ehgt.org and the
// full-size images from *.hath.network. Those CDN hosts live outside the mirror,
// so the set of hosts that must be routed is a runtime fact, not a compile-time
// constant — which is exactly why the routing table is pulled from a URL rather
// than written into this file (see the contract below).
//
// How it differs from the sibling modules
// ---------------------------------------
// The parent package (iwara) and the twimg sibling both download ech-proxy and
// run it as a child process, because their upstreams are only reachable through
// ECH domain fronting. ex.4545810.xyz is Cloudflare-fronted and directly
// reachable (cf-cache-status, cf-ray present in the response), so this module
// needs no subprocess: it is a plain host-rewriting http.RoundTripper.
//
// The Backend.LocalAddr escape hatch keeps the ech-proxy option open anyway:
// point a backend at "ex.l.moonchan.xyz" with LocalAddr "127.0.0.1:8443" and
// the rewritten URL's host becomes the SNI that a locally managed ech-proxy
// routes on, exactly as twimg.Entry.ListenAddr works. Same contract, optional.
//
// This stays under internal/echproxy/ for the same reason twimg does: each
// upstream domain keeps its own subpackage so unrelated lifecycle code does not
// share type names. back/ech is a different thing entirely (in-process ECH) and
// is not merged with any of this.
//
// Configuration delivery contract
// -------------------------------
// What  — the RemoteConfig JSON document below, served as a single file.
//
//	`version` is an opaque operator-supplied tag; it is logged on every
//	change so an operator can confirm which document is live (the same
//	discipline as ech-proxy's unversioned upstream.json, which is a
//	documented operational trap).
//
// Where — PEERDRIVE_EXHENTA_CONFIG_URL, set by the operator. HTTPS by default;
//
//	http:// is refused unless PEERDRIVE_EXHENTA_CONFIG_INSECURE=1.
//
// How often — one synchronous fetch at Start(), then a background refresh every
//
//	cache_ttl (default 1h, min 5m) drawn from the document itself, plus
//	the configured jitter. ETag / Last-Modified are honoured: a 304
//	renews freshness without resetting the interval, so a quiet operator
//	costs one HEAD-like round trip per hour and never a cache wipe.
//
// On failure — degrade, never fail. A Store always holds a usable config:
//
//	last-known-good when a refresh fails, the embedded Defaults when the
//	first fetch ever fails. So a GitHub outage cannot take down the node,
//	and an operator who pulls the URL does not get a hard error. This is
//	deliberately the opposite of twimg's loud-failure Start, which is
//	correct there because ech-proxy cannot half-run: here the module is a
//	pure pass-through whose failure mode is "behaves like the disabled
//	node", so quiet degradation is the safer default.
//
// While live — hot. The RoundTripper re-reads the current snapshot under a
//
//	read lock on *every* request, so a pushed change applies to the next
//	request with no restart and no in-flight interruption. This is the
//	"push and it takes effect" behaviour the ech-proxy upstream channel
//	has, reproduced for the routing table.
//
// Default off
// -----------
// Nothing in this package runs unless PEERDRIVE_EXHENTA_ENABLE=true. No config
// URL is fetched, no rule is built, no RoundTripper is installed, and the URL
// source keeps the exact nil-client path it has always had.
//
// Also off when the pushed document says `"enabled": false` — an operator can
// kill the module remotely without restarting the node.
package exhentai

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// SchemaVersion is the shape of RemoteConfig this module understands. An
// operator's document without a `schema` field is treated as this version, so
// documents written before the field existed still parse.
const SchemaVersion = 1

const (
	// DefaultSourceHost is the canonical ExHentai host this module routes.
	DefaultSourceHost = "exhentai.org"
	// AlternateSourceHost is the alternate ExHentai host (e-hentai.org shares the
	// cookie domain with exhentai.org, so both resolve to the same backend).
	AlternateSourceHost = "e-hentai.org"

	// DefaultBackendHost is the configured mirror backend: the ExHentai
	// compatibility mirror in the 4545810.xyz station group. Verified reachable
	// 2026-10-09 (see the package comment).
	DefaultBackendHost = "ex.4545810.xyz"

	// DefaultCacheTTL bounds how long a fetched document stays authoritative
	// before a refresh is attempted. It is also the background refresh period.
	DefaultCacheTTL = time.Hour
	// DefaultRefreshJitter is spread across the cache period so a fleet of nodes
	// does not hit one config URL on the same second.
	DefaultRefreshJitter = 5 * time.Minute
	// DefaultFetchTimeout bounds a single config fetch. Kept short: the fetch
	// happens on the startup path and must not stall node boot.
	DefaultFetchTimeout = 10 * time.Second
	// DefaultRequestTimeout bounds requests routed through a backend. It is 0 in
	// the defaults because URLSource performs sha256-verified full fetches of
	// large media; an aggressive timeout would truncate them.
	DefaultRequestTimeout = 0
)

// MinCacheTTL guards against an operator publishing a document that refreshes
// the whole node fleet every second. MaxCacheTTL is the opposite defence:
// beyond it the cache is always refreshed even when it reports itself fresh.
//
// These are the fleet-wide defaults; a Store may hold tighter bounds of its own
// (see StoreOptions.MinCacheTTL) and apply them through normalizeBounds.
const (
	MinCacheTTL = 5 * time.Minute
	MaxCacheTTL = 24 * time.Hour
)

// Duration is a JSON-friendly time.Duration. encoding/json decodes a duration
// as a number of nanoseconds, which is unreadable in an operator-facing config
// file; "1h", "30s" and "5m" are what a person actually writes.
type Duration time.Duration

// UnmarshalJSON accepts "1h30m", "30s", and plain integer nanoseconds.
func (d *Duration) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s[0] == '"' && s[len(s)-1] == '"' && len(s) >= 2 {
		s = s[1 : len(s)-1]
	}
	if s == "" || s == "null" {
		*d = 0
		return nil
	}
	// Numeric bare value: encoding/json hands us nanoseconds.
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*d = Duration(time.Duration(n))
		return nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("exhentai: cannot parse duration %q: %w", string(b), err)
	}
	*d = Duration(v)
	return nil
}

// MarshalJSON emits a human-readable form.
func (d Duration) MarshalJSON() ([]byte, error) {
	return []byte(`"` + time.Duration(d).String() + `"`), nil
}

// Duration returns the time.Duration value.
func (d Duration) Duration() time.Duration { return time.Duration(d) }

// Backend is one concrete destination a rule can send traffic to.
type Backend struct {
	// Host is the authority to rewrite to, e.g. "ex.4545810.xyz".
	Host string `json:"host"`
	// Port is appended explicitly (e.g. "8443"). Empty = the scheme's implicit
	// default, so a plain mirror backend needs nothing here.
	Port string `json:"port,omitempty"`
	// Scheme defaults to "https".
	Scheme string `json:"scheme,omitempty"`
	// PathPrefix is prepended to the original path, e.g. "/eh" for a backend
	// that mounts ExHentai under a subtree. Must start with "/" and the original
	// path is joined with a single "/" between them. Empty = the stock ExHentai
	// URL scheme, which is the case for ex.4545810.xyz.
	PathPrefix string `json:"path_prefix,omitempty"`
	// Headers are injected into every rewritten request. They override nothing:
	// keys already present on the request are left alone (set-default
	// semantics), which matters because a caller-supplied Range header must
	// survive to the backend.
	Headers map[string]string `json:"headers,omitempty"`
	// Cookie is an extra Cookie header value (e.g. "ipb_member_id=..;
	// ipb_pass_hash=..; igneous=.."). It is appended to any Cookie the caller
	// already set, not replaced, because the caller's cookie belongs to the
	// caller. Empty = anonymous.
	Cookie string `json:"cookie,omitempty"`
	// Weight is the relative share of traffic a backend receives when a rule
	// lists several. Zero or negative is treated as 1.
	Weight int `json:"weight,omitempty"`
	// SkipTLS accepts the backend's certificate without chain verification.
	// Only meaningful for private/self-signed backends; the default mirror is
	// Cloudflare-fronted with a valid chain, so this stays false.
	SkipTLS bool `json:"skip_tls,omitempty"`
	// LocalAddr, when non-empty, is the TCP dial target instead of resolving
	// Host — the ech-proxy deployment shape, where Host is the SNI
	// ("ex.l.moonchan.xyz") and LocalAddr is the local listener
	// ("127.0.0.1:8443"). Mirrors twimg.Entry.ListenAddr.
	LocalAddr string `json:"local_addr,omitempty"`
}

// normalize fills documented defaults and reports validation errors before any
// network I/O. Called on every fetched document, so a bad operator document is
// rejected loudly per-rule rather than at request time.
func (b *Backend) normalize() error {
	if strings.TrimSpace(b.Host) == "" {
		return errors.New("exhentai: backend host is empty")
	}
	// Validate the operator-supplied host here, not at request time: a bad host
	// in a pushed document must reject the whole document loudly rather than
	// failing one request at a time for the next hour.
	if !validHost(b.Host) {
		return fmt.Errorf("exhentai: backend host %q is not a valid hostname", b.Host)
	}
	if b.Scheme == "" {
		b.Scheme = "https"
	}
	if up := strings.ToUpper(b.Scheme); up != "HTTP" && up != "HTTPS" {
		return fmt.Errorf("exhentai: backend %q has unsupported scheme %q (http/https only)", b.Host, b.Scheme)
	}
	b.Scheme = strings.ToLower(b.Scheme)
	if b.Port != "" && !validPort(b.Port) {
		return fmt.Errorf("exhentai: backend %q has an invalid port %q (1-65535)", b.Host, b.Port)
	}
	if b.PathPrefix != "" && !strings.HasPrefix(b.PathPrefix, "/") {
		return fmt.Errorf("exhentai: backend %q path_prefix %q must start with \"/\"", b.Host, b.PathPrefix)
	}
	if b.PathPrefix == "/" {
		b.PathPrefix = ""
	}
	if b.Weight <= 0 {
		b.Weight = 1
	}
	if b.LocalAddr != "" {
		if _, _, err := net.SplitHostPort(b.LocalAddr); err != nil {
			return fmt.Errorf("exhentai: backend %q local_addr %q is not host:port: %w", b.Host, b.LocalAddr, err)
		}
	}
	return nil
}

// targetHost returns the host:port string used in the rewritten URL authority.
func (b Backend) targetHost() string {
	if b.Port == "" {
		return b.Host
	}
	if strings.Contains(b.Host, ":") && !strings.HasPrefix(b.Host, "[") {
		return "[" + b.Host + "]:" + b.Port
	}
	return b.Host + ":" + b.Port
}

// Rule routes a set of source hosts to an ordered, weighted list of backends.
type Rule struct {
	// SourceHosts are exact hosts ("exhentai.org") or suffix wildcards
	// ("*.hath.network"). Suffix matching is case-insensitive and also matches
	// the bare host, so "*.hath.network" matches both "hath.network" and
	// "praogxqcch.hath.network".
	SourceHosts []string `json:"source_hosts"`
	// Backends is the weighted fallback list. Traffic goes to the first by
	// default; a rule with several backends load-balances across them, and
	// the operator can reorder the list to fail over by pushing a new document.
	Backends []Backend `json:"backends"`
}

// matches reports whether host is one of the rule's source hosts.
func (r Rule) matches(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	for _, pat := range r.SourceHosts {
		p := strings.ToLower(strings.TrimSpace(pat))
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, "*.") {
			suffix := p[1:] // ".hath.network"
			if strings.HasSuffix(host, suffix) || host == suffix[1:] {
				return true
			}
			continue
		}
		if host == p {
			return true
		}
	}
	return false
}

// RemoteConfig is the document served at PEERDRIVE_EXHENTA_CONFIG_URL.
//
// Every field except `version` and `rules` is optional; a document may be as
// short as {"version":"x","rules":[...]} and everything else takes the
// documented default. Unknown fields are ignored so the document can grow
// without breaking older nodes.
type RemoteConfig struct {
	// Schema is the document shape version (SchemaVersion). Empty = SchemaVersion.
	Schema int `json:"schema,omitempty"`
	// Version is an opaque operator tag, logged on every change so the live
	// document can be identified without diffing.
	Version string `json:"version"`
	// UpdatedAt is informational (RFC3339 text); the module uses its own fetch
	// clock rather than trusting the operator's.
	UpdatedAt string `json:"updated_at,omitempty"`
	// Enabled is a remote kill switch: a document with enabled=false makes the
	// RoundTripper pass everything through, which is the same as the disabled
	// node. Nil (absent) = enabled, so an older document keeps working.
	Enabled *bool `json:"enabled,omitempty"`
	// CacheTTL is both the cache freshness window and the refresh period.
	CacheTTL Duration `json:"cache_ttl,omitempty"`
	// RefreshJitter is spread across the cache period. Default
	// DefaultRefreshJitter.
	RefreshJitter Duration `json:"refresh_jitter,omitempty"`
	// FetchTimeout bounds one config fetch. Default DefaultFetchTimeout.
	FetchTimeout Duration `json:"fetch_timeout,omitempty"`
	// RequestTimeout bounds requests routed through a backend. Default 0 (no
	// timeout; see DefaultRequestTimeout).
	RequestTimeout Duration `json:"request_timeout,omitempty"`
	// Rules is the routing table. Empty means the module is inert even when
	// enabled — a document with no rules is a valid "turn it off remotely" doc.
	Rules []Rule `json:"rules"`
}

// IsEnabled reports the effective remote switch state. Absent = enabled, so a
// document written before the field existed keeps working.
func (c RemoteConfig) IsEnabled() bool {
	if c.Enabled == nil {
		return true
	}
	return *c.Enabled
}

// normalize fills defaults and validates the whole document using the
// fleet-wide cache bounds. It returns a copy so the caller's parsed struct is
// never mutated.
func (c RemoteConfig) normalize() (RemoteConfig, error) {
	return c.normalizeBounds(MinCacheTTL, MaxCacheTTL)
}

// normalizeBounds is normalize with explicit cache bounds, so a Store can apply
// its own tighter window instead of the fleet default. Bounds are never
// process-global mutable state, which would race with a background refresh
// goroutine.
func (c RemoteConfig) normalizeBounds(minTTL, maxTTL time.Duration) (RemoteConfig, error) {
	out := c
	if out.Schema == 0 {
		out.Schema = SchemaVersion
	}
	if out.Schema > SchemaVersion {
		return out, fmt.Errorf("exhentai: config schema %d is newer than this module understands (%d)",
			out.Schema, SchemaVersion)
	}
	if out.CacheTTL.Duration() <= 0 {
		out.CacheTTL = Duration(DefaultCacheTTL)
	}
	if out.CacheTTL.Duration() < minTTL {
		out.CacheTTL = Duration(minTTL)
	}
	if out.CacheTTL.Duration() > maxTTL {
		out.CacheTTL = Duration(maxTTL)
	}
	if out.RefreshJitter.Duration() <= 0 {
		out.RefreshJitter = Duration(DefaultRefreshJitter)
	}
	if out.RefreshJitter.Duration() > out.CacheTTL.Duration() {
		out.RefreshJitter = Duration(out.CacheTTL.Duration() / 2)
	}
	if out.FetchTimeout.Duration() <= 0 {
		out.FetchTimeout = Duration(DefaultFetchTimeout)
	}
	if out.RequestTimeout.Duration() < 0 {
		out.RequestTimeout = 0
	}

	for i := range out.Rules {
		r := &out.Rules[i]
		if len(r.SourceHosts) == 0 {
			return out, errors.New("exhentai: rule with no source_hosts")
		}
		if len(r.Backends) == 0 {
			return out, fmt.Errorf("exhentai: rule for %v has no backends", r.SourceHosts)
		}
		for j := range r.Backends {
			if err := r.Backends[j].normalize(); err != nil {
				return out, fmt.Errorf("exhentai: rule for %v backend %d: %w", r.SourceHosts, j+1, err)
			}
		}
	}
	return out, nil
}

// DefaultRemoteConfig is the embedded fallback used when the config URL has
// never been reached, or when a refresh fails before anything was ever fetched.
//
// It routes the two ExHentai hosts at the verified mirror backend and the two
// CDN host families observed on a real gallery page (ehgt.org thumbnails,
// *.hath.network full-size images). The CDN backends are placeholders that an
// operator is expected to override in the pushed document — the mirror does not
// host them — which is the concrete reason the routing table has to be pushable
// rather than compiled in.
func DefaultRemoteConfig() RemoteConfig {
	return RemoteConfig{
		Schema:  SchemaVersion,
		Version: "builtin-exhentai-1",
		Rules: []Rule{
			{
				SourceHosts: []string{DefaultSourceHost, AlternateSourceHost},
				Backends: []Backend{
					{
						Host:    DefaultBackendHost,
						Scheme:  "https",
						Cookie:  "",
						Headers: map[string]string{"User-Agent": DefaultUserAgent},
					},
				},
			},
			{
				// Thumbnail sprite host seen on gallery pages. The mirror does not
				// serve it, so the operator pushes a working backend for it.
				SourceHosts: []string{"ehgt.org"},
				Backends: []Backend{
					{Host: "ehgt.org", Scheme: "https", Headers: map[string]string{"User-Agent": DefaultUserAgent}},
				},
			},
			{
				// Full-size image CDN, randomised subdomain per gallery.
				SourceHosts: []string{"*.hath.network"},
				Backends: []Backend{
					{Host: "hath.network", Scheme: "https", Headers: map[string]string{"User-Agent": DefaultUserAgent}},
				},
			},
		},
	}
}

// DefaultUserAgent is injected when a rule's backend does not supply its own.
// A browser-like agent: the mirror is a stock ExHentai deployment, which 403s
// the Go default client for page fetches the way e-hentai.org does.
const DefaultUserAgent = "Mozilla/5.0 (compatible; peerdrive/" + DefaultBackendHost + ")"

// validPort reports whether p is a decimal TCP port in [1, 65535].
// net.SplitHostPort does not validate port numbers, so every place a port is
// accepted calls this.
func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}
