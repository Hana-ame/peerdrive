package twimg

// transport.go: routes rewritten URLs to the local ech-proxy listener.
//
// Two things happen for a pbs.twimg.com request:
//  1. RoundTripper rewrites the URL authority to twimg-pbs.l.moonchan.xyz:8443
//     (Entry.Rewrite — only the host changes).
//  2. http.Transport dials Entry.ListenAddr (127.0.0.1:8443) instead of resolving the
//     entry host, so this works without "*.l.moonchan.xyz → 127.0.0.1" DNS.
//
// The rewritten URL host is what http.Transport puts in the SNI and the Host header, so
// ech-proxy sees twimg-pbs.l.moonchan.xyz and routes to pbs.twimg.com upstream.
// Non-pbs requests are handed to the base transport untouched — this module must be a
// no-op when disabled and must never touch unrelated traffic when enabled.

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// dialTargetKey marks a request whose TCP dial must be redirected to the local listener.
type dialTargetKey struct{}

// RoundTripper rewrites SrcHost URLs to the ech-proxy entry and routes them to the local
// listener. Every other request is handed to base untouched.
type RoundTripper struct {
	entry Entry
	base  http.RoundTripper
}

// NewRoundTripper builds a RoundTripper. base may be nil (defaults to
// http.DefaultTransport).
func NewRoundTripper(entry Entry, base http.RoundTripper) (*RoundTripper, error) {
	if base == nil {
		base = http.DefaultTransport
	}
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	return &RoundTripper{entry: entry, base: base}, nil
}

// RoundTrip implements http.RoundTripper. The caller's request is never mutated: a
// rewrite produces a clone, because url.go holds onto the request it builds.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("echproxy: request has no URL")
	}
	rewritten, changed, err := rt.entry.Rewrite(req.URL.String())
	if err != nil {
		return nil, err
	}
	if !changed {
		return rt.base.RoundTrip(req)
	}
	nu, err := url.Parse(rewritten)
	if err != nil {
		return nil, fmt.Errorf("echproxy: rewrite produced an unparseable url %q: %w", rewritten, err)
	}
	ctx := context.WithValue(req.Context(), dialTargetKey{}, rt.entry.ListenAddr)
	req2 := req.Clone(ctx)
	req2.URL = nu
	req2.Host = "" // let http.Transport derive Host from URL.Host
	return rt.base.RoundTrip(req2)
}

// NewTransport builds an http.Transport that dials Entry.ListenAddr for rewritten
// requests (marked via dialTargetKey) and the real host for everything else.
//
// TLS: ServerName is left unset so http.Transport derives it from the rewritten URL
// host (twimg-pbs.l.moonchan.xyz) — that is the SNI ech-proxy routes on. SkipTLS skips
// chain verification for the local proxy's self-signed cert.
func NewTransport(entry Entry) (*http.Transport, error) {
	if err := entry.Validate(); err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if target, ok := ctx.Value(dialTargetKey{}).(string); ok && target != "" {
				addr = target
			}
			return d.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: entry.SkipTLS,
		},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}, nil
}

// NewClient returns an http.Client that routes SrcHost requests through the local
// ech-proxy. base may be nil; when non-nil its Timeout/CheckRedirect are inherited and
// only the Transport is replaced.
//
// The timeout is deliberately 0 (inherit http.DefaultClient) so that a large pbs.twimg.com
// media fetch is not cut off mid-stream — url.go's URLSource performs full-range fetches
// with sha256 verification, which can take a long time for video assets.
func NewClient(entry Entry, base *http.Client) (*http.Client, error) {
	tr, err := NewTransport(entry)
	if err != nil {
		return nil, err
	}
	rt, err := NewRoundTripper(entry, tr)
	if err != nil {
		return nil, err
	}
	var client http.Client
	if base != nil {
		client = *base
	}
	client.Transport = rt
	return &client, nil
}
