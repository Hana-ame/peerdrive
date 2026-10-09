// url.go: pure rule matching and URL construction. No I/O, no globals, so the
// whole rewriting contract is testable without a network.
//
// The rewrite is **host-only plus an optional path prefix**: scheme, path,
// query and fragment are carried over by string slicing, not by url.Parse and
// re-String(). Parsing and re-serialising round-trips percent-encoding — a
// literal %2F becomes / — which corrupts gallery-token and tag paths that
// depend on the exact bytes. twimg.Entry.Rewrite in the sibling package takes
// the same approach for the same reason.
//
// Headers and cookies are set-default and append respectively: a caller-supplied
// Range or Cookie is never overwritten, because URLSource issues Range requests
// and its byte offsets must survive to the backend.

package exhentai

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strings"
)

// ErrInvalidURL marks input that is not an absolute http(s) URL. It is a
// sentinel on purpose: the caller must surface it rather than swallow it,
// because a silently "rewritten" non-URL hides a broken template.
var ErrInvalidURL = errors.New("exhentai: invalid url")

// Route is the outcome of matching one request URL against the live routing
// table.
type Route struct {
	// Target is the rewritten absolute URL.
	Target string
	// Headers are injected as set-defaults.
	Headers map[string]string
	// Cookie is appended to any existing Cookie header.
	Cookie string
	// BackendHost is the rewritten authority (without port), for logging.
	BackendHost string
	// LocalAddr overrides the TCP dial target. Empty = resolve BackendHost.
	LocalAddr string
	// SkipTLS disables certificate chain verification for this backend.
	SkipTLS bool
}

// Router resolves request URLs against the live snapshot. It is deliberately a
// thin adapter over SnapshotStore so it can be pointed at a test double.
type Router struct {
	store SnapshotStore
}

// NewRouter builds a Router over store. store must not be nil.
func NewRouter(store SnapshotStore) (*Router, error) {
	if store == nil {
		return nil, errors.New("exhentai: NewRouter requires a non-nil SnapshotStore")
	}
	return &Router{store: store}, nil
}

// Snapshot returns the live snapshot the router is currently using.
func (r *Router) Snapshot() Snapshot { return r.store.Load() }

// RuleFor returns the first rule matching host, or nil.
func (r *Router) RuleFor(host string) *Rule {
	snap := r.Snapshot()
	for i := range snap.Config.Rules {
		if snap.Config.Rules[i].matches(host) {
			return &snap.Config.Rules[i]
		}
	}
	return nil
}

// Route resolves raw into a Route, or returns (nil, nil) when the URL is not
// this module's business. It returns an error only for malformed absolute
// URLs, which callers should surface.
//
// The pass-through conditions, in order:
//
//  1. raw is not an absolute URL (no scheme) — error, not pass-through;
//  2. raw is not http(s) — pass through (rewriting ftp:// or data: into a
//     mirror host would be nonsense);
//  3. the pushed document says "enabled": false — pass through, which is the
//     remote kill switch;
//  4. no rule matches the host — pass through;
//  5. the rule matches but has no backends — pass through (normalize() rejects
//     such documents, so this only happens with a hand-built snapshot).
func (r *Router) Route(raw string) (*Route, error) {
	snap := r.Snapshot()
	if !snap.Config.IsEnabled() {
		return nil, nil
	}

	sep := strings.Index(raw, "://")
	if sep <= 0 {
		// A scheme with no "://" ("data:text/html", "mailto:a@b") is a valid
		// URL that is not ours: pass it through rather than erroring, so a
		// mixed page with data: image URIs does not break the request. A
		// string with no scheme at all is a malformed input and errors.
		if col := strings.IndexByte(raw, ':'); col > 0 {
			return nil, nil
		}
		return nil, fmt.Errorf("%w %q: no scheme", ErrInvalidURL, raw)
	}
	scheme := raw[:sep]
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_'
		if !ok {
			return nil, fmt.Errorf("%w %q: bad scheme", ErrInvalidURL, raw)
		}
	}
	up := strings.ToUpper(scheme)
	if up != "HTTP" && up != "HTTPS" {
		return nil, nil
	}

	rest := raw[sep+3:] // [userinfo@]authority + remainder
	authEnd := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' || rest[i] == '?' || rest[i] == '#' {
			authEnd = i
			break
		}
	}
	authority, remainder := rest[:authEnd], rest[authEnd:]

	// userinfo is meaningless once the authority changes; drop it.
	host := authority
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if host == "" {
		return nil, fmt.Errorf("%w %q: empty host", ErrInvalidURL, raw)
	}
	hostOnly, port, err := net.SplitHostPort(host)
	if err != nil {
		if strings.Contains(host, ":") {
			return nil, fmt.Errorf("%w %q: bad host %q: %v", ErrInvalidURL, raw, host, err)
		}
		hostOnly, port = host, ""
	}
	if !validHost(hostOnly) {
		return nil, fmt.Errorf("%w %q: bad host %q", ErrInvalidURL, raw, hostOnly)
	}
	if port != "" && !validPort(port) {
		return nil, fmt.Errorf("%w %q: bad port %q in host %q", ErrInvalidURL, raw, port, host)
	}
	_ = port // the source port is always replaced by the backend's

	rule := r.RuleFor(hostOnly)
	if rule == nil || len(rule.Backends) == 0 {
		return nil, nil
	}

	b := pickBackend(rule.Backends)

	// Path join: prefix + "/" + path, collapsing a doubled slash at the seam.
	// remainder starts with "/", "?" or "#".
	joined := remainder
	if b.PathPrefix != "" {
		prefix := strings.TrimRight(b.PathPrefix, "/")
		if strings.HasPrefix(remainder, "/") {
			joined = prefix + remainder
		} else {
			joined = prefix + "/" + remainder
		}
	}

	target := b.Scheme + "://" + b.targetHost() + joined
	return &Route{
		Target:      target,
		Headers:     b.Headers,
		Cookie:      b.Cookie,
		BackendHost: b.Host,
		LocalAddr:   b.LocalAddr,
		SkipTLS:     b.SkipTLS,
	}, nil
}

// validHost accepts hostnames and bracketed IPv6 literals, rejecting anything
// with whitespace or control characters. net.SplitHostPort splits but never
// validates the characters of a host, so a URL such as "https://bad host/x"
// would otherwise reach the backend as an unparseable authority.
func validHost(h string) bool {
	if h == "" {
		return false
	}
	for i := 0; i < len(h); i++ {
		// Explicit loop so the rejected character set stays readable: a single
		// backslash-quote escape inside a string literal like this is easy to
		// mangle.
		c := h[i]
		if c <= ' ' || c == '"' || c == '<' || c == '>' || c == '{' || c == '}' ||
			c == '|' || c == '^' || c == '`' || c == '\\' || c == '/' {
			return false
		}
	}
	if strings.HasPrefix(h, "[") {
		return strings.HasSuffix(h, "]")
	}
	for _, part := range strings.Split(h, ".") {
		if part == "" || len(part) > 63 {
			return false
		}
	}
	return true
}

// pickBackend picks one backend from a weighted list. With a single backend it
// is the identity. The weight draw is deterministic in the number of entries
// but otherwise random, so traffic spreads without a shared counter.
//
// This is a pure weighted pick, not a health-aware balancer: backend failure
// detection is out of scope because the failure signal here is HTTP-level and
// would need a circuit breaker to act on. The operator's tool for failing over
// is pushing a new document with the backends reordered — which is exactly the
// mechanism the module exists to make cheap.
func pickBackend(backends []Backend) Backend {
	if len(backends) == 1 {
		return backends[0]
	}
	total := 0
	for _, b := range backends {
		w := b.Weight
		if w <= 0 {
			w = 1
		}
		total += w
	}
	if total <= 0 {
		return backends[0]
	}
	roll := weightedRoll(total)
	for _, b := range backends {
		w := b.Weight
		if w <= 0 {
			w = 1
		}
		roll -= w
		if roll < 0 {
			return b
		}
	}
	return backends[len(backends)-1]
}

// weightedRoll is indirection so tests can pin the draw.
var weightedRoll = func(total int) int { return rand.IntN(total) }

// pickBackend's randomness comes from math/rand/v2, which is auto-seeded since
// Go 1.20, so concurrent requests need no explicit seeding. The distribution
// only has to be spread, not secure.
