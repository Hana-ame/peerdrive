// transport.go: installs the routing table on an http.Client.
//
// The RoundTripper clones the request before mutating it: url.go holds onto the
// request it builds, so mutating in place would corrupt the caller's request.
// Non-matching requests are handed to the base transport untouched, which is
// what makes the disabled module a no-op and the enabled module a pure
// additive layer that never touches unrelated traffic.
//
// Two backend shapes are supported through one transport:
//
//   - Direct (the default, ex.4545810.xyz): LocalAddr empty, so the dial goes
//     to the resolved backend host and SkipTLS is false because the mirror is
//     Cloudflare-fronted with a valid chain.
//   - ech-proxy: LocalAddr set (127.0.0.1:8443), so the dial goes to the local
//     listener while BackendHost stays in the URL and therefore in the SNI and
//     Host header the proxy routes on. SkipTLS is normally true here because
//     the local proxy uses a self-signed cert — the same shape twimg uses.
//
// TLS is always handled by DialTLSContext rather than tls.Config, because the
// dial address and the SNI server name diverge in ech-proxy mode and a
// per-request SkipTLS flag is not expressible in a shared *tls.Config.
// tls.Config.GetConfigForClient sees only the ClientHello, not our request
// context, so it cannot resolve a per-request skip.

package exhentai

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

// dialTargetKey marks a request whose TCP dial must be redirected to the local
// ech-proxy listener instead of resolving the rewritten host.
type dialTargetKey struct{}

// sniNameKey marks a request whose TLS SNI must be the rewritten host rather
// than the dial target. Always set when dialTargetKey is, but kept separate so
// the two can be reasoned about independently.
type sniNameKey struct{}

// tlsSkipKey marks a request whose TLS handshake must skip chain verification.
type tlsSkipKey struct{}

// RoundTripper routes matching URLs through their configured backend and passes
// everything else to base unchanged.
type RoundTripper struct {
	router *Router
	base   http.RoundTripper
}

// NewRoundTripper builds a RoundTripper. base nil = http.DefaultTransport.
func NewRoundTripper(router *Router, base http.RoundTripper) (*RoundTripper, error) {
	if router == nil {
		return nil, errors.New("exhentai: NewRoundTripper requires a non-nil Router")
	}
	if base == nil {
		base = http.DefaultTransport
	}
	return &RoundTripper{router: router, base: base}, nil
}

// RoundTrip implements http.RoundTripper.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req == nil || req.URL == nil {
		return nil, errors.New("exhentai: request has no URL")
	}
	route, err := rt.router.Route(req.URL.String())
	if err != nil {
		return nil, err
	}
	if route == nil {
		return rt.base.RoundTrip(req)
	}

	nu, err := url.Parse(route.Target)
	if err != nil {
		return nil, fmt.Errorf("exhentai: rewrite produced an unparseable url %q: %w", route.Target, err)
	}

	ctx := req.Context()
	if route.LocalAddr != "" {
		ctx = context.WithValue(ctx, dialTargetKey{}, route.LocalAddr)
		ctx = context.WithValue(ctx, sniNameKey{}, route.BackendHost)
	}
	ctx = context.WithValue(ctx, tlsSkipKey{}, route.SkipTLS)

	req2 := req.Clone(ctx)
	req2.URL = nu
	req2.Host = "" // let http.Transport derive Host from URL.Host
	applyHeaders(req2, route)
	return rt.base.RoundTrip(req2)
}

// applyHeaders injects the backend's headers as set-defaults and appends its
// cookie. It never replaces a caller-set Range or Cookie, because URLSource
// issues Range requests and a caller cookie belongs to the caller.
func applyHeaders(req *http.Request, route *Route) {
	for k, v := range route.Headers {
		if req.Header.Get(k) == "" {
			req.Header.Set(k, v)
		}
	}
	if route.Cookie != "" {
		if existing := req.Header.Get("Cookie"); existing == "" {
			req.Header.Set("Cookie", route.Cookie)
		} else {
			req.Header.Set("Cookie", existing+"; "+route.Cookie)
		}
	}
}

// NewTransport builds an http.Transport whose TLS dial honours the per-request
// dial target, SNI name and skip flags.
func NewTransport() *http.Transport {
	d := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialAddr := addr
			if target, ok := ctx.Value(dialTargetKey{}).(string); ok && target != "" {
				dialAddr = target
			}
			serverName, _ := ctx.Value(sniNameKey{}).(string)
			if serverName == "" {
				if h, _, err := net.SplitHostPort(dialAddr); err == nil {
					serverName = h
				} else {
					serverName = dialAddr
				}
			}
			skip := false
			if v, ok := ctx.Value(tlsSkipKey{}).(bool); ok {
				skip = v
			}
			cfg := &tls.Config{
				MinVersion:         tls.VersionTLS12,
				ServerName:         serverName,
				InsecureSkipVerify: skip,
			}
			return tls.DialWithDialer(d, network, dialAddr, cfg)
		},
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	}
}

// NewClient returns an http.Client that routes matching URLs through their
// backend. base may be nil; when non-nil its Timeout, CheckRedirect, Jar and
// other settings are inherited and only the Transport is replaced, so this
// module stacks cleanly on top of the twimg client in serverapp.
//
// The timeout is inherited rather than set: URLSource performs sha256-verified
// full fetches of large media, and an aggressive timeout here would truncate
// them mid-stream.
func NewClient(router *Router, base *http.Client) (*http.Client, error) {
	rt, err := NewRoundTripper(router, NewTransport())
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
