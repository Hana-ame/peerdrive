// url_test.go: table-driven tests for rule matching and URL construction.
//
// Background: the rewrite is the whole point of the module, and it must be
// byte-exact. ExHentai gallery URLs carry hex tokens and query signatures that
// do not survive a url.Parse/Rebuild round-trip, so the tests below pin the
// verbatim-preservation contract with %2F-style payloads.

package exhentai

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// fixedStore is a SnapshotStore with a hand-built document.
type fixedStore struct{ snap Snapshot }

func (f fixedStore) Load() Snapshot { return f.snap }

func storeWith(rules ...Rule) SnapshotStore {
	return fixedStore{snap: Snapshot{Config: RemoteConfig{Rules: rules}, Version: "test"}}
}

func ruleFor(hosts []string, be ...Backend) Rule {
	return Rule{SourceHosts: hosts, Backends: be}
}

func TestRouteExact(t *testing.T) {
	cases := []struct {
		name    string
		rules   []Rule
		in      string
		want    string
		wantHit bool
	}{
		{
			name: "bare gallery path",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org/g/4241178/c0eea5d273/",
			want:    "https://ex.4545810.xyz/g/4241178/c0eea5d273/",
			wantHit: true,
		},
		{
			name: "alternate host e-hentai.org",
			rules: []Rule{ruleFor([]string{"exhentai.org", "e-hentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://e-hentai.org/e9e7d4e5a8/4400000/",
			want:    "https://ex.4545810.xyz/e9e7d4e5a8/4400000/",
			wantHit: true,
		},
		{
			name: "query preserved verbatim",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org/search.php?prefix=abc%2Fdef&suffix=gh+%2520i&smth=1",
			want:    "https://ex.4545810.xyz/search.php?prefix=abc%2Fdef&suffix=gh+%2520i&smth=1",
			wantHit: true,
		},
		{
			name: "path percent encoding preserved (no %2F -> /)",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org/a%2Fb%2Fc",
			want:    "https://ex.4545810.xyz/a%2Fb%2Fc",
			wantHit: true,
		},
		{
			name: "fragment preserved",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org/g/1/2/#page-3",
			want:    "https://ex.4545810.xyz/g/1/2/#page-3",
			wantHit: true,
		},
		{
			name: "root path",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org/",
			want:    "https://ex.4545810.xyz/",
			wantHit: true,
		},
		{
			name: "bare host no path",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org",
			want:    "https://ex.4545810.xyz",
			wantHit: true,
		},
		{
			name: "query only",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://exhentai.org?a=b",
			want:    "https://ex.4545810.xyz?a=b",
			wantHit: true,
		},
		{
			name: "port stripped and replaced",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https", Port: "8443"})},
			in:      "https://exhentai.org:443/g/1/2/",
			want:    "https://ex.4545810.xyz:8443/g/1/2/",
			wantHit: true,
		},
		{
			name: "http upgraded to https",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "http://exhentai.org/g/1/2/",
			want:    "https://ex.4545810.xyz/g/1/2/",
			wantHit: true,
		},
		{
			name: "http backend keeps http",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "http"})},
			in:      "https://exhentai.org/g/1/2/",
			want:    "http://ex.4545810.xyz/g/1/2/",
			wantHit: true,
		},
		{
			name: "userinfo dropped",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://user:pass@exhentai.org/g/1/2/",
			want:    "https://ex.4545810.xyz/g/1/2/",
			wantHit: true,
		},
		{
			name: "case insensitive host match",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:      "https://EXHENTAI.ORG/g/1/2/",
			want:    "https://ex.4545810.xyz/g/1/2/",
			wantHit: true,
		},
		{
			name: "path prefix joined at seam",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "mirror.example", Scheme: "https", PathPrefix: "/eh"})},
			in:      "https://exhentai.org/g/1/2/",
			want:    "https://mirror.example/eh/g/1/2/",
			wantHit: true,
		},
		{
			name: "path prefix trailing slash collapses to single slash",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "mirror.example", Scheme: "https", PathPrefix: "/eh/"})},
			in:      "https://exhentai.org/g/1/2/",
			want:    "https://mirror.example/eh/g/1/2/",
			wantHit: true,
		},
		{
			name: "path prefix with query only",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "mirror.example", Scheme: "https", PathPrefix: "/eh"})},
			in:      "https://exhentai.org?a=b",
			want:    "https://mirror.example/eh/?a=b",
			wantHit: true,
		},
		{
			name: "path prefix with bare host",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "mirror.example", Scheme: "https", PathPrefix: "/eh"})},
			in:      "https://exhentai.org",
			want:    "https://mirror.example/eh/",
			wantHit: true,
		},
		{
			name: "wildcard subdomain match",
			rules: []Rule{ruleFor([]string{"*.hath.network"},
				Backend{Host: "hath-cdn.example", Scheme: "https"})},
			in:      "https://praogxqcch.hath.network/c2/r2gam89mb10jvm19c1/4241178-0.webp",
			want:    "https://hath-cdn.example/c2/r2gam89mb10jvm19c1/4241178-0.webp",
			wantHit: true,
		},
		{
			name: "wildcard bare host match",
			rules: []Rule{ruleFor([]string{"*.hath.network"},
				Backend{Host: "hath-cdn.example", Scheme: "https"})},
			in:      "https://hath.network/x.webp",
			want:    "https://hath-cdn.example/x.webp",
			wantHit: true,
		},
		{
			name: "unrelated host passes through",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:   "https://pbs.twimg.com/media/abc.jpg",
			want: "", wantHit: false,
		},
		{
			name: "suffix lookalike does not match",
			rules: []Rule{ruleFor([]string{"*.hath.network"},
				Backend{Host: "hath-cdn.example", Scheme: "https"})},
			in:   "https://evilhath.network/x.webp",
			want: "", wantHit: false,
		},
		{
			name: "non-http scheme passes through",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:   "ftp://exhentai.org/g/1/2/",
			want: "", wantHit: false,
		},
		{
			name: "data scheme passes through",
			rules: []Rule{ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
			in:   "data:text/html,hello",
			want: "", wantHit: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := NewRouter(storeWith(c.rules...))
			if err != nil {
				t.Fatal(err)
			}
			route, err := st.Route(c.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !c.wantHit {
				if route != nil {
					t.Fatalf("expected pass-through, got route %+v", route)
				}
				return
			}
			if route == nil {
				t.Fatalf("expected a route, got nil")
			}
			if route.Target != c.want {
				t.Fatalf("target = %q, want %q", route.Target, c.want)
			}
		})
	}
}

func TestRouteError(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantErr string
	}{
		{name: "no scheme", in: "exhentai.org/g/1/2/", wantErr: "no scheme"},
		{name: "empty string", in: "", wantErr: "no scheme"},
		{name: "bad scheme", in: "!bad://exhentai.org/", wantErr: "bad scheme"},
		{name: "empty host", in: "https:///g/1/2/", wantErr: "empty host"},
		{name: "bad host", in: "https://bad host/g/", wantErr: "bad host"},
		{name: "bad port", in: "https://exhentai.org:badport/g/", wantErr: "bad port"},
		{name: "port zero", in: "https://exhentai.org:0/g/", wantErr: "bad port"},
		{name: "port over range", in: "https://exhentai.org:65536/g/", wantErr: "bad port"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st, err := NewRouter(storeWith(ruleFor([]string{"exhentai.org"},
				Backend{Host: "ex.4545810.xyz", Scheme: "https"})))
			if err != nil {
				t.Fatal(err)
			}
			_, err = st.Route(c.in)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("got %q, want contain %q", err.Error(), c.wantErr)
			}
			if !strings.Contains(err.Error(), "exhentai:") {
				t.Fatalf("error should be prefixed: %q", err.Error())
			}
		})
	}
}

func TestRouteRemoteDisable(t *testing.T) {
	off := false
	st, err := NewRouter(fixedStore{snap: Snapshot{Config: RemoteConfig{
		Enabled: &off,
		Rules:   []Rule{ruleFor([]string{"exhentai.org"}, Backend{Host: "ex.4545810.xyz", Scheme: "https"})},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	route, err := st.Route("https://exhentai.org/g/1/2/")
	if err != nil {
		t.Fatal(err)
	}
	if route != nil {
		t.Fatalf("remote disabled must pass everything through, got %+v", route)
	}
}

func TestRouteNoRules(t *testing.T) {
	st, err := NewRouter(storeWith())
	if err != nil {
		t.Fatal(err)
	}
	route, err := st.Route("https://exhentai.org/g/1/2/")
	if err != nil {
		t.Fatal(err)
	}
	if route != nil {
		t.Fatalf("no rules must pass through, got %+v", route)
	}
}

func TestRouteHeadersAndCookie(t *testing.T) {
	st, err := NewRouter(storeWith(ruleFor([]string{"exhentai.org"},
		Backend{
			Host:      "ex.4545810.xyz",
			Scheme:    "https",
			Cookie:    "ipb_member_id=123; ipb_pass_hash=abc; igneous=def",
			Headers:   map[string]string{"User-Agent": "test-agent", "X-Custom": "v"},
			LocalAddr: "127.0.0.1:8443",
			SkipTLS:   true,
		})))
	if err != nil {
		t.Fatal(err)
	}
	route, err := st.Route("https://exhentai.org/g/1/2/")
	if err != nil || route == nil {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	if route.Cookie != "ipb_member_id=123; ipb_pass_hash=abc; igneous=def" {
		t.Fatalf("cookie = %q", route.Cookie)
	}
	if route.Headers["User-Agent"] != "test-agent" || route.Headers["X-Custom"] != "v" {
		t.Fatalf("headers = %v", route.Headers)
	}
	if route.LocalAddr != "127.0.0.1:8443" || !route.SkipTLS {
		t.Fatalf("local/tls = %q %v", route.LocalAddr, route.SkipTLS)
	}
	if route.BackendHost != "ex.4545810.xyz" {
		t.Fatalf("backend host = %q", route.BackendHost)
	}
}

func TestRuleFor(t *testing.T) {
	st, err := NewRouter(storeWith(
		ruleFor([]string{"exhentai.org"}, Backend{Host: "a"}),
		ruleFor([]string{"*.hath.network"}, Backend{Host: "b"}),
	))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		host string
		want string
	}{
		{host: "exhentai.org", want: "a"},
		{host: "praogxqcch.hath.network", want: "b"},
		{host: "pbs.twimg.com", want: ""},
	}
	for _, c := range cases {
		if c.want == "" {
			if got := st.RuleFor(c.host); got != nil {
				t.Fatalf("%q: got rule %+v, want nil", c.host, got)
			}
			continue
		}
		got := st.RuleFor(c.host)
		if got == nil || got.Backends[0].Host != c.want {
			t.Fatalf("%q: got %+v, want backend %q", c.host, got, c.want)
		}
	}
}

// TestRouteRewriteRoundTrip parses the target back with url.Parse to prove it is
// a valid URL, then checks the components land where the contract says.
func TestRouteRewriteRoundTrip(t *testing.T) {
	st, _ := NewRouter(storeWith(ruleFor([]string{"exhentai.org"},
		Backend{Host: "ex.4545810.xyz", Scheme: "https", Port: "8443"})))
	route, err := st.Route("https://exhentai.org:443/g/4241178/c0eea5d273/?p=1&x=%2F#f")
	if err != nil || route == nil {
		t.Fatalf("route=%+v err=%v", route, err)
	}
	u, err := url.Parse(route.Target)
	if err != nil {
		t.Fatalf("target %q does not parse: %v", route.Target, err)
	}
	if u.Scheme != "https" {
		t.Fatalf("scheme = %q", u.Scheme)
	}
	if u.Host != "ex.4545810.xyz:8443" {
		t.Fatalf("host = %q", u.Host)
	}
	if u.Path != "/g/4241178/c0eea5d273/" {
		t.Fatalf("path = %q", u.Path)
	}
	if u.RawQuery != "p=1&x=%2F" {
		t.Fatalf("rawquery = %q", u.RawQuery)
	}
	if u.Fragment != "f" {
		t.Fatalf("fragment = %q", u.Fragment)
	}
}

func TestNextRefreshBounds(t *testing.T) {
	store, err := NewStore(StoreOptions{
		Defaults: RemoteConfig{CacheTTL: Duration(time.Hour), RefreshJitter: Duration(5 * time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Hour - 5*time.Minute
	sawSpread := false
	for i := 0; i < 500; i++ {
		d := store.nextRefresh()
		// base = ttl - jitter = 55m; total is at most ttl = 1h.
		if d < base || d > time.Hour {
			t.Fatalf("refresh delay %v outside [%v, 1h]", d, base)
		}
		if d > base {
			sawSpread = true
		}
	}
	// The jitter draw must actually spread across the window.
	if !sawSpread {
		t.Fatal("jitter produced no spread at all")
	}
}

func TestNextRefreshNoJitter(t *testing.T) {
	store, err := NewStore(StoreOptions{
		Defaults: RemoteConfig{CacheTTL: Duration(time.Hour)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// NewStore normalises a zero jitter to the default, so install an explicit
	// zero through the store to exercise the no-jitter path directly.
	store.cfg.Config.RefreshJitter = 0
	for i := 0; i < 20; i++ {
		if d := store.nextRefresh(); d != time.Hour {
			t.Fatalf("no-jitter delay = %v, want 1h", d)
		}
	}
}

func TestNextRefreshShortTTL(t *testing.T) {
	// A 60s TTL with a 5m jitter would give a negative base; the floor must
	// kick in at 30s so the URL is never hammered.
	store, err := NewStore(StoreOptions{
		Defaults: RemoteConfig{CacheTTL: Duration(time.Hour), RefreshJitter: Duration(5 * time.Minute)},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Rebuild with a very short TTL through the normalise path.
	store.cfg.Config.CacheTTL = Duration(time.Minute)
	store.cfg.Config.RefreshJitter = Duration(5 * time.Minute)
	for i := 0; i < 200; i++ {
		if d := store.nextRefresh(); d < 30*time.Second {
			t.Fatalf("delay %v below the 30s floor", d)
		}
	}
}
