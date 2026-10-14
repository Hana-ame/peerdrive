// transport.go: installs the routing table on an http.Client.
//
// The RoundTripper clones the request before mutating it: url.go holds onto the
// request it builds, so mutating in place would corrupt the caller's request.
// Non-matching requests are handed to the base transport untouched, which is
// what makes the disabled module a no-op and the enabled module a pure
// additive layer that never touches unrelated traffic.
//
// The rewritten backend host is dialed directly over TLS with the default
// system roots (ex.4545810.xyz is Cloudflare-fronted with a valid chain).
// There is no local-proxy mode: the ech-proxy listener that used to serve
// *.l.moonchan.xyz has been removed, so there is no loopback dial, no
// self-signed certificate, and no verification to skip. TLS is therefore left
// to the base transport's own *tls.Config instead of a per-request
// DialTLSContext.
package exhentai

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

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

	req2 := req.Clone(req.Context())
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

// NewClient returns an http.Client that routes matching URLs through their
// backend. base may be nil; when non-nil its Timeout, CheckRedirect, Jar and
// other settings are inherited and only the Transport is replaced, so this
// module stacks cleanly on top of the twimg client in serverapp.
//
// The timeout is inherited rather than set: URLSource performs sha256-verified
// full fetches of large media, and an aggressive timeout here would truncate
// them mid-stream.
func NewClient(router *Router, base *http.Client) (*http.Client, error) {
	var client http.Client
	var baseRT http.RoundTripper
	if base != nil {
		client = *base
		baseRT = base.Transport
	}
	rt, err := NewRoundTripper(router, baseRT)
	if err != nil {
		return nil, err
	}
	client.Transport = rt
	return &client, nil
}
