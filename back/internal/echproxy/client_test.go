package echproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"peerdrive/internal/echcore"
)

// TestNewIwaraClient_ECHTransport locks the module's trust model: the client's
// transport is the in-process ECH RoundTripper, so no request ever reaches
// localhost and no TLS verification is skipped. The previous implementation
// dialed 127.0.0.1:8443 with InsecureSkipVerify=true for the local proxy's
// self-signed certificate.
func TestNewIwaraClient_ECHTransport(t *testing.T) {
	client := NewIwaraClient(nil)
	if _, ok := client.client.Transport.(echcore.RoundTripper); !ok {
		t.Fatalf("client.Transport = %T, want echcore.RoundTripper", client.client.Transport)
	}
	if client.client.Timeout != 0 {
		t.Errorf("client.Timeout = %v, want 0 (file downloads must be able to stream)", client.client.Timeout)
	}
}

// TestDo_SendsURLUnchanged is the reason ech-proxy no longer runs a subprocess:
// ECH domain fronting reaches iwara.tv with the real host in the URL, so there
// is nothing to rewrite and no entry host to dial. A regression that reintroduces
// a host rewrite would fail this test.
func TestDo_SendsURLUnchanged(t *testing.T) {
	var gotReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(r.Context())
	}))
	defer srv.Close()

	client := NewIwaraClient(nil)
	client.baseURL = srv.URL
	client.client = srv.Client()

	target := srv.URL + "/videos/abc"
	resp, err := client.Do(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if gotReq == nil {
		t.Fatal("the server was not reached")
	}
	// On the server side http.Request.URL carries only the path (the scheme and
	// host arrive in the Host header), so assert on the path and the Host header.
	if gotReq.URL.Path != "/videos/abc" {
		t.Errorf("URL path = %q, want /videos/abc (path preserved)", gotReq.URL.Path)
	}
	if gotReq.URL.Query().Encode() != "" {
		t.Errorf("unexpected query %q", gotReq.URL.RawQuery)
	}
	if gotReq.Host != srv.Listener.Addr().String() {
		t.Errorf("Host = %q, want the real host %q (no host rewrite)", gotReq.Host, srv.Listener.Addr().String())
	}
}

// TestDo_IwaraHeadersAreSent proves the standard iwara headers (Origin / X-Site /
// Referer) are injected. They mirror the ech-proxy upstream.json entry and are
// what makes the iwara API accept the request as a browser.
func TestDo_IwaraHeadersAreSent(t *testing.T) {
	var gotReq *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(r.Context())
	}))
	defer srv.Close()

	client := NewIwaraClient(nil)
	client.client = srv.Client()

	_, err := client.Do(context.Background(), http.MethodGet, srv.URL+"/videos/abc", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotReq == nil {
		t.Fatal("the server was not reached")
	}

	want := IwaraHeaders(DefaultIWARAHost)
	for k, v := range want {
		if got := gotReq.Header.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

// TestDo_CookieInjectionEmptyCookie confirms an unset cookie header is left
// absent rather than being set to the empty string, which some servers treat as
// an explicit logout.
func TestDo_CookieInjectionEmptyCookie(t *testing.T) {
	var gotCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
	}))
	defer srv.Close()

	client := NewIwaraClient(&ModuleConfig{IWARACookie: ""})
	client.client = srv.Client()

	_, err := client.Do(context.Background(), http.MethodGet, srv.URL+"/videos/abc", nil)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotCookie != "" {
		t.Errorf("Cookie = %q, want empty (no cookie injected)", gotCookie)
	}
}
