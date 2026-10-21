package urlguard

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// GuardExternalURL is the SSRF guard for **any** caller-supplied outbound URL.
//
// Boundary: checks scheme, user-info, localhost, and every resolved IP (loopback, private,
// link-local, multicast, plus IPv4-mapped IPv6 unwrapping). It does **not** restrict certificate
// trust — a separate concern. Callers must also apply it per redirect hop.
func GuardExternalURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	return GuardPullURL(u)
}

// GuardPullURL SSRF protection: only allows public http(s), rejects internal/local/link-local
// addresses.
func GuardPullURL(u *url.URL) error {
	if u == nil {
		return fmt.Errorf("empty URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("only http/https supported (got %q)", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL must not contain user info (http://user@host is often used to bypass host checks)")
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("URL missing hostname")
	}
	if strings.EqualFold(host, "localhost") {
		return fmt.Errorf("pulling from localhost is forbidden")
	}
	// Both literal IPs and DNS must be blocked: beyond 127.0.0.1 there's also ::1,
	// 169.254.169.254, and DNS names resolving to internal addresses — so resolve first,
	// then check each IP individually, rather than doing hostname string matching.
	if ip := net.ParseIP(host); ip != nil {
		return GuardPullIP(ip)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("DNS resolution failed: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("DNS resolved to no addresses")
	}
	for _, ia := range ips {
		if err := GuardPullIP(ia.IP); err != nil {
			return err
		}
	}
	return nil
}

// GuardPullIP checks whether a single IP falls within the forbidden pull range.
func GuardPullIP(ip net.IP) error {
	if ip.IsLoopback() || ip.IsUnspecified() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return fmt.Errorf("pulling from internal/local address is forbidden (%s)", ip.String())
	}
	// IPv4-mapped IPv6 (::ffff:127.0.0.1) would bypass the above checks; explicitly unwrap
	// one layer.
	if v4 := ip.To4(); v4 != nil {
		private := v4[0] == 127 || v4[0] == 10 || v4[0] == 0 ||
			(v4[0] == 172 && v4[1]&0xf0 == 16) ||
			(v4[0] == 192 && v4[1] == 168) ||
			(v4[0] == 169 && v4[1] == 254) ||
			v4[0] >= 224
		if private {
			return fmt.Errorf("pulling from internal/local address is forbidden (%s)", v4.String())
		}
	}
	return nil
}

// SafeDialContext returns a dialing function that inspects the resolved IP addresses before connection
// establishment to block SSRF and DNS rebinding attacks.
func SafeDialContext(dialer *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 15 * time.Second}
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		// Direct IP literal
		if ip := net.ParseIP(host); ip != nil {
			if err := GuardPullIP(ip); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, addr)
		}
		// Resolve candidate IPs and validate every resolved target
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS resolution failed: %w", err)
		}
		if len(ips) == 0 {
			return nil, fmt.Errorf("DNS resolved to no addresses")
		}
		for _, ia := range ips {
			if err := GuardPullIP(ia.IP); err != nil {
				return nil, fmt.Errorf("connection to forbidden IP %s rejected: %w", ia.IP, err)
			}
		}
		// Connect to the first validated IP
		target := net.JoinHostPort(ips[0].IP.String(), port)
		return dialer.DialContext(ctx, network, target)
	}
}

// NewSafeTransport returns an http.Transport configured with SafeDialContext to block SSRF and DNS rebinding.
func NewSafeTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &http.Transport{
		DialContext:           SafeDialContext(dialer),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewSafeClient returns an http.Client equipped with NewSafeTransport and specified timeout.
func NewSafeClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: NewSafeTransport(),
	}
}

