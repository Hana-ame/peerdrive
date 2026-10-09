// module_test.go: tests for the public Module API and the RoundTripper seam.
//
// Two contracts the rest of peerdrive relies on:
//
//  1. Disabled is a no-op. A node that never turns the module on must behave
//     exactly as one without it: nothing is constructed, Start is never called,
//     and Client() stays nil. The remote "enabled": false document gives the
//     same effect at runtime, without a restart.
//  2. Enabled is additive. Only a URL that matches a rule is rewritten; every
//     other request is handed to the base transport untouched — same headers,
//     no injected cookie.
//
// Routed behaviour is asserted against real httptest backends so the module is
// tested through the same code path serverapp uses. The base-transport
// assertions are made at the RoundTripper seam, because a base RoundTripper can
// only be injected there — NewClient builds its own transport on purpose, the
// same way twimg does.

package exhentai

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeTransport records every request it is handed and answers immediately, so
// a test can prove that non-matching traffic reached the base layer untouched.
type fakeTransport struct {
	mu   sync.Mutex
	seen []fakeSeen
}

type fakeSeen struct {
	host   string
	path   string
	cookie string
	ua     string
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// http.Transport fills Host from URL.Host when the caller left it empty,
	// which is what the RoundTripper does on purpose so the rewritten authority
	// reaches the wire. Mirror that here or the field is always empty.
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	f.mu.Lock()
	f.seen = append(f.seen, fakeSeen{
		host:   host,
		path:   req.URL.Path,
		cookie: req.Header.Get("Cookie"),
		ua:     req.Header.Get("User-Agent"),
	})
	f.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}, nil
}

func (f *fakeTransport) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.seen)
}

func (f *fakeTransport) at(i int) fakeSeen {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seen[i]
}

func TestModuleNotStarted(t *testing.T) {
	m, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Client() != nil {
		t.Fatal("Client() must be nil before Start: the module is optional")
	}
	if m.Alive() {
		t.Fatal("Alive() must be false before Start")
	}
	// Store and Router are built eagerly — cheap, and it makes Snapshot()
	// safe to call at any time, including from a status endpoint.
	if m.Store() == nil || m.Router() == nil {
		t.Fatal("Store/Router must be non-nil after New")
	}
	if src := m.Snapshot().Source; src != "builtin" {
		t.Fatalf("source = %q", src)
	}
	// Stopping before starting must be a no-op, not an error.
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
}

func TestModuleStartStop(t *testing.T) {
	m, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if m.Client() == nil {
		t.Fatal("Client() must be non-nil after Start")
	}
	if !m.Alive() {
		t.Fatal("Alive() must be true after Start")
	}
	// Starting twice must be idempotent so a config reload cannot double the
	// refresh goroutine.
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if m.Client() != nil {
		t.Fatal("Client() must be nil after Stop")
	}
	if m.Alive() {
		t.Fatal("Alive() must be false after Stop")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
}

// TestRoundTripPassThrough is the "enabled is additive" half of the contract:
// a non-matching URL must arrive at the base transport with every header as the
// caller sent it, and no cookie added.
func TestRoundTripPassThrough(t *testing.T) {
	base := &fakeTransport{}
	rt, err := NewRoundTripper(mustRouter(t), base)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://pbs.twimg.com/media/xyz.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "caller=token")
	req.Header.Set("Range", "bytes=100-199")
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}
	if n := base.count(); n != 1 {
		t.Fatalf("base transport saw %d requests, want 1", n)
	}
	got := base.at(0)
	if got.host != "pbs.twimg.com" || got.path != "/media/xyz.jpg" {
		t.Fatalf("base saw %q at %q", got.path, got.host)
	}
	if got.cookie != "caller=token" {
		t.Fatalf("caller cookie was altered: %q", got.cookie)
	}
}

// TestRoundTripRouted checks that a matching URL is rewritten, and that the
// caller's own headers still win over the operator document's.
func TestRoundTripRouted(t *testing.T) {
	base := &fakeTransport{}
	router, err := NewRouter(fixedStore{snap: Snapshot{Config: RemoteConfig{Rules: []Rule{{
		SourceHosts: []string{DefaultSourceHost},
		Backends: []Backend{{
			Host:    "ex.4545810.xyz",
			Scheme:  "https",
			Cookie:  "ipb_member_id=1",
			Headers: map[string]string{"User-Agent": "from-config"},
		}},
	}}}}})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRoundTripper(router, base)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodGet, "https://exhentai.org/g/4241178/c0eea5d273/?p=1&x=a%2Fb", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "from-caller")
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatal(err)
	}

	if n := base.count(); n != 1 {
		t.Fatalf("base transport saw %d requests", n)
	}
	got := base.at(0)
	if got.host != "ex.4545810.xyz" {
		t.Fatalf("host = %q, want the rewritten authority", got.host)
	}
	if got.path != "/g/4241178/c0eea5d273/" {
		t.Fatalf("path = %q", got.path)
	}
	if got.ua != "from-caller" {
		t.Fatalf("user agent = %q, want the caller's value to win", got.ua)
	}
	if got.cookie != "ipb_member_id=1" {
		t.Fatalf("cookie = %q, want the config's value appended", got.cookie)
	}
}

// TestModuleEndToEnd drives the module through the same path serverapp uses:
// Config → New → Start → Client → Do, with two real httptest servers playing
// the backend and an unrelated origin.
func TestModuleEndToEnd(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotRawQuery, gotHost, gotCookie, gotUA, gotRange string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		gotPath = r.URL.Path
		gotRawQuery = r.URL.RawQuery
		gotHost = r.Host
		gotCookie = r.Header.Get("Cookie")
		gotUA = r.Header.Get("User-Agent")
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer backend.Close()

	// A second server standing in for "something else entirely" on the same
	// machine: reaching it proves the request was passed through rather than
	// routed, without any real network access.
	unrelated := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer unrelated.Close()
	unrelatedBase := "http://" + unrelated.Listener.Addr().String()

	m, err := New(Config{
		Defaults: RemoteConfig{
			Version: "test-1",
			Rules: []Rule{{
				SourceHosts: []string{DefaultSourceHost, AlternateSourceHost},
				Backends: []Backend{{
					Host:    backend.Listener.Addr().String(),
					Scheme:  "http",
					Cookie:  "ipb_member_id=42; ipb_pass_hash=deadbeef; igneous=cafebabe",
					Headers: map[string]string{"User-Agent": "peerdrive-test/1.0"},
				}},
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	client := m.Client()
	if client == nil {
		t.Fatal("client is nil")
	}

	// 1. A gallery URL is rewritten to the backend; headers and cookie applied;
	//    the caller's Range survives.
	req, err := http.NewRequest(http.MethodGet, "https://exhentai.org/g/4241178/c0eea5d273/?p=1&prefix=a%2Fb", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Range", "bytes=0-99")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("gallery request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	mu.Lock()
	if gotPath != "/g/4241178/c0eea5d273/" {
		t.Errorf("backend path = %q", gotPath)
	}
	if gotRawQuery != "p=1&prefix=a%2Fb" {
		t.Errorf("backend query = %q", gotRawQuery)
	}
	if gotHost != backend.Listener.Addr().String() {
		t.Errorf("backend host = %q", gotHost)
	}
	if gotCookie != "ipb_member_id=42; ipb_pass_hash=deadbeef; igneous=cafebabe" {
		t.Errorf("cookie = %q", gotCookie)
	}
	if gotUA != "peerdrive-test/1.0" {
		t.Errorf("user agent = %q", gotUA)
	}
	if gotRange != "bytes=0-99" {
		t.Errorf("caller Range must survive the rewrite, got %q", gotRange)
	}
	mu.Unlock()

	// 2. The alternate host routes to the same backend.
	resp2, err := client.Get("https://e-hentai.org/e9e7d4e5a8/4400000/")
	if err != nil {
		t.Fatalf("alternate host request: %v", err)
	}
	resp2.Body.Close()
	mu.Lock()
	if gotPath != "/e9e7d4e5a8/4400000/" {
		t.Errorf("alternate host path = %q", gotPath)
	}
	mu.Unlock()

	// 3. An unrelated URL is passed through untouched: it reaches the local
	//    stand-in, never the backend.
	resp3, err := client.Get(unrelatedBase + "/media/xyz.jpg")
	if err != nil {
		t.Fatalf("pass-through request: %v", err)
	}
	resp3.Body.Close()
	mu.Lock()
	if gotPath == "/media/xyz.jpg" {
		t.Error("the backend received a request meant to pass through")
	}
	mu.Unlock()
}

// TestModuleCallerCookieIsAppended proves the caller's Cookie is kept alongside
// the backend's. Replacing it would break a logged-in user's own session
// cookies — exactly the case ech-proxy's upstream.json handles for e-hentai.
func TestModuleCallerCookieIsAppended(t *testing.T) {
	var gotCookie string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
	}))
	defer backend.Close()

	m, err := New(Config{
		Defaults: RemoteConfig{Rules: []Rule{{
			SourceHosts: []string{DefaultSourceHost},
			Backends:    []Backend{{Host: backend.Listener.Addr().String(), Scheme: "http", Cookie: "ipb_member_id=1"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	req, _ := http.NewRequest(http.MethodGet, "https://exhentai.org/", nil)
	req.Header.Set("Cookie", "caller=token")
	resp, err := m.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotCookie != "caller=token; ipb_member_id=1" {
		t.Fatalf("cookie = %q, want both parts", gotCookie)
	}
}

// TestModuleCallerHeaderWins checks set-default semantics: a header the caller
// set is not overwritten by the operator document.
func TestModuleCallerHeaderWins(t *testing.T) {
	var gotUA string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
	}))
	defer backend.Close()

	m, err := New(Config{
		Defaults: RemoteConfig{Rules: []Rule{{
			SourceHosts: []string{DefaultSourceHost},
			Backends: []Backend{{
				Host:    backend.Listener.Addr().String(),
				Scheme:  "http",
				Headers: map[string]string{"User-Agent": "from-config"},
			}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	req, _ := http.NewRequest(http.MethodGet, "https://exhentai.org/", nil)
	req.Header.Set("User-Agent", "from-caller")
	resp, err := m.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if gotUA != "from-caller" {
		t.Fatalf("user agent = %q, want the caller's value", gotUA)
	}
}

// TestModuleRemoteDisableIsAKillSwitch proves an operator can switch the module
// off by pushing a document, with no restart. The backend must not be contacted
// at all while the document says enabled=false.
func TestModuleRemoteDisableIsAKillSwitch(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("backend must not be contacted while the document is disabled")
	}))
	defer backend.Close()

	off := false
	router, err := NewRouter(fixedStore{snap: Snapshot{Config: RemoteConfig{
		Enabled: &off,
		Rules: []Rule{{
			SourceHosts: []string{DefaultSourceHost},
			Backends:    []Backend{{Host: backend.Listener.Addr().String(), Scheme: "http"}},
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := NewRoundTripper(router, &fakeTransport{})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://exhentai.org/g/1/2/", nil)
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("a disabled module must pass the request through: %v", err)
	}
}

// TestModuleHotReload swaps the live routing table between two requests without
// restarting the module. This is the "push a document and it takes effect"
// property the delivery URL exists to provide, so it is tested as behaviour,
// not as plumbing.
func TestModuleHotReload(t *testing.T) {
	paths := make(chan string, 2)
	var mu sync.Mutex
	newBackend := func() *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			paths <- r.URL.Path
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	b1, b2 := newBackend(), newBackend()

	m, err := New(Config{
		Defaults: RemoteConfig{Rules: []Rule{{
			SourceHosts: []string{DefaultSourceHost},
			Backends:    []Backend{{Host: b1.Listener.Addr().String(), Scheme: "http"}},
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	if _, err := m.Client().Get("https://exhentai.org/g/1/2/"); err != nil {
		t.Fatal(err)
	}

	// Publish a new document pointing at the second backend.
	mu.Lock()
	m.store.cfg = Snapshot{
		Config: RemoteConfig{Rules: []Rule{{
			SourceHosts: []string{DefaultSourceHost},
			Backends:    []Backend{{Host: b2.Listener.Addr().String(), Scheme: "http"}},
		}}},
		Version: "v2",
		Source:  "test",
	}
	mu.Unlock()

	if _, err := m.Client().Get("https://exhentai.org/g/1/2/"); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	select {
	case p := <-paths:
		if p != "/g/1/2/" {
			t.Fatalf("first backend path = %q", p)
		}
	case <-time.After(2 * time.Second):
		mu.Unlock()
		t.Fatal("first backend never received a request")
	}
	select {
	case p := <-paths:
		if p != "/g/1/2/" {
			t.Fatalf("second backend path = %q", p)
		}
	case <-time.After(2 * time.Second):
		mu.Unlock()
		t.Fatal("second backend never received a request: hot reload failed")
	}
	mu.Unlock()
}

// TestModuleStartSurvivesFetchFailure: the module must come up even when the
// config URL is unreachable, degrading to the built-in table. An operator outage
// must never take the node down with it.
func TestModuleStartSurvivesFetchFailure(t *testing.T) {
	var logs []string
	var mu sync.Mutex
	m, err := New(Config{
		ConfigURL:         "http://127.0.0.1:1/config.json",
		AllowInsecureHTTP: true,
		HTTPBase:          &http.Client{},
		ConfigHTTP:        &http.Client{Timeout: 200 * time.Millisecond},
		Logf: func(format string, args ...interface{}) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, format)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("Start must not fail on a bad config URL: %v", err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	if m.Client() == nil {
		t.Fatal("client must still be built")
	}
	if src := m.Snapshot().Source; src != "builtin" {
		t.Fatalf("source = %q, want builtin", src)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("expected a failure log line")
	}
}

// TestModuleInvalidDefaults rejects a bad document at construction time.
func TestModuleInvalidDefaults(t *testing.T) {
	_, err := New(Config{Defaults: RemoteConfig{
		Version: "bad",
		Rules:   []Rule{{SourceHosts: []string{"h"}, Backends: []Backend{{Host: ""}}}},
	}})
	if err == nil {
		t.Fatal("expected a validation error")
	}
}

// TestModuleClientShape mirrors how serverapp consumes the client: it must be a
// real *http.Client that can be handed to source.NewURLSource, with the base
// client's timeout inherited rather than reset.
func TestModuleClientShape(t *testing.T) {
	m, err := New(Config{HTTPBase: &http.Client{Timeout: 30 * time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()

	c := m.Client()
	if c.Timeout != 30*time.Second {
		t.Fatalf("base client Timeout must be inherited, got %v", c.Timeout)
	}
	if c.Transport == nil {
		t.Fatal("Transport must be set")
	}
}

// TestApplyHeaders exercises the header injection helper directly, including the
// empty-cookie and empty-route edge cases.
func TestApplyHeaders(t *testing.T) {
	cases := []struct {
		name       string
		preCookie  string
		preHeaders map[string]string
		route      *Route
		wantCookie string
		wantUA     string
	}{
		{
			name:       "injects everything",
			route:      &Route{Cookie: "ipb_member_id=1", Headers: map[string]string{"User-Agent": "cfg"}},
			wantCookie: "ipb_member_id=1",
			wantUA:     "cfg",
		},
		{
			name:       "appends to an existing cookie",
			preCookie:  "caller=1",
			route:      &Route{Cookie: "ipb_member_id=1"},
			wantCookie: "caller=1; ipb_member_id=1",
		},
		{
			name:       "an empty backend cookie leaves the caller alone",
			preCookie:  "caller=1",
			route:      &Route{},
			wantCookie: "caller=1",
		},
		{
			name:       "the caller's header wins",
			preHeaders: map[string]string{"User-Agent": "caller"},
			route:      &Route{Headers: map[string]string{"User-Agent": "cfg"}},
			wantUA:     "caller",
		},
		{
			name:       "nil route headers",
			route:      &Route{Cookie: "c=1"},
			wantCookie: "c=1",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, "https://example.com/", nil)
			if err != nil {
				t.Fatal(err)
			}
			if c.preCookie != "" {
				req.Header.Set("Cookie", c.preCookie)
			}
			for k, v := range c.preHeaders {
				req.Header.Set(k, v)
			}
			applyHeaders(req, c.route)
			if got := req.Header.Get("Cookie"); got != c.wantCookie {
				t.Fatalf("cookie = %q, want %q", got, c.wantCookie)
			}
			if got := req.Header.Get("User-Agent"); got != c.wantUA {
				t.Fatalf("user agent = %q, want %q", got, c.wantUA)
			}
		})
	}
}

// TestNewRoundTripperValidation pins the constructor guards.
func TestNewRoundTripperValidation(t *testing.T) {
	if _, err := NewRoundTripper(nil, nil); err == nil {
		t.Fatal("nil router must be rejected")
	}
	rt, err := NewRoundTripper(mustRouter(t), &fakeTransport{})
	if err != nil {
		t.Fatal(err)
	}
	if rt == nil {
		t.Fatal("nil RoundTripper")
	}
	if _, err := rt.RoundTrip(nil); err == nil {
		t.Fatal("a nil request must error")
	}
	if _, err := rt.RoundTrip(&http.Request{}); err == nil {
		t.Fatal("a request without a URL must error")
	}
}

func TestNewClientValidation(t *testing.T) {
	if _, err := NewClient(nil, nil); err == nil {
		t.Fatal("nil router must be rejected")
	}
	c, err := NewClient(mustRouter(t), &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Transport == nil {
		t.Fatal("Transport must be set")
	}
}

func mustRouter(t *testing.T) *Router {
	t.Helper()
	r, err := NewRouter(storeWith(ruleFor([]string{DefaultSourceHost},
		Backend{Host: "ex.4545810.xyz", Scheme: "https"})))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// TestValidHost checks the host validator used both for operator documents and
// for the source URL, so a malformed authority can never reach a backend.
func TestValidHost(t *testing.T) {
	cases := []struct {
		host  string
		valid bool
	}{
		{host: "exhentai.org", valid: true},
		{host: "ex.4545810.xyz", valid: true},
		{host: "localhost", valid: true},
		{host: "127.0.0.1", valid: true},
		{host: "[::1]", valid: true},
		{host: "1.2.3.4.5", valid: true}, // syntactically a legal hostname
		{host: "", valid: false},
		{host: "bad host", valid: false},
		{host: "host/tab", valid: false},
		{host: "a/b", valid: false},
		{host: "ho<st", valid: false},
		{host: `ho"st`, valid: false},
		{host: `ho\st`, valid: false},
		{host: "[::1", valid: false},
		{host: ".leading-dot", valid: false},
		{host: "a..b", valid: false},
	}
	for _, c := range cases {
		if got := validHost(c.host); got != c.valid {
			t.Errorf("validHost(%q) = %v, want %v", c.host, got, c.valid)
		}
	}
}

// TestParseHTTPDate pins the HTTP-date parsing used for Last-Modified caching.
func TestParseHTTPDate(t *testing.T) {
	cases := []struct {
		in   string
		want time.Time
	}{
		{in: "", want: time.Time{}},
		{in: "not a date", want: time.Time{}},
		{in: "Fri, 09 Oct 2026 02:21:11 GMT", want: time.Date(2026, 10, 9, 2, 21, 11, 0, time.UTC)},
	}
	for _, c := range cases {
		if got := parseHTTPDate(c.in); !got.Equal(c.want) {
			t.Errorf("parseHTTPDate(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestRoundTripBaseError checks that a base-transport failure propagates
// unmodified to the caller.
func TestRoundTripBaseError(t *testing.T) {
	rt, err := NewRoundTripper(mustRouter(t), errTransport{err: errors.New("boom")})
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "https://exhentai.org/g/1/2/", nil)
	if _, err := rt.RoundTrip(req); err == nil {
		t.Fatal("expected the base transport error to propagate")
	}
}

type errTransport struct{ err error }

func (e errTransport) RoundTrip(*http.Request) (*http.Response, error) { return nil, e.err }
