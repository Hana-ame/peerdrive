package twimg

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRewrite covers the URL rewrite contract: pbs.twimg.com/<path>?<query> is routed
// to https://twimg-pbs.l.moonchan.xyz:8443/<path>?<query> with the authority as the only
// changed byte-run. Scheme is preserved (https never downgrades), and path/query are
// copied verbatim — no url.Parse round-trip, which would collapse %2F into /.
func TestRewrite(t *testing.T) {
	e := DefaultEntry()

	tests := []struct {
		name    string
		in      string
		out     string
		changed bool
		errStr  string
	}{
		// --- pure path ---
		{name: "pure path",
			in:  "https://pbs.twimg.com/media/A.jpg",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", changed: true},
		{name: "nested path",
			in:  "https://pbs.twimg.com/media/profile_images/123/square_50.jpg",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/profile_images/123/square_50.jpg", changed: true},
		{name: "root path",
			in:  "https://pbs.twimg.com/",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/", changed: true},
		{name: "no path at all",
			in:  "https://pbs.twimg.com",
			out: "https://twimg-pbs.l.moonchan.xyz:8443", changed: true},

		// --- with query ---
		{name: "query preserved",
			in:  "https://pbs.twimg.com/media/A.jpg?name=foo&x=1",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg?name=foo&x=1", changed: true},
		{name: "query with no path",
			in:  "https://pbs.twimg.com?name=foo",
			out: "https://twimg-pbs.l.moonchan.xyz:8443?name=foo", changed: true},

		// --- percent-encoding must survive byte-for-byte ---
		// The ech-proxy v1.2.1 release notes call out tag-search paths where Go's URL
		// re-encoder would turn %2F into / and break upstream routing. These must survive.
		{name: "percent-encoded slash preserved",
			in:      "https://pbs.twimg.com/api/search/%20%24tag%3A%E4%BA%B2%E7%83%AD%2F%E7%94%9C%E8%9C%9C%24?q=1",
			out:     "https://twimg-pbs.l.moonchan.xyz:8443/api/search/%20%24tag%3A%E4%BA%B2%E7%83%AD%2F%E7%94%9C%E8%9C%9C%24?q=1",
			changed: true},
		{name: "plus and space preserved",
			in:  "https://pbs.twimg.com/a?q=1%2B2&x=%20",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/a?q=1%2B2&x=%20", changed: true},

		// --- fragment preserved ---
		{name: "fragment preserved",
			in:  "https://pbs.twimg.com/media/A.jpg#section-2",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg#section-2", changed: true},

		// --- source port is replaced by the entry port ---
		{name: "explicit source port replaced",
			in:  "https://pbs.twimg.com:443/media/A.jpg",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", changed: true},
		{name: "non-standard source port replaced",
			in:  "http://pbs.twimg.com:8080/media/A.jpg?q=1",
			out: "http://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg?q=1", changed: true},

		// --- scheme is preserved, never downgraded ---
		{name: "https stays https",
			in:  "https://pbs.twimg.com/media/A.jpg",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", changed: true},
		{name: "http stays http",
			in:  "http://pbs.twimg.com/media/A.jpg",
			out: "http://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", changed: true},

		// --- host match is case-insensitive; path case is untouched ---
		{name: "host case-insensitive, path case preserved",
			in:  "https://PBS.twimg.com/Media/A.JPG?q=Q",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/Media/A.JPG?q=Q", changed: true},

		// --- non-pbs hosts are not rewritten ---
		{name: "other domain unchanged",
			in: "https://x.com/api/1/statuses", out: "https://x.com/api/1/statuses", changed: false},
		{name: "sibling twimg CDN unchanged",
			in:  "https://video-cf.twimg.com/ext_tw_video/v/a.mp4",
			out: "https://video-cf.twimg.com/ext_tw_video/v/a.mp4", changed: false},
		{name: "suffix lookalike unchanged",
			in:  "https://pbs.twimg.com.evil.com/media/A.jpg",
			out: "https://pbs.twimg.com.evil.com/media/A.jpg", changed: false},
		{name: "userinfo points elsewhere unchanged",
			in:  "https://pbs.twimg.com@evil.com/media/A.jpg",
			out: "https://pbs.twimg.com@evil.com/media/A.jpg", changed: false},
		{name: "non-http scheme passes through",
			in: "ipfs://QmABC/media/A.jpg", out: "ipfs://QmABC/media/A.jpg", changed: false},

		// --- userinfo on the real host is dropped (endpoint changes entirely) ---
		{name: "userinfo dropped when host matches",
			in:  "https://user:pass@pbs.twimg.com/media/A.jpg",
			out: "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", changed: true},

		// --- malformed input is an explicit error, never a silent pass-through ---
		{name: "empty string", in: "", errStr: "no scheme"},
		{name: "no scheme", in: "not a url", errStr: "no scheme"},
		{name: "empty authority", in: "https://", errStr: "empty host"},
		{name: "scheme only no host", in: "https:///media/A.jpg", errStr: "empty host"},
		{name: "bad source port", in: "https://pbs.twimg.com:badport/media/A.jpg", errStr: "bad port"},
		{name: "zero length scheme", in: "://pbs.twimg.com/media/A.jpg", errStr: "no scheme"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := e.Rewrite(tc.in)
			if tc.errStr != "" {
				require.Error(t, err, "expected an error for %q", tc.in)
				assert.ErrorIs(t, err, ErrInvalidURL)
				assert.Contains(t, err.Error(), tc.errStr)
				assert.Equal(t, tc.in, got, "input must be echoed back unchanged")
				assert.False(t, changed)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.changed, changed, "changed flag for %q", tc.in)
			assert.Equal(t, tc.out, got, "rewrite of %q", tc.in)
		})
	}
}

// TestRewrite_NoDoubleRewrite ensures rewriting twice is idempotent — the entry host is
// not treated as a rewrite candidate.
func TestRewrite_NoDoubleRewrite(t *testing.T) {
	e := DefaultEntry()
	once, changed, err := e.Rewrite("https://pbs.twimg.com/media/A.jpg?x=1")
	require.NoError(t, err)
	require.True(t, changed)
	twice, changed2, err := e.Rewrite(once)
	require.NoError(t, err)
	assert.False(t, changed2)
	assert.Equal(t, once, twice)
}

// TestRewrite_DoesNotCrossSchemeBoundary guards the "https 保持 HTTPS 不降级" contract:
// no output for an https input may ever start with http://.
func TestRewrite_DoesNotCrossSchemeBoundary(t *testing.T) {
	e := DefaultEntry()
	for _, in := range []string{
		"https://pbs.twimg.com/a",
		"https://pbs.twimg.com:443/a?b=c",
		"https://pbs.twimg.com/a/b/c?d=e#f",
	} {
		got, changed, err := e.Rewrite(in)
		require.NoError(t, err)
		require.True(t, changed, in)
		assert.False(t, strings.HasPrefix(got, "http://"), "https input downgraded: %q -> %q", in, got)
		assert.True(t, strings.HasPrefix(got, "https://"), got)
	}
}

// TestEntry_Validate catches misconfiguration before any I/O or subprocess is spawned.
func TestEntry_Validate(t *testing.T) {
	tests := []struct {
		name   string
		entry  Entry
		errStr string
	}{
		{name: "default entry is valid", entry: DefaultEntry()},
		{name: "empty SrcHost", entry: DefaultEntry(), errStr: "SrcHost"},
		{name: "empty EntryHost", entry: DefaultEntry(), errStr: "EntryHost"},
		{name: "empty Port", entry: DefaultEntry(), errStr: "Port"},
		{name: "empty ListenAddr", entry: DefaultEntry(), errStr: "ListenAddr"},
		{name: "bad ListenAddr", entry: DefaultEntry(), errStr: "ListenAddr"},
		{name: "bad entry port", entry: DefaultEntry(), errStr: "Entry.Port"},
	}
	// Patch fields per case by name for readability.
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := tc.entry
			switch tc.name {
			case "empty SrcHost":
				e.SrcHost = ""
			case "empty EntryHost":
				e.EntryHost = ""
			case "empty Port":
				e.Port = ""
			case "empty ListenAddr":
				e.ListenAddr = ""
			case "bad ListenAddr":
				e.ListenAddr = "not-a-port"
			case "bad entry port":
				e.Port = "badport"
			}
			err := e.Validate()
			if tc.errStr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.errStr)
			}
		})
	}
}

// TestDefaultAsset maps the published release table and refuses unknown platforms.
// skipIfUnsupportedPlatform skips tests that build the module for the platform they run on.
// The ech-proxy release does not ship an asset for every host (no darwin/arm64), and on
// those hosts the module correctly returns an explicit error rather than picking a foreign
// binary. That refusal is pinned by TestDefaultAsset; re-asserting a successful build here
// would only make the macos-arm64 CI cell fail.
func skipIfUnsupportedPlatform(t *testing.T) {
	t.Helper()
	if _, err := DefaultAssetForCurrent(); err != nil {
		t.Skipf("ech-proxy publishes no asset for %s/%s (%v); the refusal path is covered by TestDefaultAsset",
			runtime.GOOS, runtime.GOARCH, err)
	}
}

func TestDefaultAsset(t *testing.T) {
	tests := []struct {
		goos, goarch string
		want         string
	}{
		{"windows", "amd64", "ech-proxy-windows-amd64.exe"},
		{"windows", "arm64", "ech-proxy-windows-arm64.exe"},
		{"linux", "amd64", "ech-proxy-linux-amd64"},
		{"linux", "arm64", "ech-proxy-linux-arm64"},
		{"darwin", "amd64", "ech-proxy-darwin-amd64"},
	}
	for _, tc := range tests {
		got, err := DefaultAsset(tc.goos, tc.goarch)
		require.NoError(t, err)
		assert.Equal(t, tc.want, got)
	}
	for _, pair := range [][2]string{{"windows", "386"}, {"linux", "386"}, {"darwin", "arm64"}, {"freebsd", "amd64"}} {
		_, err := DefaultAsset(pair[0], pair[1])
		require.Error(t, err, "GOOS=%s GOARCH=%s must be refused", pair[0], pair[1])
		assert.Contains(t, err.Error(), "no published ech-proxy asset")
	}
}
