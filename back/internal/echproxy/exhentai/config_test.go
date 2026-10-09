// config_test.go: table-driven tests for the config types, defaults and
// validation. No network involved.
//
// Background: the routing table is operator-supplied JSON that arrives over the
// network at runtime, so every field must be normalised or rejected explicitly.
// These tests pin the contract so a future edit cannot silently accept a bad
// document (which would surface much later as a broken request rather than a
// loud rejection).

package exhentai

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDurationUnmarshal(t *testing.T) {
	cases := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: `"1h"`, want: time.Hour},
		{in: `"30s"`, want: 30 * time.Second},
		{in: `"1h30m"`, want: 90 * time.Minute},
		{in: `"5m"`, want: 5 * time.Minute},
		{in: `""`, want: 0},
		{in: `null`, want: 0},
		{in: `"null"`, want: 0},
		{in: `"0"`, want: 0},
		{in: `3600000000000`, want: time.Hour}, // bare nanoseconds
		{in: `"bogus"`, wantErr: true},
		{in: `"-5s"`, want: -5 * time.Second}, // parses; the caller clamps
	}
	for _, c := range cases {
		t.Run("in="+c.in, func(t *testing.T) {
			var d Duration
			err := d.UnmarshalJSON([]byte(c.in))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error for %s, got nil", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if d.Duration() != c.want {
				t.Fatalf("got %v, want %v", d.Duration(), c.want)
			}
		})
	}
}

func TestDurationMarshal(t *testing.T) {
	d := Duration(90 * time.Minute)
	b, err := d.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"1h30m0s"` {
		t.Fatalf("got %s", b)
	}
}

func TestBackendNormalize(t *testing.T) {
	cases := []struct {
		name    string
		in      Backend
		want    Backend
		wantErr string
	}{
		{
			name: "minimal fills defaults",
			in:   Backend{Host: "ex.4545810.xyz"},
			want: Backend{Host: "ex.4545810.xyz", Scheme: "https", Weight: 1},
		},
		{
			name: "empty host rejected",
			in:   Backend{Scheme: "https"},
			want: Backend{}, wantErr: "host is empty",
		},
		{
			name: "bad port rejected",
			in:   Backend{Host: "h", Port: "badport"},
			want: Backend{}, wantErr: "invalid port",
		},
		{
			name: "port 0 rejected",
			in:   Backend{Host: "h", Port: "0"},
			want: Backend{}, wantErr: "invalid port",
		},
		{
			name: "port 65536 rejected",
			in:   Backend{Host: "h", Port: "65536"},
			want: Backend{}, wantErr: "invalid port",
		},
		{
			name: "port 65535 accepted",
			in:   Backend{Host: "h", Port: "65535"},
			want: Backend{Host: "h", Port: "65535", Scheme: "https", Weight: 1},
		},
		{
			name: "unsupported scheme rejected",
			in:   Backend{Host: "h", Scheme: "ftp"},
			want: Backend{}, wantErr: "unsupported scheme",
		},
		{
			name: "scheme upper normalised to lower",
			in:   Backend{Host: "h", Scheme: "HTTP"},
			want: Backend{Host: "h", Scheme: "http", Weight: 1},
		},
		{
			name: "path_prefix must start with slash",
			in:   Backend{Host: "h", PathPrefix: "eh"},
			want: Backend{}, wantErr: "must start with",
		},
		{
			name: "path_prefix bare slash collapses to empty",
			in:   Backend{Host: "h", PathPrefix: "/"},
			want: Backend{Host: "h", Scheme: "https", Weight: 1},
		},
		{
			name: "path_prefix trailing slash kept",
			in:   Backend{Host: "h", PathPrefix: "/eh/"},
			want: Backend{Host: "h", Scheme: "https", PathPrefix: "/eh/", Weight: 1},
		},
		{
			name: "zero weight becomes 1",
			in:   Backend{Host: "h", Weight: 0},
			want: Backend{Host: "h", Scheme: "https", Weight: 1},
		},
		{
			name: "negative weight becomes 1",
			in:   Backend{Host: "h", Weight: -3},
			want: Backend{Host: "h", Scheme: "https", Weight: 1},
		},
		{
			name: "local_addr must be host:port",
			in:   Backend{Host: "h", LocalAddr: "not-a-port"},
			want: Backend{}, wantErr: "not host:port",
		},
		{
			name: "local_addr valid",
			in:   Backend{Host: "ex.l.moonchan.xyz", LocalAddr: "127.0.0.1:8443"},
			want: Backend{Host: "ex.l.moonchan.xyz", Scheme: "https", Weight: 1, LocalAddr: "127.0.0.1:8443"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := c.in
			err := b.normalize()
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got error %q, want it to contain %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(b, c.want) {
				t.Fatalf("got %+v, want %+v", b, c.want)
			}
		})
	}
}

func TestBackendNormalizeRejectsBadHost(t *testing.T) {
	for _, host := range []string{"bad host", "a/b", "ho<st", "a..b", "."} {
		b := Backend{Host: host, Scheme: "https"}
		if err := b.normalize(); err == nil {
			t.Errorf("host %q: expected an error", host)
		}
	}
	// Legal hosts must still be accepted.
	for _, host := range []string{"exhentai.org", "ex.4545810.xyz", "127.0.0.1", "[::1]"} {
		b := Backend{Host: host, Scheme: "https"}
		if err := b.normalize(); err != nil {
			t.Errorf("host %q: %v", host, err)
		}
	}
}

func TestBackendTargetHost(t *testing.T) {
	cases := []struct {
		in   Backend
		want string
	}{
		{in: Backend{Host: "ex.4545810.xyz"}, want: "ex.4545810.xyz"},
		{in: Backend{Host: "ex.4545810.xyz", Port: "8443"}, want: "ex.4545810.xyz:8443"},
		{in: Backend{Host: "::1"}, want: "::1"},
		{in: Backend{Host: "::1", Port: "8443"}, want: "[::1]:8443"},
	}
	for _, c := range cases {
		if got := c.in.targetHost(); got != c.want {
			t.Fatalf("%+v -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRuleMatches(t *testing.T) {
	cases := []struct {
		name  string
		rule  Rule
		host  string
		match bool
	}{
		{name: "exact hit", rule: Rule{SourceHosts: []string{"exhentai.org"}}, host: "exhentai.org", match: true},
		{name: "exact miss", rule: Rule{SourceHosts: []string{"exhentai.org"}}, host: "e-hentai.org", match: false},
		{name: "case insensitive", rule: Rule{SourceHosts: []string{"ExHentai.ORG"}}, host: "exhentai.org", match: true},
		{name: "whitespace trimmed", rule: Rule{SourceHosts: []string{" exhentai.org "}}, host: "exhentai.org", match: true},
		{name: "wildcard subdomain", rule: Rule{SourceHosts: []string{"*.hath.network"}}, host: "praogxqcch.hath.network", match: true},
		{name: "wildcard bare host", rule: Rule{SourceHosts: []string{"*.hath.network"}}, host: "hath.network", match: true},
		{name: "wildcard does not cross labels", rule: Rule{SourceHosts: []string{"*.hath.network"}}, host: "evilhath.network", match: false},
		{name: "wildcard deeper subdomain", rule: Rule{SourceHosts: []string{"*.hath.network"}}, host: "a.b.hath.network", match: true},
		{name: "empty host", rule: Rule{SourceHosts: []string{"*.hath.network"}}, host: "", match: false},
		{name: "empty pattern ignored", rule: Rule{SourceHosts: []string{"", "*.hath.network"}}, host: "x.hath.network", match: true},
		{name: "multiple patterns", rule: Rule{SourceHosts: []string{"exhentai.org", "e-hentai.org"}}, host: "e-hentai.org", match: true},
		{name: "no patterns", rule: Rule{}, host: "exhentai.org", match: false},
		{name: "exact does not match subdomain", rule: Rule{SourceHosts: []string{"exhentai.org"}}, host: "www.exhentai.org", match: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.rule.matches(c.host); got != c.match {
				t.Fatalf("rule %+v host %q -> %v, want %v", c.rule, c.host, got, c.match)
			}
		})
	}
}

func TestRemoteConfigNormalize(t *testing.T) {
	ptrFalse := false
	ptrTrue := true

	cases := []struct {
		name    string
		in      RemoteConfig
		want    RemoteConfig
		wantErr string
	}{
		{
			name: "empty becomes built-in-shaped defaults",
			in:   RemoteConfig{},
			want: RemoteConfig{
				Schema:        SchemaVersion,
				CacheTTL:      Duration(DefaultCacheTTL),
				RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout:  Duration(DefaultFetchTimeout),
				Rules:         nil,
			},
		},
		{
			name: "schema newer than supported rejected",
			in:   RemoteConfig{Schema: SchemaVersion + 1, Version: "x"},
			want: RemoteConfig{}, wantErr: "newer than this module understands",
		},
		{
			name: "cache ttl clamped to min",
			in:   RemoteConfig{Version: "x", CacheTTL: Duration(time.Second)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(MinCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "cache ttl clamped to max",
			in:   RemoteConfig{Version: "x", CacheTTL: Duration(72 * time.Hour)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(MaxCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "jitter equal to ttl allowed",
			in:   RemoteConfig{Version: "x", CacheTTL: Duration(time.Hour), RefreshJitter: Duration(time.Hour)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(time.Hour), RefreshJitter: Duration(time.Hour),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "jitter clamped to half of ttl when it exceeds it",
			in:   RemoteConfig{Version: "x", CacheTTL: Duration(time.Hour), RefreshJitter: Duration(2 * time.Hour)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(time.Hour), RefreshJitter: Duration(time.Hour / 2),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "zero fetch timeout becomes default",
			in:   RemoteConfig{Version: "x", FetchTimeout: Duration(0)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(DefaultCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "negative request timeout becomes zero",
			in:   RemoteConfig{Version: "x", RequestTimeout: Duration(-1)},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x",
				CacheTTL: Duration(DefaultCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout), RequestTimeout: 0,
			},
		},
		{
			name: "rule with no source hosts rejected",
			in:   RemoteConfig{Version: "x", Rules: []Rule{{Backends: []Backend{{Host: "h"}}}}},
			want: RemoteConfig{}, wantErr: "no source_hosts",
		},
		{
			name: "rule with no backends rejected",
			in:   RemoteConfig{Version: "x", Rules: []Rule{{SourceHosts: []string{"h"}}}},
			want: RemoteConfig{}, wantErr: "no backends",
		},
		{
			name: "bad backend in rule rejected with index",
			in:   RemoteConfig{Version: "x", Rules: []Rule{{SourceHosts: []string{"h"}, Backends: []Backend{{Host: ""}}}}},
			want: RemoteConfig{}, wantErr: "backend 1",
		},
		{
			name: "enabled false preserved",
			in:   RemoteConfig{Version: "x", Enabled: &ptrFalse},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x", Enabled: &ptrFalse,
				CacheTTL: Duration(DefaultCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
		{
			name: "enabled true preserved",
			in:   RemoteConfig{Version: "x", Enabled: &ptrTrue},
			want: RemoteConfig{
				Schema: SchemaVersion, Version: "x", Enabled: &ptrTrue,
				CacheTTL: Duration(DefaultCacheTTL), RefreshJitter: Duration(DefaultRefreshJitter),
				FetchTimeout: Duration(DefaultFetchTimeout),
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := c.in.normalize()
			if c.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", c.wantErr)
				}
				if !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("got %q, want contain %q", err.Error(), c.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %+v\nwant %+v", got, c.want)
			}
		})
	}
}

func TestIsEnabled(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		name    string
		in      RemoteConfig
		enabled bool
	}{
		{name: "absent means enabled", in: RemoteConfig{}, enabled: true},
		{name: "explicit true", in: RemoteConfig{Enabled: &tr}, enabled: true},
		{name: "explicit false", in: RemoteConfig{Enabled: &fa}, enabled: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.in.IsEnabled(); got != c.enabled {
				t.Fatalf("got %v, want %v", got, c.enabled)
			}
		})
	}
}

func TestDefaultRemoteConfigNormalizes(t *testing.T) {
	def := DefaultRemoteConfig()
	if def.Version == "" {
		t.Fatal("default config must carry a version tag")
	}
	got, err := def.normalize()
	if err != nil {
		t.Fatalf("built-in default must be valid: %v", err)
	}
	if len(got.Rules) == 0 {
		t.Fatal("built-in default must have rules")
	}
	for _, r := range got.Rules {
		if !r.matches(DefaultSourceHost) && !r.matches("x.hath.network") && !r.matches("ehgt.org") {
			t.Fatalf("unexpected rule: %+v", r)
		}
	}
	// The built-in must route the two ExHentai hosts at the verified mirror.
	r := got.Rules[0]
	if !r.matches(DefaultSourceHost) || !r.matches(AlternateSourceHost) {
		t.Fatalf("first rule must cover both ExHentai hosts: %+v", r)
	}
	if r.Backends[0].Host != DefaultBackendHost {
		t.Fatalf("first rule backend = %q, want %q", r.Backends[0].Host, DefaultBackendHost)
	}
}

func TestPickBackendSingle(t *testing.T) {
	b := Backend{Host: "only", Weight: 7}
	for i := 0; i < 100; i++ {
		if got := pickBackend([]Backend{b}); !reflect.DeepEqual(got, b) {
			t.Fatalf("single-backend pick changed: %+v", got)
		}
	}
}

func TestPickBackendDistributes(t *testing.T) {
	weightedRoll = func(total int) int { return -1 } // force the first bucket every time
	defer func() { weightedRoll = func(total int) int { return rand.IntN(total) } }()

	a := Backend{Host: "a", Weight: 1}
	b := Backend{Host: "b", Weight: 1}
	got := pickBackend([]Backend{a, b})
	if !reflect.DeepEqual(got, a) {
		t.Fatalf("roll=-1 should select the first backend, got %+v", got)
	}
}
