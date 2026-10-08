package echproxy

import (
	"net/url"
	"strings"
)

// Rewriter rewrites iwara.tv URLs to go through the ech-proxy entry host.
//
// The rewrite is **host-only**: exactly one component of the URL changes
// (the Host, which also gains the ech-proxy listen port). Scheme, Path,
// Query and Fragment are preserved byte-for-byte. The scheme is normalized
// to "https" because the ech-proxy entry serves TLS exclusively; an
// "http://" iwara URL is therefore also rewritten to "https://".
//
// Rewrite rules mirror the ech-proxy upstream.json iwara entry:
//
//	www.iwara.tv  →  iwara.l.moonchan.xyz            (the "rewrites" entry)
//	iwara.tv      →  iwara.l.moonchan.xyz            (bare host, same as www)
//	<sub>.iwara.tv → iwara-<sub>.l.moonchan.xyz      (wildcard entry)
//	<anything else> → unchanged
//
// "Host-only" is a deliberate design choice: it is the minimal surface that
// makes the ech-proxy entry work, and it is what the upstream.json
// "rewrites" mechanism does. It deliberately does NOT touch query strings,
// fragments or paths — iwara's file URLs carry signed query parameters
// (?expires=...&signature=...) that must survive the rewrite intact.
type Rewriter struct {
	upstreamSuffix string // e.g. "iwara.tv"
	entrySuffix    string // e.g. "l.moonchan.xyz"
	entryPrefix    string // e.g. "iwara-"
	entryPort      string // e.g. "8443"
}

// NewRewriter builds a Rewriter. Empty fields fall back to the module
// defaults; all four are required for the rewrite to be meaningful, so a
// Rewriter built by hand is always valid.
func NewRewriter(upstreamSuffix, entrySuffix, entryPrefix, entryPort string) Rewriter {
	if upstreamSuffix == "" {
		upstreamSuffix = DefaultUpstreamSuffix
	}
	if entrySuffix == "" {
		entrySuffix = DefaultEntrySuffix
	}
	if entryPrefix == "" {
		entryPrefix = DefaultEntryPrefix
	}
	if entryPort == "" {
		entryPort = DefaultEntryPort
	}
	return Rewriter{
		upstreamSuffix: upstreamSuffix,
		entrySuffix:    entrySuffix,
		entryPrefix:    entryPrefix,
		entryPort:      entryPort,
	}
}

// RewriterFromConfig builds a Rewriter from a ModuleConfig.
func RewriterFromConfig(c *ModuleConfig) Rewriter {
	if c == nil {
		return NewRewriter("", "", "", "")
	}
	return NewRewriter(c.UpstreamSuffix, c.EntrySuffix, c.EntryPrefix, c.EntryPort)
}

// Rewrite rewrites rawURL to go through the ech-proxy entry.
//
// Returns the rewritten URL and true when rawURL is an iwara.tv URL (and was
// therefore rewritten). Returns the input unchanged and false when rawURL is
// not an iwara.tv URL, is not a valid URL, or has no host. Callers that need
// "leave non-iwara URLs alone" semantics can simply use the returned string
// and ignore the bool.
func (r Rewriter) Rewrite(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL, false
	}

	host := strings.ToLower(u.Host)
	// Strip any port from the original host before matching. The original
	// host may already carry a port (e.g. "api.iwara.tv:443"); the upstream
	// suffix is a host, so a port must not defeat the match.
	if hp := hostSplit(host); hp != "" {
		host = hp
	}

	if !strings.HasSuffix(host, "."+r.upstreamSuffix) && host != r.upstreamSuffix {
		return rawURL, false
	}

	// Determine the entry host.
	var entryHost string
	if host == r.upstreamSuffix || host == "www."+r.upstreamSuffix {
		// Bare or www host → the upstream.json "rewrites" entry
		// (no subdomain prefix).
		entryHost = strings.TrimSuffix(r.entryPrefix, "-") + "." + r.entrySuffix
	} else {
		// <sub>.iwara.tv → iwara-<sub>.l.moonchan.xyz (wildcard entry).
		sub := strings.TrimSuffix(host, "."+r.upstreamSuffix)
		entryHost = r.entryPrefix + sub + "." + r.entrySuffix
	}

	// Host-only rewrite: set Host (with port) and normalize scheme; preserve
	// Path, RawQuery, and FragmentFragment verbatim.
	u.Host = netJoinHostPort(entryHost, r.entryPort)
	if u.Scheme == "" || u.Scheme == "http" {
		u.Scheme = "https"
	}
	return u.String(), true
}

// hostSplit strips a trailing ":port" from a host string. Returns the bare
// host. This mirrors net.SplitHostPort semantics but is written inline to
// avoid a per-call import for a one-line helper.
func hostSplit(host string) string {
	if i := strings.LastIndex(host, ":"); i >= 0 {
		return host[:i]
	}
	return host
}

// netJoinHostPort joins a host and a port, handling bracketed IPv6 hosts
// (not expected for iwara, but correct by construction).
func netJoinHostPort(host, port string) string {
	if port == "" {
		return host
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		return "[" + host + "]:" + port
	}
	return host + ":" + port
}

// IwaraHeaders returns the standard iwara request headers that the ech-proxy
// upstream.json iwara entry injects (Origin / X-Site / Referer). They are
// also sent explicitly by IwaraClient so that requests not going through
// ech-proxy (direct API access) still carry them.
func IwaraHeaders(host string) map[string]string {
	if host == "" {
		host = DefaultIWARAHost
	}
	return map[string]string{
		"Origin":  "https://" + host,
		"X-Site":  host,
		"Referer": "https://" + host + "/",
	}
}
