package httpd

import (
	"net"
	"strings"
)

// JoinHostPort builds a "host:port" listen address, using net.JoinHostPort so
// IPv6 hosts come out bracketed ("[::1]:8080").
//
// Extracted because three call sites each joined the main service's host and
// port by hand; keeping one implementation means an address cannot be formed
// one way in one place and another way in the next.
func JoinHostPort(host, port string) string {
	return net.JoinHostPort(host, port)
}

// IsLoopback reports whether a listen host binds to loopback only.
//
// An empty host means "all interfaces" (0.0.0.0), which is NOT loopback. That
// asymmetry is what makes it useful: validateAuthStartup uses it to refuse to
// serve a non-loopback address with no auth backend configured.
func IsLoopback(host string) bool {
	if host == "" {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// NormalizePort turns a bare port or a host:port string into a listen address,
// accepting both spellings callers historically used.
//
// The default ":4000" is the registration server's historical port, preserved
// so that deployment scripts that set PORT=4000 without a host keep working —
// "HOST=127.0.0.1 PORT=4000" must not become "127.0.0.1:127.0.0.1:4000".
func NormalizePort(p string) string {
	if p == "" {
		return ":4000"
	}
	if strings.Contains(p, ":") {
		return p // already host:port or :port
	}
	if strings.Contains(p, ".") {
		return p + ":4000" // bare IP, no port
	}
	return ":" + p
}
