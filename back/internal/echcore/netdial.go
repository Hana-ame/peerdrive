// Package echcore provides an ECH (Encrypted Client Hello) HTTP client with
// SNI camouflage, IP family control, and retry-aware DoH bootstrap.
//
// Ported from Hana-ame/ech-proxy (commit fc2ccb1) — echproxy/ech/client.go
// and echproxy/netdial/netdial.go. The gin-dependent proxy layer (router,
// cookie, rewriter, sw) is deliberately NOT included; this package only
// provides the ECH/SNI dialing primitives that peerdrive's media-node and
// source system need.
//
// The netdial sub-package from ech-proxy is folded into this package to keep
// peerdrive's internal structure flat.
package echcore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Operation timeouts and retries: DoH fetching, dialing, DNS, and config
// loading are unified to 5 minutes with retries on transient failures.
const (
	OpTimeout     = 5 * time.Minute
	RetryAttempts = 3
	RetryBackoff  = 2 * time.Second
)

// dnsServers is a list of public DNS servers tried sequentially.
var dnsServers = []string{
	"8.8.8.8:53",
	"1.1.1.1:53",
	"223.5.5.5:53",
	"114.114.114.114:53",
}

// newResolver constructs a resolver that routes queries via public DNS
// (bypassing local [::1]:53). Dials with the network parameter requested by
// Go resolver (udp/tcp): when receiving a TC (truncated) response, Resolver
// retries over TCP.
func newResolver() *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{Timeout: OpTimeout}
			var err error
			for _, dns := range dnsServers {
				var conn net.Conn
				conn, err = d.DialContext(ctx, network, dns)
				if err == nil {
					return conn, nil
				}
			}
			return nil, err
		},
	}
}

// resolver and rootPool global caches: Dialer() and Transport() previously
// constructed new resolvers and cert pools on each call. Cert pool loading
// (SystemCertPool + reading files) is expensive on hot paths. Both are
// concurrency-safe, so reusing a single global instance suffices.
var (
	resolverOnce sync.Once
	resolver     *net.Resolver

	rootPoolOnce sync.Once
	rootPool     *x509.CertPool
)

func getResolver() *net.Resolver {
	resolverOnce.Do(func() { resolver = newResolver() })
	return resolver
}

func getRootPool() *x509.CertPool {
	rootPoolOnce.Do(func() { rootPool = rootCAs() })
	return rootPool
}

// Dialer returns a net.Dialer using public DNS, suitable for environments
// without resolv.conf like Termux/Android.
func Dialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   OpTimeout,
		KeepAlive: 30 * time.Second,
		Resolver:  getResolver(),
	}
}

// rootCAs loads system root certificates and appends Termux's CA bundle if present.
func rootCAs() *x509.CertPool {
	pool, _ := x509.SystemCertPool()
	if pool == nil {
		pool = x509.NewCertPool()
	}
	paths := []string{
		"/data/data/com.termux/files/usr/etc/tls/cert.pem",
		os.Getenv("PREFIX") + "/etc/tls/cert.pem",
	}
	for _, p := range paths {
		if p == "/etc/tls/cert.pem" || p == "" {
			continue
		}
		if pem, err := os.ReadFile(p); err == nil {
			pool.AppendCertsFromPEM(pem)
		}
	}
	return pool
}

var (
	transportOnce sync.Once
	cachedTransport *http.Transport
)

// Transport returns an http.Transport suitable for Termux (public DNS + Termux CA).
// The transport is globally cached and reused to share connection pools across callers.
func Transport() *http.Transport {
	transportOnce.Do(func() {
		cachedTransport = &http.Transport{
			DialContext: Dialer().DialContext,
			TLSClientConfig: &tls.Config{
				RootCAs: getRootPool(),
			},
			MaxIdleConns:    100,
			IdleConnTimeout: OpTimeout,
			ForceAttemptHTTP2: true,
		}
	})
	return cachedTransport
}

// HTTPClient returns an http.Client suitable for Termux.
// Named HTTPClient to avoid conflict with the ECH Client struct in ech.go.
func HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:   timeout,
		Transport: Transport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// sleepCtx sleeps for duration d while respecting ctx cancellation; returns
// false if ctx was cancelled.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// Retry retries fn until success, context cancellation, or max attempts
// reached; sleeps backoff between failures (cancellable via ctx). Used for
// DoH, dialing, and config fetching prone to transient network jitter.
func Retry[T any](ctx context.Context, attempts int, backoff time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	var last error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		if i > 0 && !sleepCtx(ctx, backoff) {
			return zero, ctx.Err()
		}
		v, err := fn()
		if err == nil {
			return v, nil
		}
		last = err
	}
	return zero, last
}
