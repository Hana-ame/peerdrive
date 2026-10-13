package echcore

import "net/http"

// RoundTripper is an http.RoundTripper that sends every request through the
// in-process ECH domain-fronting client.
//
// It exists so that modules which already hold an *http.Client can switch to
// ECH egress without changing their call sites:
//
//	client := &http.Client{Transport: echcore.RoundTripper{}}
//
// This replaces the previous "local ech-proxy listener" pattern, in which a
// peerdrive module downloaded and spawned an ech-proxy subprocess binding
// 127.0.0.1:8443 with a self-signed certificate, rewrote every request host to
// *.l.moonchan.xyz, and dialed the loopback address. That pattern forced
// InsecureSkipVerify on the dialer (gosec G402) because the loopback proxy's
// certificate is not in the system trust store.
//
// With in-process ECH there is no local listener, no subprocess, and no
// self-signed certificate: the TLS handshake is performed here against
// cloudflare-ech.com's own ECH configuration, so the upstream certificate is
// verified with the default system roots and no verification is skipped
// anywhere. Requests are sent to the real upstream host as-is; no host
// rewriting is needed because domain fronting is what ECH provides.
type RoundTripper struct {
	// IPMode pins the upstream egress family ("v4" or "v6"). Empty means use
	// the global configuration set by SetIPMode / Config.IPMode.
	IPMode string
}

// RoundTrip implements http.RoundTripper. The caller's request is used as-is —
// no host rewrite, no URL mutation. Redirects are followed by the enclosing
// http.Client, each hop re-entering this RoundTripper.
func (rt RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if rt.IPMode != "" {
		return Do(req, rt.IPMode)
	}
	return Do(req)
}
