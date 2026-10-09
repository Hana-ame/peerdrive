package httpd

import (
	"net/http"
	"net/url"
	"strings"
)

// StripPrefix and WithPrefix are the two primitives behind "mount several
// services on one port". Go 1.22's ServeMux pattern grammar takes
// "METHOD /path" strings, and re-mounting a subtree means rewriting both the
// pattern (outward) and the incoming request's URL.Path (inward) — otherwise
// the inner ServeMux, whose routes are written as "/ping", never matches
// "/_reg/ping" and the relocated endpoint is a silent 404.
//
// Extracted because they are pure request-shape machinery with no peerdrive
// knowledge in them: the decision of *which* routes collide and *what prefix*
// to move them under stays with the caller (services.UnifiedMux, which owns
// regPrefix and prefixConflicts).
func StripPrefix(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			stripped := strings.TrimPrefix(r.URL.Path, prefix)
			if stripped == "" {
				// Exactly the prefix (e.g. GET /_signal): stripping yields "".
				// signalserver.HandleDashboard hard-tests Path != "/" and would
				// 404 the empty string, so "/prefix" maps to "/" — the meaning
				// of mounting a whole subtree at a prefix.
				stripped = "/"
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = stripped
			if r2.URL.RawPath != "" {
				r2.URL.RawPath = stripped
			}
			r = r2
		}
		h.ServeHTTP(w, r)
	})
}

// WithPrefix adds prefix to a "METHOD /path" or "/path" ServeMux pattern:
// "GET /ping" -> "GET /_reg/ping".
//
// It finds the method with IndexByte rather than slicing by length — a
// p[4:] style split breaks the moment a method is longer or shorter than GET
// and quietly drops the method word, producing an invalid " /auth/register".
func WithPrefix(prefix, pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return pattern[:i+1] + prefix + pattern[i+1:]
	}
	return prefix + pattern
}
