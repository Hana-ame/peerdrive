package twimg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// TestNew_Defaults fills the documented defaults: pbs.twimg.com and v4.
func TestNew_Defaults(t *testing.T) {
	m, err := New(Config{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.SrcHost() != "pbs.twimg.com" {
		t.Errorf("SrcHost = %q, want pbs.twimg.com", m.SrcHost())
	}
	rt, ok := m.Client().Transport.(echRT)
	if !ok {
		t.Fatalf("Transport = %T, want echRT", m.Client().Transport)
	}
	if rt.srcHost != "pbs.twimg.com" || rt.ipMode != "v4" {
		t.Errorf("rt = %+v, want srcHost=pbs.twimg.com ipMode=v4", rt)
	}
}

// TestNew_NilBaseFallsBackToDefaultTransport keeps the base nil-safe: a caller
// that does not set HTTPBase must still get a usable transport.
func TestNew_NilBaseFallsBackToDefaultTransport(t *testing.T) {
	m, err := New(Config{SrcHost: "example.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	rt, ok := m.Client().Transport.(echRT)
	if !ok {
		t.Fatalf("Transport = %T, want echRT", m.Client().Transport)
	}
	if rt.base == nil {
		t.Error("base must not be nil (defaults to http.DefaultTransport)")
	}
}

// TestModule_Lifecycle is the facade contract the server relies on: New gives a
// working client, Start and Stop are safe to call, and Alive reports usable.
func TestModule_Lifecycle(t *testing.T) {
	m, err := New(Config{SrcHost: "pbs.twimg.com"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !m.Alive() {
		t.Error("Alive must be true immediately after New (no subprocess to watch)")
	}
	if m.Client() == nil {
		t.Error("Client must be non-nil immediately after New (no Start required)")
	}
	if err := m.Start(context.Background()); err != nil {
		t.Errorf("Start: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// recordingRT records every request the base transport receives.
type recordingRT struct {
	mu  sync.Mutex
	req *http.Request
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	r.req = req.Clone(req.Context())
	r.mu.Unlock()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       http.NoBody,
		Header:     make(http.Header),
	}, nil
}

func (r *recordingRT) saw() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.req != nil
}

// TestRoundTrip_MatchingHostGoesThroughECH is the module's core contract:
// pbs.twimg.com traffic is handed to ECH, never to the base transport. The
// previous implementation rewrote the host to twimg-pbs.l.moonchan.xyz and
// dialed 127.0.0.1:8443; ECH needs the real host, so the URL must be sent
// unchanged.
func TestRoundTrip_MatchingHostGoesThroughECH(t *testing.T) {
	base := &recordingRT{}
	gotECH := false
	old := echDo
	echDo = func(req *http.Request, ipMode string) (*http.Response, error) {
		gotECH = true
		if !strings.EqualFold(req.URL.Hostname(), "pbs.twimg.com") {
			t.Errorf("ECH received host %q, want pbs.twimg.com (no host rewrite)", req.URL.Hostname())
		}
		if req.URL.Path != "/media/x.jpg" {
			t.Errorf("ECH received path %q, want /media/x.jpg (path preserved)", req.URL.Path)
		}
		return nil, nil
	}
	defer func() { echDo = old }()

	rt := echRT{srcHost: "pbs.twimg.com", ipMode: "v4", base: base}
	req, err := http.NewRequest(http.MethodGet, "https://pbs.twimg.com/media/x.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rt.RoundTrip(req)

	if !gotECH {
		t.Error("matching host must be routed through ECH")
	}
	if base.saw() {
		t.Error("matching host must NOT reach the base transport")
	}
}

// TestRoundTrip_NonMatchingHostGoesToBase is what makes the module a pure
// additive layer: a node that stacks the exhentai module on top of it must
// still send unrelated traffic wherever the base transport sends it.
func TestRoundTrip_NonMatchingHostGoesToBase(t *testing.T) {
	base := &recordingRT{}
	old := echDo
	echDo = func(req *http.Request, ipMode string) (*http.Response, error) {
		t.Error("ECH must not be called for a non-matching host")
		return nil, nil
	}
	defer func() { echDo = old }()

	rt := echRT{srcHost: "pbs.twimg.com", ipMode: "v4", base: base}
	req, err := http.NewRequest(http.MethodGet, "https://exhentai.org/g/1/2/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	if !base.saw() {
		t.Error("non-matching host must reach the base transport")
	}
}

// TestRoundTrip_HostMatchIsCaseInsensitive matches the way a browser's URL bar
// treats hosts and keeps an operator-supplied SrcHost usable regardless of case.
func TestRoundTrip_HostMatchIsCaseInsensitive(t *testing.T) {
	base := &recordingRT{}
	gotECH := false
	old := echDo
	echDo = func(req *http.Request, ipMode string) (*http.Response, error) {
		gotECH = true
		return nil, nil
	}
	defer func() { echDo = old }()

	rt := echRT{srcHost: "Pbs.TwImg.Com", ipMode: "v4", base: base}
	req, err := http.NewRequest(http.MethodGet, "https://Pbs.twimg.com/media/x.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = rt.RoundTrip(req)
	if !gotECH || base.saw() {
		t.Error("case-insensitive host match must route through ECH")
	}
}

// TestRoundTrip_NilRequest errors instead of panicking: an http.Client can
// hand us a nil request on certain failure paths.
func TestRoundTrip_NilRequest(t *testing.T) {
	rt := echRT{srcHost: "pbs.twimg.com", base: &recordingRT{}}
	if _, err := rt.RoundTrip(nil); err == nil {
		t.Error("nil request must return an error")
	}
}

// TestNewRoundTrip_EchoesBaseTransport proves a caller-supplied HTTPBase is
// used for non-matching traffic, which is how the module stacks on the twimg
// client's own transport.
func TestNewRoundTrip_EchoesBaseTransport(t *testing.T) {
	var gotReq *http.Request
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(r.Context())
	}))
	defer base.Close()
	baseRT := base.Client().Transport.(http.RoundTripper)

	m, err := New(Config{SrcHost: "pbs.twimg.com", HTTPBase: baseRT})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, base.URL+"/g/1/2/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Client().Do(req); err != nil {
		t.Fatalf("Do: %v", err)
	}
	if gotReq == nil {
		t.Fatal("the base transport was not reached")
	}
}
