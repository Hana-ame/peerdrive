// Package echproxy is an optional peerdrive module (default OFF) that accesses
// iwara.tv content through the ech-proxy transparent TLS proxy.
//
// When the module is enabled (PEERDRIVE_IWARA_ENABLE=true) it:
//
//  1. Downloads the ech-proxy Windows executable from the project's GitHub
//     releases, verifies it against checksums.txt (SHA256), and manages its
//     lifecycle (start → readiness → stop, with port-conflict detection).
//  2. Rewrites iwara.tv URLs to go through the ech-proxy entry host
//     (host-only rewrite: only the URL Host changes; path, query, fragment
//     are preserved).
//  3. Resolves an iwara video ID to a direct CDN download URL via the iwara
//     API (api.iwara.tv), injecting the user's iwara login cookie.
//
// When disabled (the default), none of this runs: no ech-proxy process is
// spawned, no URL is rewritten, and no iwara-specific code path executes.
// peerdrive behaviour is byte-identical to the baseline.
//
// Design notes:
//   - The module is a plain Go package, not a Go build-tag optional module.
//     "Optional" here means a runtime configuration flag, matching peerdrive's
//     existing convention for opt-in features (ShareEnable, AutoExtract, ...).
//   - The iwara API surface used here (https://api.iwara.tv/video/{id} →
//     fileUrl → resolution list → src.download, X-Version = SHA1(fid_+expires+
//     _<secret>)) mirrors the open-source IwaraDownloadTool / iwaradl /
//     iwara_spider_lib implementations; see iwara.go for the endpoint list.
//
// Background: iwara.tv is Cloudflare-fronted and blocked in mainland China.
// ech-proxy (Hana-ame/ech-proxy) uses ECH domain fronting with a moonchan.xyz
// entry host so the SNI seen by the GFW is cloudflare-ech.com, not iwara.tv.
// The upstream.json iwara entry (v1.3.0) is:
//
//	"iwara.l.moonchan.xyz": {
//	    "host": "www.iwara.tv", "ip_mode": "v4",
//	    "referer": "https://www.iwara.tv/",
//	    "headers": {"Origin": "https://www.iwara.tv", "X-Site": "www.iwara.tv"},
//	    "wildcard": {"prefix": "iwara-", "entry_suffix": ".l.moonchan.xyz",
//	                    "upstream_suffix": ".iwara.tv", "referer": "https://www.iwara.tv/"},
//	    "rewrites": {"www.iwara.tv": "iwara.l.moonchan.xyz"}
//	}
//
// So https://www.iwara.tv/videos/x becomes
// https://iwara.l.moonchan.xyz:8443/videos/x, and
// https://api.iwara.tv/video/xyz becomes
// https://iwara-api.l.moonchan.xyz:8443/video/xyz.
package echproxy

import (
	"path/filepath"
	"strings"
)

// Default ech-proxy release coordinates (Hana-ame/ech-proxy, v1.3.0).
const (
	// DefaultEchProxyVersion is the ech-proxy release tag.
	DefaultEchProxyVersion = "v1.3.0"
	// DefaultEchProxyExeName is the Windows amd64 release asset.
	DefaultEchProxyExeName = "ech-proxy-windows-amd64.exe"
	// DefaultChecksumsName is the SHA256 manifest shipped alongside the exe.
	DefaultChecksumsName = "checksums.txt"
	// DefaultReleaseBaseURL is the release download URL prefix.
	DefaultReleaseBaseURL = "https://github.com/Hana-ame/ech-proxy/releases/download/"
)

// Default iwara/ech-proxy routing coordinates (upstream.json, v1.3.0).
const (
	// DefaultUpstreamSuffix is the upstream host suffix to match.
	DefaultUpstreamSuffix = "iwara.tv"
	// DefaultEntrySuffix is the ech-proxy entry host suffix.
	DefaultEntrySuffix = "l.moonchan.xyz"
	// DefaultEntryPrefix is the wildcard entry prefix.
	DefaultEntryPrefix = "iwara-"
	// DefaultEntryPort is the ech-proxy default TLS listen port.
	DefaultEntryPort = "8443"
	// DefaultIWARAHost is the iwara front host used for the X-Site / Origin headers.
	DefaultIWARAHost = "www.iwara.tv"
)

// ModuleConfig holds the runtime configuration of the optional iwara module.
//
// It is populated from the peerdrive environment by LoadModuleConfig and is
// intentionally independent of internal/config: the module must be importable
// and testable without pulling in the whole peerdrive config surface (and the
// reverse — internal/config must not depend on this package).
type ModuleConfig struct {
	// Enable is the module master switch (PEERDRIVE_IWARA_ENABLE, default false).
	Enable bool

	// IWARACookie is the iwara login Cookie header value, imported by the user
	// (PEERDRIVE_IWARA_COOKIE, default empty). When set, it is injected into
	// every iwara API request header so premium / private / ecchi content is
	// visible. Empty = anonymous (public content only).
	IWARACookie string

	// UpstreamSuffix is the upstream host suffix to match (DefaultUpstreamSuffix).
	UpstreamSuffix string
	// EntrySuffix is the ech-proxy entry host suffix (DefaultEntrySuffix).
	EntrySuffix string
	// EntryPrefix is the wildcard entry prefix (DefaultEntryPrefix).
	EntryPrefix string
	// EntryPort is the ech-proxy listen port (DefaultEntryPort).
	EntryPort string
	// IWARAHost is the iwara front host for X-Site / Origin / Referer
	// (DefaultIWARAHost).
	IWARAHost string

	// EchProxyVersion is the ech-proxy release tag (DefaultEchProxyVersion).
	EchProxyVersion string
	// EchProxyExeName is the release asset file name (DefaultEchProxyExeName).
	EchProxyExeName string
	// ReleaseBaseURL is the release download URL prefix (DefaultReleaseBaseURL).
	ReleaseBaseURL string
	// EchProxyDir is where the ech-proxy executable is stored (default "./echproxy").
	EchProxyDir string

	// ChecksumsURLOverride overrides the computed checksums.txt URL
	// (for tests and local mirrors). Empty = computed from ReleaseBaseURL.
	ChecksumsURLOverride string
	// ExeURLOverride overrides the computed exe URL (for tests and local mirrors).
	// Empty = computed from ReleaseBaseURL.
	ExeURLOverride string

	// ListenAddr is the local address ech-proxy binds to (default "127.0.0.1:8443").
	// Must be loopback: ech-proxy serves a TLS listener that rewrites arbitrary
	// upstream hosts, so binding to 0.0.0.0 would turn it into an open proxy.
	ListenAddr string

	// FixedCookies is an extra set of cookies injected by ech-proxy itself
	// (upstream.json "cookie" / "headers" mechanism) — a comma-separated list of
	// name=value pairs. Unlike IWARACookie (which this module injects per
	// request), these are baked into the ech-proxy process configuration and
	// apply to every upstream through that proxy. Empty = none.
	FixedCookies string
}

// NewModuleConfig returns a ModuleConfig with every field at its documented
// default. Callers override only what they need.
func NewModuleConfig() ModuleConfig {
	return ModuleConfig{
		Enable:          false,
		UpstreamSuffix:  DefaultUpstreamSuffix,
		EntrySuffix:     DefaultEntrySuffix,
		EntryPrefix:     DefaultEntryPrefix,
		EntryPort:       DefaultEntryPort,
		IWARAHost:       DefaultIWARAHost,
		EchProxyVersion: DefaultEchProxyVersion,
		EchProxyExeName: DefaultEchProxyExeName,
		ReleaseBaseURL:  DefaultReleaseBaseURL,
		EchProxyDir:     "./echproxy",
		ListenAddr:      "127.0.0.1:" + DefaultEntryPort,
	}
}

// Normalize fills any empty string field with its documented default. Intended
// to be called on a config loaded from the environment, where unset env vars
// yield empty strings rather than defaults.
func (c *ModuleConfig) Normalize() {
	if c.UpstreamSuffix == "" {
		c.UpstreamSuffix = DefaultUpstreamSuffix
	}
	if c.EntrySuffix == "" {
		c.EntrySuffix = DefaultEntrySuffix
	}
	if c.EntryPrefix == "" {
		c.EntryPrefix = DefaultEntryPrefix
	}
	if c.EntryPort == "" {
		c.EntryPort = DefaultEntryPort
	}
	if c.IWARAHost == "" {
		c.IWARAHost = DefaultIWARAHost
	}
	if c.EchProxyVersion == "" {
		c.EchProxyVersion = DefaultEchProxyVersion
	}
	if c.EchProxyExeName == "" {
		c.EchProxyExeName = DefaultEchProxyExeName
	}
	if c.ReleaseBaseURL == "" {
		c.ReleaseBaseURL = DefaultReleaseBaseURL
	}
	if c.EchProxyDir == "" {
		c.EchProxyDir = "./echproxy"
	}
	if c.ListenAddr == "" {
		c.ListenAddr = "127.0.0.1:" + c.EntryPort
	}
}

// ExeName returns the ech-proxy executable file name.
func (c *ModuleConfig) ExeName() string {
	if c.EchProxyExeName == "" {
		return DefaultEchProxyExeName
	}
	return c.EchProxyExeName
}

// ExePath returns the local file path of the ech-proxy executable.
// filepath.Join (not "/" concatenation) is required: the ech-proxy binary is
// only ever built for Windows, and CI's windows-amd64 job compares this value
// against filepath.Join(dir, name), which yields "\" there. Hardcoding "/"
// made TestEnsureExe / TestEnsureExeCorruptVerifies fail on every Windows run.
func (c *ModuleConfig) ExePath() string {
	dir := strings.TrimRight(c.EchProxyDir, "/")
	if dir == "" {
		return c.EchProxyExeName
	}
	return filepath.Join(dir, c.EchProxyExeName)
}

// ChecksumsURL returns the checksums.txt download URL for the configured
// ech-proxy version. If ChecksumsURLOverride is set, that is returned
// verbatim (useful for tests and for pinning a local mirror).
func (c *ModuleConfig) ChecksumsURL() string {
	if c.ChecksumsURLOverride != "" {
		return c.ChecksumsURLOverride
	}
	return c.ReleaseBaseURL + c.EchProxyVersion + "/" + DefaultChecksumsName
}

// ExeURL returns the ech-proxy executable download URL for the configured
// version. If ExeURLOverride is set, that is returned verbatim.
func (c *ModuleConfig) ExeURL() string {
	if c.ExeURLOverride != "" {
		return c.ExeURLOverride
	}
	return c.ReleaseBaseURL + c.EchProxyVersion + "/" + c.EchProxyExeName
}

// EntryBase returns the base entry host for the non-subdomain case, e.g.
// "iwara.l.moonchan.xyz" (the upstream.json "rewrites" target).
func (c *ModuleConfig) EntryBase() string {
	return strings.TrimSuffix(c.EntryPrefix, "-") + "." + c.EntrySuffix
}
