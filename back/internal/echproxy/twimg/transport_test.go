package twimg

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingRT is a stub base transport that records what the RoundTripper hands it.
type recordingRT struct {
	mu        sync.Mutex
	urls      []string
	hosts     []string
	methods   []string
	responses []string
}

func (r *recordingRT) RoundTrip(req *http.Request) (*http.Response, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.urls = append(r.urls, req.URL.String())
	r.hosts = append(r.hosts, req.Host)
	r.methods = append(r.methods, req.Method)
	body := "upstream"
	if len(r.responses) > 0 {
		body = r.responses[0]
		r.responses = r.responses[1:]
	}
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK",
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
		Request: req,
	}, nil
}

func (r *recordingRT) urlsSeen() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.urls))
	copy(out, r.urls)
	return out
}

// TestRoundTrip_RewritesToEntry is the end-to-end httptest check. A request to
// https://pbs.twimg.com/... must arrive at the *local* listener with:
//   - the entry host as the SNI (that is how ech-proxy routes to pbs.twimg.com upstream)
//   - the entry host:port as the Host header
//   - TLS preserved (https never downgrades to http)
//   - path and query byte-identical to the original
func TestRoundTrip_RewritesToEntry(t *testing.T) {
	var (
		mu       sync.Mutex
		host     string
		path     string
		query    string
		sni      string
		isTLS    bool
		referer  string
		reqCount int
	)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		reqCount++
		host = r.Host
		path = r.URL.EscapedPath()
		query = r.URL.RawQuery
		referer = r.Header.Get("Referer")
		isTLS = r.TLS != nil
		if r.TLS != nil {
			sni = r.TLS.ServerName
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("twimg-bytes"))
	}))
	defer srv.Close()

	// The httptest server binds a real local address; the entry host is the production
	// SNI value, so nothing here depends on DNS.
	laddr := srv.Listener.Addr().String()
	_, portStr, err := net.SplitHostPort(laddr)
	require.NoError(t, err)

	entry := Entry{
		SrcHost:    DefaultSrcHost,
		EntryHost:  DefaultEntryHost,
		Port:       portStr,
		ListenAddr: laddr,
		SkipTLS:    true, // the httptest certificate is not in the system store
	}

	client, err := NewClient(entry, nil)
	require.NoError(t, err)

	const original = "https://pbs.twimg.com/media/profile_images/1/abc.jpg?name=foo&x=1%2B2"
	req, err := http.NewRequest(http.MethodGet, original, nil)
	require.NoError(t, err)
	req.Header.Set("Referer", "https://x.com/")
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, []byte("twimg-bytes"), body)

	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, reqCount)
	assert.True(t, isTLS, "https must stay https — no downgrade to http")
	assert.Equal(t, DefaultEntryHost, sni, "SNI must be the entry host so ech-proxy can route")
	assert.Equal(t, DefaultEntryHost+":"+portStr, host, "Host header must be the entry authority")
	assert.Equal(t, "/media/profile_images/1/abc.jpg", path, "path must be byte-identical")
	assert.Equal(t, "name=foo&x=1%2B2", query, "query must be byte-identical")
	assert.Equal(t, "https://x.com/", referer, "unrelated headers must survive the rewrite")
}

// TestRoundTrip_PassesThroughNonPbs proves the enabled module is a no-op for unrelated
// traffic: the base transport sees the original request object, uncloned and unmodified.
func TestRoundTrip_PassesThroughNonPbs(t *testing.T) {
	base := &recordingRT{}
	rt, err := NewRoundTripper(DefaultEntry(), base)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, "https://x.com/api/1/statuses/show/123", nil)
	require.NoError(t, err)
	origURL := req.URL.String()
	origHost := req.Host

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, []string{origURL}, base.urlsSeen(), "the URL must reach the base unchanged")
	assert.Equal(t, "x.com", req.URL.Host, "the caller's URL must not be mutated")
	assert.Equal(t, origHost, req.Host, "the caller's Host must not be mutated")
}

// TestRoundTrip_NilRequest returns an explicit error.
func TestRoundTrip_NilRequest(t *testing.T) {
	rt, err := NewRoundTripper(DefaultEntry(), &recordingRT{})
	require.NoError(t, err)
	_, err = rt.RoundTrip(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no URL")

	req := &http.Request{}
	_, err = rt.RoundTrip(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no URL")
}

// TestRoundTrip_MalformedURL surfaces the rewrite error rather than swallowing it.
func TestRoundTrip_MalformedURL(t *testing.T) {
	rt, err := NewRoundTripper(DefaultEntry(), &recordingRT{})
	require.NoError(t, err)

	// A URL with a host but no scheme stringifies to "//host/path" — not an absolute URL.
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Host: "pbs.twimg.com", Path: "/media/A.jpg"},
	}
	_, err = rt.RoundTrip(req)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidURL)
}

// TestRoundTrip_RewrittenRequestIsAClone ensures the caller's request is never mutated
// even when the rewrite fires.
func TestRoundTrip_RewrittenRequestIsAClone(t *testing.T) {
	base := &recordingRT{}
	rt, err := NewRoundTripper(DefaultEntry(), base)
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodGet, "https://pbs.twimg.com/media/A.jpg", nil)
	require.NoError(t, err)
	req.Header.Set("X-Custom", "keep-me")
	origURL := req.URL.String()
	origHost := req.Host

	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	assert.Equal(t, origURL, req.URL.String(), "the original request URL must be untouched")
	assert.Equal(t, origHost, req.Host, "the original Host must be untouched")
	assert.Equal(t, "https://twimg-pbs.l.moonchan.xyz:8443/media/A.jpg", base.urlsSeen()[0],
		"the base must receive the rewritten URL")
}

// TestNewTransport_ValidatesEntry rejects a bad entry before building anything.
func TestNewTransport_ValidatesEntry(t *testing.T) {
	_, err := NewTransport(Entry{})
	require.Error(t, err)
	_, err = NewTransport(DefaultEntry())
	require.NoError(t, err)
}

// TestNewClient_Validation and inheritance from a base client.
func TestNewClient(t *testing.T) {
	_, err := NewClient(Entry{}, nil)
	require.Error(t, err)

	base := &http.Client{Timeout: 42 * time.Second}
	client, err := NewClient(DefaultEntry(), base)
	require.NoError(t, err)
	assert.Equal(t, 42*time.Second, client.Timeout, "base client Timeout must be inherited")
	require.NotNil(t, client.Transport)
	_, ok := client.Transport.(*RoundTripper)
	assert.True(t, ok, "the transport must be the rewriting RoundTripper")
}

// TestNewClient_NilBase defaults to an unbounded timeout, so a long media fetch is not
// cut off mid-stream.
func TestNewClient_NilBase(t *testing.T) {
	client, err := NewClient(DefaultEntry(), nil)
	require.NoError(t, err)
	assert.Equal(t, time.Duration(0), client.Timeout)
}

// TestRoundTrip_DoesNotDowngrade asserts the scheme contract across many inputs: an https
// input never produces an http request, and an http input never produces an https one.
func TestRoundTrip_DoesNotDowngrade(t *testing.T) {
	base := &recordingRT{}
	rt, err := NewRoundTripper(DefaultEntry(), base)
	require.NoError(t, err)

	cases := []struct {
		in, scheme string
	}{
		{"https://pbs.twimg.com/a", "https"},
		{"https://pbs.twimg.com:443/a?b=c", "https"},
		{"http://pbs.twimg.com/a", "http"},
		{"http://pbs.twimg.com:8080/a?b=c", "http"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodGet, tc.in, nil)
			require.NoError(t, err)
			_, err = rt.RoundTrip(req)
			require.NoError(t, err)
			got := base.urlsSeen()[len(base.urlsSeen())-1]
			assert.True(t, strings.HasPrefix(got, tc.scheme+"://"),
				"input %q became %q", tc.in, got)
		})
	}
}
