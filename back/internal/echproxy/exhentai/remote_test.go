// remote_test.go: table-driven tests for the config delivery contract.
//
// Background: the routing table arrives over the network from an operator
// document, so every failure mode has to degrade rather than fail. These tests
// pin the four failure classes — bad JSON, unreachable, wrong status, wrong
// schema — plus the caching semantics that make the contract cheap at scale.
// All of it runs against httptest servers; nothing touches the real network.

package exhentai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// docJSON marshals a RemoteConfig the way an operator would write it, keeping
// the JSON shape (duration strings, snake_case keys) under test too.
func docJSON(t *testing.T, doc RemoteConfig) []byte {
	t.Helper()
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSyncValid(t *testing.T) {
	body := []byte(`{
		"version": "v2026.10.09-1",
		"cache_ttl": "30m",
		"rules": [
			{"source_hosts": ["exhentai.org", "e-hentai.org"],
			 "backends": [{"host": "ex.4545810.xyz", "scheme": "https",
				 "headers": {"User-Agent": "test"}, "weight": 3}]}
		]
	}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchUpdated {
		t.Fatalf("result = %v", res)
	}
	snap := store.Load()
	if snap.Version != "v2026.10.09-1" {
		t.Fatalf("version = %q", snap.Version)
	}
	if snap.Source != "remote" {
		t.Fatalf("source = %q", snap.Source)
	}
	if snap.Config.CacheTTL.Duration() != 30*time.Minute {
		t.Fatalf("cache ttl = %v", snap.Config.CacheTTL.Duration())
	}
	if len(snap.Config.Rules) != 1 {
		t.Fatalf("rules = %d", len(snap.Config.Rules))
	}
	if snap.Config.Rules[0].Backends[0].Weight != 3 {
		t.Fatalf("weight = %d", snap.Config.Rules[0].Backends[0].Weight)
	}
	// Cache window must follow the document, and the snapshot must be fresh.
	if snap.Expires.Before(snap.At.Add(30 * time.Minute)) {
		t.Fatalf("expires %v earlier than at+%v", snap.Expires, snap.At)
	}
	fetches, unchanged, failed := store.Stats()
	if fetches != 1 || unchanged != 0 || failed != 0 {
		t.Fatalf("stats = %d/%d/%d", fetches, unchanged, failed)
	}
}

func TestSyncBadJSONKeepsPrevious(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		switch n {
		case 1:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"good","rules":[]}`))
		case 2:
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{not json`))
		default:
			t.Errorf("unexpected request %d", n)
		}
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchUpdated {
		t.Fatalf("first = %v", res)
	}
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("second = %v", res)
	}
	// The good snapshot must still be in force.
	if v := store.Version(); v != "good" {
		t.Fatalf("version after bad fetch = %q", v)
	}
	if src := store.Load().Source; src != "remote" {
		t.Fatalf("source after bad fetch = %q", src)
	}
	if _, _, failed := store.Stats(); failed != 1 {
		t.Fatalf("failed = %d", failed)
	}
}

func TestSyncFirstFetchFailsFallsBackToBuiltIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	snap := store.Load()
	if snap.Source != "builtin" {
		t.Fatalf("source = %q, want builtin", snap.Source)
	}
	if snap.Version == "" {
		t.Fatal("version must not be empty")
	}
	if len(snap.Config.Rules) == 0 {
		t.Fatal("built-in fallback must carry rules")
	}
}

func TestSyncStatusCodes(t *testing.T) {
	type tc struct {
		name    string
		status  int
		wantRes FetchResult
		wantSrc string
	}
	cases := []tc{
		{name: "404 falls back to builtin", status: http.StatusNotFound, wantRes: FetchFailed, wantSrc: "builtin"},
		{name: "410 falls back to builtin", status: http.StatusGone, wantRes: FetchFailed, wantSrc: "builtin"},
		{name: "403 keeps last known good", status: http.StatusForbidden, wantRes: FetchFailed, wantSrc: "remote"},
		{name: "500 keeps last known good", status: http.StatusInternalServerError, wantRes: FetchFailed, wantSrc: "remote"},
		{name: "503 keeps last known good", status: http.StatusServiceUnavailable, wantRes: FetchFailed, wantSrc: "remote"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n++
				if n == 1 {
					_, _ = w.Write([]byte(`{"version":"v1","rules":[]}`))
					return
				}
				w.WriteHeader(c.status)
			}))
			defer srv.Close()

			store, err := NewStore(StoreOptions{URL: srv.URL,
				AllowInsecureHTTP: true, Client: srv.Client()})
			if err != nil {
				t.Fatal(err)
			}
			store.Sync(context.Background())
			if res := store.Sync(context.Background()); res != c.wantRes {
				t.Fatalf("result = %v, want %v", res, c.wantRes)
			}
			if got := store.Load().Source; got != c.wantSrc {
				t.Fatalf("source = %q, want %q", got, c.wantSrc)
			}
		})
	}
}

func TestSync404AfterGoodFallsBack(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			_, _ = w.Write([]byte(`{"version":"v1","rules":[{"source_hosts":["exhentai.org"],"backends":[{"host":"ex.4545810.xyz"}]}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	store.Sync(context.Background())
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	if v := store.Version(); v != "builtin" {
		t.Fatalf("version = %q, want builtin", v)
	}
	// The built-in table is a valid table, so the module still routes.
	if len(store.Load().Config.Rules) == 0 {
		t.Fatal("builtin fallback must have rules")
	}
}

func TestSyncNotModified(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		if n == 1 {
			w.Header().Set("ETag", `"v1"`)
			_, _ = w.Write([]byte(`{"version":"v1","rules":[]}`))
			return
		}
		if got := r.Header.Get("If-None-Match"); got != `"v1"` {
			t.Fatalf("second request If-None-Match = %q", got)
		}
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchUpdated {
		t.Fatalf("first = %v", res)
	}
	before := store.Load().Config.Version
	if res := store.Sync(context.Background()); res != FetchUnchanged {
		t.Fatalf("second = %v", res)
	}
	if got := store.Version(); got != before {
		t.Fatalf("version changed on 304: %q -> %q", before, got)
	}
	if _, unchanged, failed := store.Stats(); unchanged != 1 || failed != 0 {
		t.Fatalf("unchanged=%d failed=%d", unchanged, failed)
	}
}

func TestSyncSchemaTooNew(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"schema":99,"version":"future","rules":[]}`))
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	if store.Load().Source != "builtin" {
		t.Fatalf("source = %q", store.Load().Source)
	}
}

func TestSyncOverSizedDocument(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes_Repeat('x', maxResponseBytes+1))
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	if store.Load().Source != "builtin" {
		t.Fatalf("source = %q", store.Load().Source)
	}
}

// bytes_Repeat builds a string of n bytes without pulling in strings.Repeat
// for a single test helper.
func bytes_Repeat(c byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return b
}

func TestSyncTimeout(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-time.After(3 * time.Second)
	}))
	defer srv.Close()

	client := &http.Client{Timeout: 30 * time.Millisecond}
	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: client})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	if store.Load().Source != "builtin" {
		t.Fatalf("source = %q", store.Load().Source)
	}
}

func TestSyncCancelledContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v","rules":[]}`))
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store, err := NewStore(StoreOptions{URL: srv.URL,
		AllowInsecureHTTP: true, Client: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(ctx); res != FetchFailed {
		t.Fatalf("result = %v", res)
	}
	if store.Load().Source != "builtin" {
		t.Fatalf("source = %q", store.Load().Source)
	}
}

func TestNewStoreRefusesHTTP(t *testing.T) {
	_, err := NewStore(StoreOptions{URL: "http://example.com/config.json"})
	if err == nil {
		t.Fatal("expected http:// to be refused")
	}
	if !strings.Contains(err.Error(), "http://") {
		t.Fatalf("error = %q", err)
	}
	// Explicitly allowing it must work.
	if _, err := NewStore(StoreOptions{URL: "http://example.com/config.json", AllowInsecureHTTP: true}); err != nil {
		t.Fatalf("AllowInsecureHTTP did not take: %v", err)
	}
}

func TestNewStoreInvalidURL(t *testing.T) {
	_, err := NewStore(StoreOptions{URL: "://noscheme"})
	if err == nil {
		t.Fatal("expected an invalid-URL error")
	}
}

func TestSyncNoURL(t *testing.T) {
	store, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchSkipped {
		t.Fatalf("result = %v", res)
	}
	if store.Load().Source != "builtin" {
		t.Fatalf("source = %q", store.Load().Source)
	}
	fetches, _, _ := store.Stats()
	if fetches != 1 {
		t.Fatalf("fetches = %d", fetches)
	}
	// Run must return immediately with no URL.
	done := make(chan struct{})
	go func() { store.Run(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return for a URL-less store")
	}
}

func TestSyncAuthHeadersAndHeadersSent(t *testing.T) {
	var gotAuth, gotAccept, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		gotUA = r.Header.Get("User-Agent")
		_, _ = w.Write([]byte(`{"version":"v","rules":[]}`))
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{
		URL:               srv.URL,
		AllowInsecureHTTP: true,
		Client:            srv.Client(),
		AuthHeaders:       map[string]string{"Authorization": "Bearer tok", "X-Extra": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := store.Sync(context.Background()); res != FetchUpdated {
		t.Fatalf("result = %v", res)
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	if gotAccept != "application/json" {
		t.Fatalf("accept = %q", gotAccept)
	}
	if !strings.HasPrefix(gotUA, "peerdrive-exhentai-config/") {
		t.Fatalf("user agent = %q", gotUA)
	}
}

func TestRunRefreshesInBackground(t *testing.T) {
	n := 0
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		version := "v1"
		if n > 1 {
			version = "v2"
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{"version":"` + version + `","cache_ttl":"100ms","refresh_jitter":"0s","rules":[]}`))
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{
		URL:               srv.URL,
		AllowInsecureHTTP: true,
		Client:            srv.Client(),
		// NewStore would clamp a 100ms TTL up to the five-minute fleet minimum,
		// so this store gets its own tighter window. Keep it a Store field
		// rather than rewriting MinCacheTTL, which would race with the refresh
		// goroutine.
		MinCacheTTL: 50 * time.Millisecond,
		Defaults:    RemoteConfig{CacheTTL: Duration(100 * time.Millisecond), RefreshJitter: 0},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The interval floor is the same story: a per-store value the test owns.
	store.mu.Lock()
	store.minInterval = 10 * time.Millisecond
	store.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go store.Run(ctx)

	// Poll until two fetches have landed.
	// 发现背景：macOS runner 在启动 httptest 和 goroutine 时调度延迟较大，
	// 原 30ms TTL 与 3s 超时容易在冷启动时偶发超时。放宽 TTL 至 100ms 并给予 6s 等待余量。
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := n
		mu.Unlock()
		if got >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	mu.Lock()
	after := n
	mu.Unlock()
	if after < 2 {
		t.Fatalf("expected at least 2 fetches, got %d", after)
	}
	// The handler counts a request before Sync has installed the snapshot it
	// returned, so poll for the version rather than asserting on the count.
	// Do NOT cancel the context here: the Run goroutine needs the context
	// alive to complete its second Sync and install the v2 snapshot. If we
	// cancel before the Sync finishes, the HTTP request fails with context
	// cancellation and the version stays at v1 — a flake on slow runners.
	deadline = time.Now().Add(2 * time.Second)
	for store.Version() != "v2" && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel() // Now safe to cancel: version is confirmed v2.
	if v := store.Version(); v != "v2" {
		t.Fatalf("version = %q, want v2 (hot reload)", v)
	}
	if src := store.Load().Source; src != "remote" {
		t.Fatalf("source = %q", src)
	}
}

func TestSyncCacheTTLClamped(t *testing.T) {
	cases := []struct {
		name string
		ttl  string
		want time.Duration
	}{
		{name: "below min clamps up", ttl: "1s", want: MinCacheTTL},
		{name: "above max clamps down", ttl: "72h", want: MaxCacheTTL},
		{name: "in range preserved", ttl: "30m", want: 30 * time.Minute},
		{name: "absent takes default", ttl: "", want: DefaultCacheTTL},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body string
			if c.ttl == "" {
				body = `{"version":"v","rules":[]}`
			} else {
				body = `{"version":"v","cache_ttl":"` + c.ttl + `","rules":[]}`
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			store, err := NewStore(StoreOptions{URL: srv.URL,
				AllowInsecureHTTP: true, Client: srv.Client()})
			if err != nil {
				t.Fatal(err)
			}
			store.Sync(context.Background())
			if got := store.Load().Config.CacheTTL.Duration(); got != c.want {
				t.Fatalf("cache ttl = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSyncVersionChangeIsLogged(t *testing.T) {
	var logs []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"version":"v2","rules":[]}`))
	}))
	defer srv.Close()

	store, err := NewStore(StoreOptions{
		URL:               srv.URL,
		AllowInsecureHTTP: true,
		Client:            srv.Client(),
		Logf: func(format string, args ...interface{}) {
			mu.Lock()
			defer mu.Unlock()
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	store.Sync(context.Background())
	mu.Lock()
	defer mu.Unlock()
	if len(logs) == 0 {
		t.Fatal("no logs emitted")
	}
	if !strings.Contains(logs[0], "v2") {
		t.Fatalf("log does not carry the new version: %q", logs[0])
	}
}

func TestSnapshotNeverZero(t *testing.T) {
	store, err := NewStore(StoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	snap := store.Load()
	if snap.Version == "" || snap.Source == "" {
		t.Fatalf("snapshot must be fully populated: %+v", snap)
	}
	if snap.At.IsZero() {
		t.Fatal("snapshot At is zero")
	}
	if snap.Expires.IsZero() {
		t.Fatal("snapshot Expires is zero")
	}
}

func TestCustomNow(t *testing.T) {
	start := time.Date(2026, 10, 9, 0, 0, 0, 0, time.UTC)
	store, err := NewStore(StoreOptions{Now: func() time.Time { return start }})
	if err != nil {
		t.Fatal(err)
	}
	if !store.Load().At.Equal(start) {
		t.Fatalf("at = %v, want %v", store.Load().At, start)
	}
	if !store.Load().Expires.Equal(start.Add(DefaultCacheTTL)) {
		t.Fatalf("expires = %v", store.Load().Expires)
	}
}
