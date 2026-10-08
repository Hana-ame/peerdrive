package echproxy

import "testing"

// TestRewrite covers the URL rewriting table.
//
// Discovery background: the module rewrites iwara.tv URLs to go through the
// ech-proxy entry host. The rewrite must be host-only (path/query/fragment
// preserved byte-for-byte) because iwara's signed file URLs carry query
// parameters that must survive. The table exercises the four rewrite classes
// named in the upstream.json iwara entry, plus the "leave non-iwara alone"
// path that keeps the default-off behaviour identical to baseline.
func TestRewrite(t *testing.T) {
	r := NewRewriter("", "", "", "")

	tests := []struct {
		name     string
		in       string
		want     string
		wantOK   bool
	}{
		// --- iwara.tv main host ---
		{
			name:   "www.iwara.tv main rewrite",
			in:     "https://www.iwara.tv/videos/abc",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc",
			wantOK: true,
		},
		{
			name:   "bare iwara.tv rewrite",
			in:     "https://iwara.tv/videos/abc",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc",
			wantOK: true,
		},
		{
			name:   "www.iwara.tv with query preserved",
			in:     "https://www.iwara.tv/videos/abc?sort=date&page=2&rating=ecchi",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc?sort=date&page=2&rating=ecchi",
			wantOK: true,
		},
		{
			name:   "www.iwara.tv query with special chars preserved",
			in:     "https://www.iwara.tv/videos/abc?q=a%20b&x=y%26z",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc?q=a%20b&x=y%26z",
			wantOK: true,
		},
		{
			name:   "www.iwara.tv fragment preserved",
			in:     "https://www.iwara.tv/videos/abc?x=1#section",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc?x=1#section",
			wantOK: true,
		},
		{
			name:   "www.iwara.tv http upgraded to https",
			in:     "http://www.iwara.tv/videos/abc",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc",
			wantOK: true,
		},
		{
			name:   "www.iwara.tv existing port stripped and replaced",
			in:     "https://www.iwara.tv:443/videos/abc",
			want:   "https://iwara.l.moonchan.xyz:8443/videos/abc",
			wantOK: true,
		},

		// --- iwara.tv subdomains (wildcard) ---
		{
			name:   "api.iwara.tv wildcard rewrite",
			in:     "https://api.iwara.tv/video/abc",
			want:   "https://iwara-api.l.moonchan.xyz:8443/video/abc",
			wantOK: true,
		},
		{
			name:   "cdn.iwara.tv wildcard rewrite",
			in:     "https://cdn.iwara.tv/files/abc.mp4",
			want:   "https://iwara-cdn.l.moonchan.xyz:8443/files/abc.mp4",
			wantOK: true,
		},
		{
			name:   "subdomain with query preserved",
			in:     "https://api.iwara.tv/video/abc?expires=123&sig=xyz",
			want:   "https://iwara-api.l.moonchan.xyz:8443/video/abc?expires=123&sig=xyz",
			wantOK: true,
		},
		{
			name:   "multi-level subdomain wildcard rewrite",
			in:     "https://foo.bar.iwara.tv/x",
			want:   "https://iwara-foo.bar.l.moonchan.xyz:8443/x",
			wantOK: true,
		},
		{
			name:   "subdomain with port",
			in:     "https://api.iwara.tv:443/video/abc",
			want:   "https://iwara-api.l.moonchan.xyz:8443/video/abc",
			wantOK: true,
		},
		{
			name:   "subdomain http upgraded to https",
			in:     "http://api.iwara.tv/video/abc",
			want:   "https://iwara-api.l.moonchan.xyz:8443/video/abc",
			wantOK: true,
		},
		{
			name:   "subdomain case-insensitive host match",
			in:     "https://API.IWARA.TV/video/abc",
			want:   "https://iwara-api.l.moonchan.xyz:8443/video/abc",
			wantOK: true,
		},

		// --- non-iwara: unchanged ---
		{
			name:   "example.com unchanged",
			in:     "https://example.com/videos/abc",
			want:   "https://example.com/videos/abc",
			wantOK: false,
		},
		{
			name:   "non-iwara subdomain unchanged",
			in:     "https://api.example.tv/video/abc",
			want:   "https://api.example.tv/video/abc",
			wantOK: false,
		},
		{
			name:   "iwara.tv as suffix of another domain unchanged",
			in:     "https://notiwara.tv/videos/abc",
			want:   "https://notiwara.tv/videos/abc",
			wantOK: false,
		},
		{
			name:   "iwara.tv.cn unchanged",
			in:     "https://www.iwara.tv.cn/videos/abc",
			want:   "https://www.iwara.tv.cn/videos/abc",
			wantOK: false,
		},
		{
			name:   "iwara.tv subdomain of another domain unchanged",
			in:     "https://evil.com/iwara.tv/videos/abc",
			want:   "https://evil.com/iwara.tv/videos/abc",
			wantOK: false,
		},
		{
			name:   "empty URL unchanged",
			in:     "",
			want:   "",
			wantOK: false,
		},
		{
			name:   "not a URL unchanged",
			in:     "not a url",
			want:   "not a url",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := r.Rewrite(tt.in)
			if got != tt.want {
				t.Errorf("Rewrite(%q) = %q, want %q", tt.in, got, tt.want)
			}
			if ok != tt.wantOK {
				t.Errorf("Rewrite(%q) ok = %v, want %v", tt.in, ok, tt.wantOK)
			}
		})
	}
}

// TestRewriteHostOnly verifies that the rewrite only touches the Host
// component: Path, Query and Fragment survive byte-for-byte, and the
// scheme is normalized to https (http→https; https stays https).
//
// Discovery background: iwara's signed file URLs carry query parameters
// (?expires=...&signature=...) that must not be mangled by the rewrite —
// a signature over the original query string becomes invalid if the query
// is re-encoded. This test pins that invariant.
func TestRewriteHostOnly(t *testing.T) {
	r := NewRewriter("", "", "", "")

	// Query string with characters that url.URL.String() might re-encode.
	in := "https://www.iwara.tv/files/abc?expires=1700000000&signature=a/b+c%20d&e=f"
	got, ok := r.Rewrite(in)
	if !ok {
		t.Fatalf("expected rewrite, got ok=false")
	}
	want := "https://iwara.l.moonchan.xyz:8443/files/abc?expires=1700000000&signature=a/b+c%20d&e=f"
	if got != want {
		t.Errorf("Rewrite(%q) = %q\nwant  %q", in, got, want)
	}
}

// TestRewriteCustomConfig verifies the Rewriter honours non-default
// upstream/entry suffixes (testability of the config surface).
func TestRewriteCustomConfig(t *testing.T) {
	r := NewRewriter("example.com", "proxy.test", "eg-", "9443")

	got, ok := r.Rewrite("https://www.example.com/x")
	if !ok {
		t.Fatal("expected rewrite")
	}
	if got != "https://eg.proxy.test:9443/x" {
		t.Errorf("Rewrite = %q, want https://eg.proxy.test:9443/x", got)
	}
	got2, ok2 := r.Rewrite("https://sub.example.com/x")
	if !ok2 {
		t.Fatal("expected wildcard rewrite")
	}
	if got2 != "https://eg-sub.proxy.test:9443/x" {
		t.Errorf("Rewrite = %q, want https://eg-sub.proxy.test:9443/x", got2)
	}
}

// TestRewriterFromConfig verifies the config→Rewriter adapter.
func TestRewriterFromConfig(t *testing.T) {
	cfg := NewModuleConfig()
	r := RewriterFromConfig(&cfg)
	got, ok := r.Rewrite("https://api.iwara.tv/x")
	if got != "https://iwara-api.l.moonchan.xyz:8443/x" || !ok {
		t.Errorf("default config rewrite: got (%q, %v)", got, ok)
	}
	// Nil config must not panic.
	func() {
		got, ok := RewriterFromConfig(nil).Rewrite("https://api.iwara.tv/x")
		if got != "https://iwara-api.l.moonchan.xyz:8443/x" || !ok {
			t.Errorf("nil config rewrite: got (%q, %v)", got, ok)
		}
	}()
}

// TestIwaraHeaders verifies the header set returned by IwaraHeaders.
//
// Discovery background: the ech-proxy upstream.json iwara entry injects
// Origin/X-Site/Referer. The module also sends them explicitly so that
// direct API access (not through ech-proxy) carries the same headers.
func TestIwaraHeaders(t *testing.T) {
	h := IwaraHeaders("")
	if h["Origin"] != "https://www.iwara.tv" {
		t.Errorf("Origin = %q", h["Origin"])
	}
	if h["X-Site"] != "www.iwara.tv" {
		t.Errorf("X-Site = %q", h["X-Site"])
	}
	if h["Referer"] != "https://www.iwara.tv/" {
		t.Errorf("Referer = %q", h["Referer"])
	}

	h2 := IwaraHeaders("api.iwara.tv")
	if h2["Origin"] != "https://api.iwara.tv" {
		t.Errorf("custom Origin = %q", h2["Origin"])
	}
	if h2["X-Site"] != "api.iwara.tv" {
		t.Errorf("custom X-Site = %q", h2["X-Site"])
	}
}
