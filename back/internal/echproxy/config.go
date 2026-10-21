// Package echproxy is an optional peerdrive module (default OFF) that accesses
// iwara.tv content through in-process ECH domain fronting (internal/echcore).
//
// When the module is enabled (PEERDRIVE_IWARA_ENABLE=true) it:
//
//  1. Sends every iwara request through echcore, the in-process ECH client
//     that fronts the request at cloudflare-ech.com with the real iwara host
//     encrypted inside the ClientHello. There is no local server: no subprocess
//     is spawned, no address is bound, and no self-signed certificate is used,
//     so no TLS verification is skipped.
//  2. Resolves an iwara video ID to a direct CDN download URL via the iwara
//     API (api.iwara.tv), injecting the user's iwara login cookie.
//
// When disabled (the default), none of this runs: no ECH egress is configured
// and no iwara-specific code path executes. peerdrive behaviour is
// byte-identical to the baseline.
//
// Design notes:
//   - The module is a plain Go package, not a Go build-tag optional module.
//     "Optional" here means a runtime configuration flag, matching peerdrive's
//     existing convention for opt-in features (ShareEnable, AutoExtract, ...).
//   - The iwara API surface used here (https://api.iwara.tv/video/{id} →
//     fileUrl → resolution list → src.download, X-Version = SHA1(fid_+expires+
//     _<secret>)) mirrors the open-source IwaraDownloadTool / iwaradl /
//     iwara_spider_lib implementations; see client.go for the endpoint list.
//
// Background: iwara.tv is Cloudflare-fronted and blocked in mainland China, so
// a direct dial times out after the caller's deadline. ECH domain fronting
// sends the request to cloudflare-ech.com with the true target host encrypted
// inside the ClientHello; the SNI seen by any intermediate observer is
// cloudflare-ech.com, not iwara.tv. The upstream certificate is still verified
// against the system roots, because the ECH handshake is performed in-process
// against cloudflare-ech.com's own ECH configuration — there is no locally
// managed listener whose certificate would have to be trusted out of band.
package echproxy

// Default iwara host coordinates (upstream.json iwara entry, v1.3.0).
const (
	// DefaultUpstreamSuffix is the iwara host suffix used to recognise iwara
	// URLs (e.g. in ParseVideoID).
	DefaultUpstreamSuffix = "iwara.tv"
	// DefaultIWARAHost is the iwara front host used for the X-Site / Origin /
	// Referer headers.
	DefaultIWARAHost = "www.iwara.tv"
)

// ModuleConfig holds the runtime configuration of the optional iwara module.
//
// It is populated from the peerdrive environment by serverapp and is
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
	// IWARAHost is the iwara front host for X-Site / Origin / Referer
	// (DefaultIWARAHost).
	IWARAHost string
}

// NewModuleConfig returns a ModuleConfig with every field at its documented
// default. Callers override only what they need.
func NewModuleConfig() ModuleConfig {
	return ModuleConfig{
		Enable:    false,
		IWARAHost: DefaultIWARAHost,
	}
}

// Normalize fills any empty string field with its documented default. Intended
// to be called on a config loaded from the environment, where unset env vars
// yield empty strings rather than defaults.
func (c *ModuleConfig) Normalize() {
	if c.IWARAHost == "" {
		c.IWARAHost = DefaultIWARAHost
	}
}
