package echproxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// mockIwaraServer runs an in-process mock of the iwara API surface that
// the IwaraClient talks to. It records the headers of every request so
// tests can assert on cookie injection and the X-Version signing.
type mockIwaraServer struct {
	*httptest.Server
	t              *testing.T
	videoID        string
	fileID         string
	expires        string
	downloadSrc    string
	expectedCookie string
	sawCookie      atomic.Bool
	sawXSite       atomic.Bool
	sawXVersion    atomic.Bool
	lastXVersion   string
	// Optional metadata for the VideoMeta tests (meta.go reads author / cover /
	// duration / views out of the same /video/{id} payload). Left empty by the
	// download tests, which do not care about them.
	author   string
	authorID string
	cover    string
	duration float64
	views    float64
}

func newMockIwaraServer(t *testing.T) *mockIwaraServer {
	m := &mockIwaraServer{t: t}
	m.downloadSrc = "//v-f007-abc.v.ihstatic.com/video/abc.mp4?expire=1700000000&token=tok123"
	m.videoID = "abc123"
	m.fileID = "file789"
	m.expires = "1700000000"
	m.expectedCookie = "iwara_session=s3cret; theme=dark"

	mux := http.NewServeMux()
	mux.HandleFunc("/video/"+m.videoID, m.handleVideoInfo)
	mux.HandleFunc("/files/"+m.fileID+"/resolution", m.handleResolution)
	m.Server = httptest.NewTLSServer(mux)
	t.Cleanup(m.Server.Close)
	return m
}

func (m *mockIwaraServer) handleVideoInfo(w http.ResponseWriter, r *http.Request) {
	m.assertCommonHeaders(r)
	w.Header().Set("Content-Type", "application/json")
	payload := map[string]any{
		"id":      m.videoID,
		"title":   "Mock Video",
		"file":    map[string]string{"id": m.fileID, "name": "abc.mp4"},
		"fileUrl": m.Server.URL + "/files/" + m.fileID + "/resolution?expires=" + m.expires + "&sig=x",
	}
	if m.author != "" {
		payload["author"] = m.author
		if m.authorID != "" {
			payload["authorId"] = m.authorID
		}
	}
	if m.cover != "" {
		// coverUrl (not cover) is the spelling the metadata decoder probes first.
		payload["coverUrl"] = m.cover
	}
	if m.duration > 0 {
		payload["duration"] = m.duration
	}
	if m.views > 0 {
		payload["views"] = m.views
	}
	json.NewEncoder(w).Encode(payload)
}

func (m *mockIwaraServer) handleResolution(w http.ResponseWriter, r *http.Request) {
	m.assertCommonHeaders(r)

	if got := r.URL.Query().Get("expires"); got != m.expires {
		m.t.Errorf("resolution expires = %q, want %q", got, m.expires)
	}
	xv := r.Header.Get("X-Version")
	if xv == "" {
		m.t.Error("resolution request missing X-Version header")
	} else {
		m.sawXVersion.Store(true)
		m.lastXVersion = xv
	}

	want := SHA1Hex(m.fileID + "_" + m.expires + "_" + xVersionSecret)
	if xv != want {
		m.t.Errorf("X-Version = %q, want SHA1(%q)=%q", xv, m.fileID+"_"+m.expires+"_"+xVersionSecret, want)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode([]map[string]any{
		{"id": "sd", "name": "480p", "src": map[string]string{"view": "//sd/v.mp4", "download": "//sd/d.mp4"}},
		{"id": "src", "name": "Source", "src": map[string]string{"view": "//src/v.mp4", "download": m.downloadSrc}},
	})
}

// assertCommonHeaders verifies the iwara headers + cookie that the
// IwaraClient injects into every request.
func (m *mockIwaraServer) assertCommonHeaders(r *http.Request) {
	if r.Header.Get("X-Site") == "" {
		m.t.Errorf("missing X-Site header on %s", r.URL.Path)
	} else {
		m.sawXSite.Store(true)
	}
	if r.Header.Get("Origin") == "" {
		m.t.Errorf("missing Origin header on %s", r.URL.Path)
	}
	if r.Header.Get("Referer") == "" {
		m.t.Errorf("missing Referer header on %s", r.URL.Path)
	}
	if got := r.Header.Get("Cookie"); m.expectedCookie != "" && got != m.expectedCookie {
		m.t.Errorf("Cookie = %q, want %q", got, m.expectedCookie)
	} else if m.expectedCookie != "" && got != "" {
		m.sawCookie.Store(true)
	}
}

// TestResolveDownloadURL exercises the full 3-step download-URL resolution
// against a mock iwara API and asserts the returned URL is the Source
// resolution's CDN download src with an https: prefix.
//
// Discovery background: the download URL is produced by mirroring the
// open-source iwaradl flow (video info → resolution list → src.download).
// The test pins the exact URL the module must emit so that the open-source
// compatibility claim is executable, not just documented.
func TestResolveDownloadURL(t *testing.T) {
	m := newMockIwaraServer(t)

	cfg := NewModuleConfig()
	cfg.IWARACookie = m.expectedCookie
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	got, name, err := client.ResolveDownloadURL(context.Background(), m.videoID)
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	want := "https:" + m.downloadSrc
	if got != want {
		t.Errorf("ResolveDownloadURL = %q, want %q", got, want)
	}
	if name != "Source" {
		t.Errorf("resolution name = %q, want Source", name)
	}
	if !m.sawCookie.Load() {
		t.Error("cookie was never observed on the API requests")
	}
	if !m.sawXSite.Load() {
		t.Error("X-Site header was never observed")
	}
	if !m.sawXVersion.Load() {
		t.Error("X-Version header was never observed")
	}
}

// TestResolveDownloadURLXVersion verifies the X-Version signature is the
// SHA1 of "fileID_expires_<secret>" — the exact formula used by the
// open-source iwara downloaders.
func TestResolveDownloadURLXVersion(t *testing.T) {
	m := newMockIwaraServer(t)

	cfg := NewModuleConfig()
	cfg.IWARACookie = m.expectedCookie
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	if _, _, err := client.ResolveDownloadURL(context.Background(), m.videoID); err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	want := SHA1Hex(m.fileID + "_" + m.expires + "_" + xVersionSecret)
	if m.lastXVersion != want {
		t.Errorf("X-Version = %q, want %q", m.lastXVersion, want)
	}
}

// TestResolveDownloadURLNoCookie verifies that without a configured cookie
// no Cookie header is sent (anonymous access path).
func TestResolveDownloadURLNoCookie(t *testing.T) {
	m := newMockIwaraServer(t)
	m.expectedCookie = ""

	cfg := NewModuleConfig()
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	if _, _, err := client.ResolveDownloadURL(context.Background(), m.videoID); err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
}

// TestResolveDownloadURLEmptyID verifies the empty-ID guard.
func TestResolveDownloadURLEmptyID(t *testing.T) {
	cfg := NewModuleConfig()
	client := NewIwaraClient(&cfg)
	if _, _, err := client.ResolveDownloadURL(context.Background(), "  "); err == nil {
		t.Error("expected error for empty video ID")
	}
}

// TestResolveDownloadURLNoSourceFallsBack verifies the fallback to the
// first resolution when no "Source" entry is present.
func TestResolveDownloadURLNoSourceFallsBack(t *testing.T) {
	m := &mockIwaraServer{t: t}
	m.videoID = "abc123"
	m.fileID = "file789"
	m.expires = "1700000000"
	m.downloadSrc = "//sd/v.mp4"
	m.expectedCookie = ""

	mux := http.NewServeMux()
	mux.HandleFunc("/video/"+m.videoID, func(w http.ResponseWriter, r *http.Request) {
		m.assertCommonHeaders(r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      m.videoID,
			"file":    map[string]string{"id": m.fileID},
			"fileUrl": m.Server.URL + "/files/" + m.fileID + "/resolution?expires=" + m.expires,
		})
	})
	mux.HandleFunc("/files/"+m.fileID+"/resolution", func(w http.ResponseWriter, r *http.Request) {
		m.assertCommonHeaders(r)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "sd", "name": "480p", "src": map[string]string{"download": m.downloadSrc}},
		})
	})
	m.Server = httptest.NewTLSServer(mux)
	t.Cleanup(m.Server.Close)

	cfg := NewModuleConfig()
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	got, name, err := client.ResolveDownloadURL(context.Background(), m.videoID)
	if err != nil {
		t.Fatalf("ResolveDownloadURL: %v", err)
	}
	if got != "https:"+m.downloadSrc {
		t.Errorf("got %q, want https:%s", got, m.downloadSrc)
	}
	if name != "480p" {
		t.Errorf("name = %q, want 480p", name)
	}
}

// TestDoCookieInjection is a focused cookie-injection test: it issues a
// single request through IwaraClient.Do and asserts the Cookie header
// arrives verbatim.
//
// Discovery background: the "cookie 导入" requirement is satisfied by
// setting req.Header.Set("Cookie", cfg.IWARACookie). This test is the
// executable proof that the cookie the user pastes actually reaches the
// wire — and that an empty cookie results in no Cookie header.
func TestDoCookieInjection(t *testing.T) {
	const cookie = "iwara_session=abc; theme=dark; locale=en"

	var gotCookie string
	var gotXSite string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		gotXSite = r.Header.Get("X-Site")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := NewModuleConfig()
	cfg.IWARACookie = cookie
	client := NewIwaraClient(&cfg)
	client.client = srv.Client()

	resp, err := client.Do(context.Background(), http.MethodGet, srv.URL+"/anything", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if gotCookie != cookie {
		t.Errorf("Cookie = %q, want %q", gotCookie, cookie)
	}
	if gotXSite != DefaultIWARAHost {
		t.Errorf("X-Site = %q, want %q", gotXSite, DefaultIWARAHost)
	}

	// Empty cookie → no Cookie header.
	cfg.IWARACookie = ""
	var gotCookie2 string
	srv2 := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie2 = r.Header.Get("Cookie")
	}))
	t.Cleanup(srv2.Close)
	client2 := NewIwaraClient(&cfg)
	client2.client = srv2.Client()
	resp2, err := client2.Do(context.Background(), http.MethodGet, srv2.URL+"/anything", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp2.Body.Close()
	if gotCookie2 != "" {
		t.Errorf("empty config cookie must not set a Cookie header, got %q", gotCookie2)
	}
}

// recordingRT is a RoundTripper that records the request it receives and
// returns an empty OK response. Used to verify URL rewriting without any
// network I/O.
type recordingRT struct {
	req *http.Request
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.req = req.Clone(req.Context())
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
	}, nil
}

// TestDoRewriteThroughEchProxy verifies that when a Rewriter is attached,
// the request URL is rewritten to the ech-proxy entry host before sending.
//
// Discovery background: the "iwara URL 改写（只动 host）" requirement is
// satisfied by the Rewriter + IwaraClient.SetRewriter pairing. This test
// exercises the integration: client.Do rewrites the URL and the rewritten
// URL is what reaches the wire.
func TestDoRewriteThroughEchProxy(t *testing.T) {
	rt := &recordingRT{}

	cfg := NewModuleConfig()
	client := NewIwaraClient(&cfg)
	client.client = &http.Client{Transport: rt}

	r := NewRewriter(cfg.UpstreamSuffix, cfg.EntrySuffix, cfg.EntryPrefix, cfg.EntryPort)
	client.SetRewriter(&r)

	// Request a www.iwara.tv URL; the Rewriter must rewrite it to the
	// entry host before the transport sees it.
	resp, err := client.Do(context.Background(), http.MethodGet, "https://www.iwara.tv/videos/abc", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if rt.req == nil {
		t.Fatal("RoundTripper was not called")
	}
	entryHost := strings.TrimSuffix(cfg.EntryPrefix, "-") + "." + cfg.EntrySuffix
	if !strings.Contains(rt.req.Host, entryHost) {
		t.Errorf("request Host = %q, want to contain entry host %q", rt.req.Host, entryHost)
	}
	if rt.req.URL.Path != "/videos/abc" {
		t.Errorf("request path = %q, want /videos/abc (host-only rewrite)", rt.req.URL.Path)
	}
	if rt.req.URL.Query().Get("x") != "" {
		t.Error("unexpected query parameter")
	}

	// Non-iwara URL must NOT be rewritten.
	rt2 := &recordingRT{}
	client2 := NewIwaraClient(&cfg)
	client2.client = &http.Client{Transport: rt2}
	client2.SetRewriter(&r)
	resp2, err := client2.Do(context.Background(), http.MethodGet, "https://example.com/videos/abc", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp2.Body.Close()
	if rt2.req.Host != "example.com" {
		t.Errorf("non-iwara Host = %q, want example.com (should not be rewritten)", rt2.req.Host)
	}
}

// TestParseVideoID covers the URL → video ID parser.
func TestParseVideoID(t *testing.T) {
	tests := []struct {
		in     string
		wantID string
		wantOK bool
	}{
		{"https://www.iwara.tv/videos/abc123", "abc123", true},
		{"https://www.iwara.tv/videos/abc123?x=1", "abc123", true},
		{"https://iwara.tv/videos/abc123", "abc123", true},
		{"https://api.iwara.tv/something", "", false},
		{"https://example.com/videos/abc123", "", false},
		{"abc123", "abc123", true},
		{"", "", false},
		{"https://www.iwara.tv/videos/", "", false},
	}
	for _, tt := range tests {
		id, ok := ParseVideoID(tt.in)
		if ok != tt.wantOK || id != tt.wantID {
			t.Errorf("ParseVideoID(%q) = (%q, %v), want (%q, %v)", tt.in, id, ok, tt.wantID, tt.wantOK)
		}
	}
}

// TestSHA1Hex verifies the SHA1 helper against a known vector.
func TestSHA1Hex(t *testing.T) {
	// SHA1("abc") = a9993e364706816aba3e25717850c26c9cd0d89d
	if got := SHA1Hex("abc"); got != "a9993e364706816aba3e25717850c26c9cd0d89d" {
		t.Errorf("SHA1Hex(abc) = %q", got)
	}
}

// splitHostPort splits "host:port" and strips any IPv6 brackets.
func splitHostPort(addr string) (host, port string) {
	if strings.Contains(addr, ":") {
		if strings.HasPrefix(addr, "[") {
			end := strings.Index(addr, "]")
			if end < 0 {
				return addr, ""
			}
			host = addr[1:end]
			rest := addr[end+1:]
			if strings.HasPrefix(rest, ":") {
				port = rest[1:]
			}
			return host, port
		}
		i := strings.LastIndex(addr, ":")
		return addr[:i], addr[i+1:]
	}
	return addr, ""
}
