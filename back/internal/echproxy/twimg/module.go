// Package twimg is an optional peerdrive module that makes pbs.twimg.com
// (Twitter's image CDN) reachable from mainland China by sending those requests
// through the in-process ECH domain-fronting client (internal/echcore).
//
// pbs.twimg.com is Cloudflare-fronted, so ECH fronting via cloudflare-ech.com
// works with no host rewrite: the request is sent to the real host and the true
// SNI is carried encrypted inside the ClientHello. Non-matching hosts are handed
// to the base transport untouched, so the module is a pure additive layer that
// never touches unrelated traffic.
//
// The previous implementation downloaded an ech-proxy binary, verified it
// against a published SHA256, spawned it as a subprocess binding
// 127.0.0.1:8443 with a self-signed certificate, and routed pbs.twimg.com
// through the entry host twimg-pbs.l.moonchan.xyz. That required
// InsecureSkipVerify on the dialer (gosec G402). echcore performs the same ECH
// handshake in-process, so there is no local server, no subprocess, no
// self-signed certificate, and no verification to skip.
//
// Default off. Enable with PEERDRIVE_ECH_PROXY_ENABLE=true.
package twimg

import (
	"context"
	"net/http"
	"strings"

	"peerdrive/internal/echcore"
	"peerdrive/internal/log"
)

const (
	// DefaultSrcHost is the upstream host this module routes through ECH.
	DefaultSrcHost = "pbs.twimg.com"
	// DefaultIPMode pins the upstream egress family. echcore's default is
	// "auto", which follows the OS resolver; v4 is the documented Pixiv fix and
	// the safe choice for a media proxy.
	DefaultIPMode = "v4"
)

// Config is the module's settings. Zero-value fields fall back to the
// documented defaults.
type Config struct {
	// SrcHost is the upstream host routed through ECH (default DefaultSrcHost).
	SrcHost string
	// IPMode pins the upstream egress family: "v4", "v6", or "" (auto).
	IPMode string
	// HTTPBase is the base transport used for non-matching traffic. nil →
	// http.DefaultTransport.
	HTTPBase http.RoundTripper
}

// Module is the in-process ECH client that reaches SrcHost. It owns no process,
// downloads nothing, and binds no address: Client() is available immediately
// after New.
type Module struct {
	srcHost string
	ipMode  string
	client  *http.Client
}

// New validates cfg and builds the http.Client. Nothing is downloaded, spawned
// or bound — there is no local server to start.
func New(cfg Config) (*Module, error) {
	if cfg.SrcHost == "" {
		cfg.SrcHost = DefaultSrcHost
	}
	if cfg.IPMode == "" {
		cfg.IPMode = DefaultIPMode
	}
	if cfg.HTTPBase == nil {
		cfg.HTTPBase = http.DefaultTransport
	}
	return &Module{
		srcHost: cfg.SrcHost,
		ipMode:  cfg.IPMode,
		client: &http.Client{
			Transport: echRT{srcHost: cfg.SrcHost, ipMode: cfg.IPMode, base: cfg.HTTPBase},
		},
	}, nil
}

// Start is retained for the server's module contract but does nothing to the
// network layer: the in-process ECH client needs no lifecycle. It only warms the
// ECH key cache so the first request does not pay the DoH round trip, and starts
// the periodic key refresh. A failure is not fatal — echcore fetches the config
// on demand if the cache is still empty.
func (m *Module) Start(ctx context.Context) error {
	if err := echcore.InitDefault(echcore.Config{IPMode: m.ipMode}); err != nil {
		log.LogWarn("twimg: ECH init failed (%v); requests will fetch the ECH config on demand", err)
	}
	log.LogInfo("twimg: enabled — %s via in-process ECH domain fronting (ip_mode=%s, no local server)",
		m.srcHost, m.ipMode)
	return nil
}

// Stop is a no-op: there is no subprocess to terminate.
func (m *Module) Stop(ctx context.Context) error { return nil }

// Client returns the http.Client that reaches SrcHost through ECH. Non-matching
// traffic is passed to the base transport.
func (m *Module) Client() *http.Client { return m.client }

// Alive reports whether the module is usable. There is no child process to
// watch, so it is always true once New succeeds.
func (m *Module) Alive() bool { return m.client != nil }

// SrcHost returns the upstream host routed through ECH.
func (m *Module) SrcHost() string { return m.srcHost }

// echDo is the ECH egress used by echRT. Kept as a package-level seam so tests
// can avoid real network I/O without reaching into http.Client internals.
var echDo = func(req *http.Request, ipMode string) (*http.Response, error) {
	if ipMode == "" {
		return echcore.Do(req)
	}
	return echcore.Do(req, ipMode)
}

// echRT sends matching hosts through in-process ECH and everything else to base.
type echRT struct {
	srcHost string
	ipMode  string
	base    http.RoundTripper
}

// RoundTrip implements http.RoundTripper. The caller's request is never mutated:
// matching hosts are forwarded unchanged (ECH needs the real host, not a rewrite),
// so url.go's request objects stay valid.
func (rt echRT) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, http.ErrServerClosed
	}
	if strings.EqualFold(req.URL.Hostname(), rt.srcHost) {
		return echDo(req, rt.ipMode)
	}
	return rt.base.RoundTrip(req)
}
