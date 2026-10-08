// Package twimg is an **optional** module that routes pbs.twimg.com (Twitter image CDN)
// traffic through a locally-managed ech-proxy subprocess.
//
// Why this module exists: pbs.twimg.com is unreachable from mainland China. ech-proxy
// (github.com/Hana-ame/ech-proxy) is an ECH domain-fronting reverse proxy that binds
// 127.0.0.1:8443 and routes "*.l.moonchan.xyz" entry hosts to real upstreams. The entry
// for the Twitter image CDN is "twimg-pbs.l.moonchan.xyz" → pbs.twimg.com.
//
// This lives in internal/echproxy/twimg: the parent package is the sibling iwara module
// (a separate ech-proxy consumer). Each upstream domain keeps its own subpackage so the
// two don't share type names for unrelated lifecycle code.
//
// Deliberate distinction from back/ech: back/ech does ECH **in-process** (media-node's
// transport); this module runs ech-proxy as an **external** Windows/Linux subprocess and
// owns its lifecycle (download → sha256 verify → spawn → stop). Do not merge them.
//
// Default off. Enable with PEERDRIVE_ECH_PROXY_ENABLE=true.
package twimg

import (
	"errors"
	"fmt"
	"net"
	"runtime"
	"strconv"
	"strings"
)

// Defaults. The release asset names below are verified against the v1.3.0 GitHub
// release (https://github.com/Hana-ame/ech-proxy/releases/tag/v1.3.0) and its
// checksums.txt — do not rename without re-checking that file.
const (
	// DefaultRepo is the GitHub owner/repo used to build release asset URLs.
	DefaultRepo = "Hana-ame/ech-proxy"
	// DefaultVersion is the ech-proxy release tag the module downloads.
	DefaultVersion = "v1.3.0"
	// DefaultSrcHost is the upstream host this module rewrites.
	DefaultSrcHost = "pbs.twimg.com"
	// DefaultEntryHost is ech-proxy's entry host for pbs.twimg.com.
	DefaultEntryHost = "twimg-pbs.l.moonchan.xyz"
	// DefaultPort is ech-proxy's default (TLS) listen port.
	DefaultPort = "8443"
	// DefaultListenAddr is where the managed subprocess binds.
	DefaultListenAddr = "127.0.0.1:8443"
	// DefaultIPMode pins the upstream egress family. ech-proxy's own default is
	// "auto", which follows the OS resolver; v4 is the documented Pixiv fix and the
	// safe choice for a media proxy.
	DefaultIPMode = "v4"
	// ChecksumFile is the release asset that lists the sha256 of every other asset.
	ChecksumFile = "checksums.txt"
)

// assets maps (GOOS, GOARCH) → release asset name. The Windows .exe entries are the
// stated target of this module; the others are included because the release publishes
// them, which also makes the module exercisable on a Linux CI box.
var assets = map[string]string{
	"windows/amd64": "ech-proxy-windows-amd64.exe",
	"windows/arm64": "ech-proxy-windows-arm64.exe",
	"linux/amd64":   "ech-proxy-linux-amd64",
	"linux/arm64":   "ech-proxy-linux-arm64",
	"linux/aarch64": "ech-proxy-linux-aarch64",
	"darwin/amd64":  "ech-proxy-darwin-amd64",
}

// DefaultAsset picks the release asset for the current platform (or the given one).
// Unknown platforms are an explicit error — never silently pick a foreign binary.
func DefaultAsset(goos, goarch string) (string, error) {
	name, ok := assets[goos+"/"+goarch]
	if !ok {
		return "", fmt.Errorf("echproxy: no published ech-proxy asset for GOOS=%s GOARCH=%s", goos, goarch)
	}
	return name, nil
}

// DefaultAssetForCurrent is DefaultAsset(runtime.GOOS, runtime.GOARCH).
func DefaultAssetForCurrent() (string, error) { return DefaultAsset(runtime.GOOS, runtime.GOARCH) }

// ErrInvalidURL is returned by Entry.Rewrite for input that is not an absolute http(s) URL.
// It is deliberately a sentinel: the caller must surface it rather than swallow it, because
// a silently rewritten URL hides a broken URLSource template.
var ErrInvalidURL = errors.New("echproxy: invalid url")

// validPort reports whether p is a decimal TCP port in [1, 65535].
//
// net.SplitHostPort does not validate port numbers in the Go version this project
// builds with (it accepts "badport", "0", "65536" and " 8080"), so every place a
// port is accepted must call this.
func validPort(p string) bool {
	n, err := strconv.Atoi(p)
	return err == nil && n >= 1 && n <= 65535
}

// Entry describes how SrcHost URLs are rewritten and routed.
type Entry struct {
	// SrcHost is the upstream host to rewrite (pbs.twimg.com).
	SrcHost string
	// EntryHost is the ech-proxy entry host to rewrite to (twimg-pbs.l.moonchan.xyz).
	EntryHost string
	// Port is the entry port (8443). Always emitted explicitly in the rewritten URL so
	// the routing target never depends on the scheme's implicit default port.
	Port string
	// ListenAddr is where the managed subprocess binds (127.0.0.1:8443). The transport
	// dials this address instead of resolving EntryHost, so local development does not
	// depend on "*.l.moonchan.xyz → 127.0.0.1" DNS being in place.
	ListenAddr string
	// SkipTLS accepts the local proxy's certificate without chain verification. Default
	// true: the proxy binds 127.0.0.1 and the entry host resolves to it, so in a local
	// deployment the cert is self-signed or issued for a name that is not chain-trusted
	// by the node's store. Set false to require a valid chain (e.g. a deployment that
	// owns *.moonchan.xyz and a real CA).
	SkipTLS bool
}

// Validate reports configuration errors before any network I/O or process is spawned.
func (e Entry) Validate() error {
	if strings.TrimSpace(e.SrcHost) == "" {
		return errors.New("echproxy: Entry.SrcHost is empty")
	}
	if strings.TrimSpace(e.EntryHost) == "" {
		return errors.New("echproxy: Entry.EntryHost is empty")
	}
	if strings.TrimSpace(e.Port) == "" {
		return errors.New("echproxy: Entry.Port is empty")
	}
	if _, _, err := net.SplitHostPort(e.EntryHost + ":" + e.Port); err != nil {
		return fmt.Errorf("echproxy: Entry.EntryHost %q is not a valid host: %w", e.EntryHost, err)
	}
	if !validPort(e.Port) {
		return fmt.Errorf("echproxy: Entry.Port %q is not a valid port (1-65535)", e.Port)
	}
	if strings.TrimSpace(e.ListenAddr) == "" {
		return errors.New("echproxy: Entry.ListenAddr is empty")
	}
	laHost, laPort, err := net.SplitHostPort(e.ListenAddr)
	if err != nil {
		return fmt.Errorf("echproxy: Entry.ListenAddr %q is not a valid host:port: %w", e.ListenAddr, err)
	}
	if !validPort(laPort) {
		return fmt.Errorf("echproxy: Entry.ListenAddr %q has an invalid port %q", e.ListenAddr, laPort)
	}
	if laHost == "" {
		return fmt.Errorf("echproxy: Entry.ListenAddr %q has an empty host", e.ListenAddr)
	}
	return nil
}

// DefaultEntry returns the documented entry for pbs.twimg.com.
func DefaultEntry() Entry {
	return Entry{
		SrcHost:    DefaultSrcHost,
		EntryHost:  DefaultEntryHost,
		Port:       DefaultPort,
		ListenAddr: DefaultListenAddr,
		SkipTLS:    true,
	}
}

// Rewrite routes raw through the ech-proxy entry when it targets SrcHost.
//
// It returns (rewritten, true, nil) when the URL was changed, (raw, false, nil) when it
// was not a rewrite candidate, and an error when the input is not an absolute URL.
//
// Only the authority changes. Scheme, path, query and fragment are copied **byte-for-byte**
// by string slicing, not by url.Parse/Rebuild — rebuilding round-trips percent-encoding
// (%2F would become /), which would corrupt tag-search style paths that the ech-proxy
// release notes explicitly call out as needing to survive.
//
// Scheme is preserved, so https stays https (never downgraded to http) and http stays
// http. An explicit source port is discarded in favour of the entry port.
func (e Entry) Rewrite(raw string) (string, bool, error) {
	sep := strings.Index(raw, "://")
	if sep <= 0 {
		return raw, false, fmt.Errorf("%w %q: no scheme", ErrInvalidURL, raw)
	}
	scheme := raw[:sep]
	for i := 0; i < len(scheme); i++ {
		c := scheme[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '.' || c == '_'
		if !ok {
			return raw, false, fmt.Errorf("%w %q: bad scheme", ErrInvalidURL, raw)
		}
	}
	// Only http(s) can reach the proxy. Anything else is passed through untouched —
	// rewriting ftp:// or ipfs:// into the entry host would be nonsense.
	if up := strings.ToUpper(scheme); up != "HTTP" && up != "HTTPS" {
		return raw, false, nil
	}
	rest := raw[sep+3:] // [userinfo@]authority + remainder

	// The authority ends at the first '/', '?' or '#' — everything after it is the
	// path?query#fragment, preserved verbatim.
	authEnd := len(rest)
	for i := 0; i < len(rest); i++ {
		if rest[i] == '/' || rest[i] == '?' || rest[i] == '#' {
			authEnd = i
			break
		}
	}
	authority, remainder := rest[:authEnd], rest[authEnd:]

	// userinfo is meaningless after the endpoint changes, so drop it rather than carry
	// it into the entry URL.
	host := authority
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if host == "" {
		return raw, false, fmt.Errorf("%w %q: empty host", ErrInvalidURL, raw)
	}

	// SplitHostPort splits but does NOT validate the port number (it accepts "badport",
	// "0", "65536", " 8080" without complaint), so validate the port explicitly.
	hostOnly, port, err := net.SplitHostPort(host)
	if err != nil {
		if strings.Contains(host, ":") {
			return raw, false, fmt.Errorf("%w %q: bad host %q: %v", ErrInvalidURL, raw, host, err)
		}
		hostOnly, port = host, ""
	}
	if port != "" && !validPort(port) {
		return raw, false, fmt.Errorf("%w %q: bad port %q in host %q", ErrInvalidURL, raw, port, host)
	}
	_ = port // source port is always replaced by Entry.Port

	if !strings.EqualFold(hostOnly, e.SrcHost) {
		return raw, false, nil
	}
	return scheme + "://" + e.EntryHost + ":" + e.Port + remainder, true, nil
}
