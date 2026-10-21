package transport

// pull_test.go: Behavioral contract for network pull (pull).
//
// The only reason this file exists: pull is the first verb where this node
// accepts an "externally specified target address". A bug here does not break
// a feature -- it *turns the node into an internal network scanner for
// someone else*. So every SSRF boundary must be nailed down (including DNS
// pointing to internal IPs, IPv4-mapped IPv6, and redirect bypasses). The
// success path must also be covered -- otherwise tightening the guard so much
// that good URLs are rejected, no one would notice.

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// allowOnly temporarily swaps the SSRF check to "only allow the host of rawURL,
// everything else still uses the real check".
//
// Why not just return nil: httptest can only bind to loopback, and gardPullURL
// rejecting loopback is correct. The allowed scope must be as narrow as a single
// host, otherwise the redirect test would let itself through too, making that test
// meaningless.
func allowOnly(t *testing.T, rawURL string) {
	t.Helper()
	u, err := url.Parse(rawURL)
	require.NoError(t, err)
	oldGuard := pullGuard
	oldTransport := pullTransport
	pullGuard = func(x *url.URL) error {
		if x.Host == u.Host {
			return nil
		}
		return guardPullURL(x)
	}
	pullTransport = http.DefaultTransport
	t.Cleanup(func() {
		pullGuard = oldGuard
		pullTransport = oldTransport
	})
}

// TestPull_Success Normal pull: content lands in the index, and the hash in the
// response frame can be looked up in the index. Success cannot be judged by just
// "got pulled" -- if the response says success but the index has no entry, that's
// the easiest false success to miss.
func TestPull_Success(t *testing.T) {
	const body = "hello from remote url\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/remote.txt", "name": "pulled.txt", "reqId": "r1",
	}))

	h, ok := waitSent(sess, "pulled", 3*time.Second)
	require.True(t, ok, "should reply pulled frame")
	assert.Equal(t, "r1", h["reqId"])
	assert.Equal(t, int64(len(body)), int64(h["total"].(float64)))
	assert.Equal(t, "pulled.txt", h["name"])

	hash, _ := h["hash"].(string)
	require.NotEmpty(t, hash)
	fi, err := svc.fileIndex.Info(hash)
	require.NoError(t, err, "the hash from pulled must be findable in the index (content-addressing promise)")
	assert.Equal(t, int64(len(body)), fi.Size)
}

// TestPull_RejectsNonHTTP Non-http(s) is always rejected (file://, gopher://, ftp://).
func TestPull_RejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com/1", "ftp://example.com/x"} {
		initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
		svc := newTestPeerJSService(t)
		sess := &fakeSession{id: "peer-x"}
		svc.bindConn(sess)
		svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

		svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
			"type": "pull", "url": raw, "reqId": "r",
		}))
		h, ok := waitSent(sess, "err", 2*time.Second)
		require.True(t, ok, "%s should be rejected", raw)
		assert.Contains(t, h["msg"], "only http/https supported", "%s rejection reason should be clear", raw)
	}
}

// TestPull_RejectsPrivateTargets Internal/local/link-local is always rejected --
// the core SSRF surface: 169.254.169.254 is the cloud metadata endpoint,
// 127.0.0.1 is the local admin port.
func TestPull_RejectsPrivateTargets(t *testing.T) {
	for _, host := range []string{
		"127.0.0.1", "127.0.0.1:8080", "[::1]", "0.0.0.0",
		"10.0.0.5", "192.168.1.1", "172.16.0.1", "172.31.255.254",
		"169.254.169.254", "localhost", "[::ffff:127.0.0.1]", "[fd00::1]",
	} {
		u, err := url.Parse("http://" + host + "/x")
		require.NoError(t, err, "%s should be resolvable", host)
		err = guardPullURL(u)
		assert.Error(t, err, "%s must be rejected", host)
		if err != nil {
			// DNS resolution failures can mask things on certain hosts/network conditions;
			// distinguish and handle them separately
			assert.NotContains(t, err.Error(), "domain resolution failed", "%s should be rejected by address check", host)
		}
	}
}

// TestPull_RejectsUserInfo user@host is a historically famous parsing difference
// bypass technique; pin it down separately.
func TestPull_RejectsUserInfo(t *testing.T) {
	u, err := url.Parse("http://user@127.0.0.1/x")
	require.NoError(t, err)
	assert.Error(t, guardPullURL(u))

	// Empty URL goes to another branch (no URL means no SSRF is possible)
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})
	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{"type": "pull", "reqId": "r-empty"}))
	h, ok := waitSent(sess, "err", 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, "url required", h["msg"])
}

// TestPull_RejectsRedirectToInternal A public URL redirecting to internal must be
// blocked **before the redirect**. If we only check the first hop, a 302 sitting
// on a public server can punch through the entire guard.
func TestPull_RejectsRedirectToInternal(t *testing.T) {
	pub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/secret", http.StatusFound)
	}))
	defer pub.Close()

	allowOnly(t, pub.URL) // only allow the first hop; redirect target still uses the real check
	initTestDB(t)         // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": pub.URL + "/hop", "reqId": "r-redir",
	}))

	h, ok := waitSent(sess, "err", 3*time.Second)
	require.True(t, ok, "redirect to internal must be rejected")
	assert.Contains(t, h["msg"].(string), "redirect target rejected")
}

// TestPull_HTTPErrorStatus Non-2xx must return an error; we must not store error
// pages as content -- otherwise the hash would point to an error page and
// everyone pulling it later would get garbage.
func TestPull_HTTPErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/missing", "reqId": "r-404",
	}))

	h, ok := waitSent(sess, "err", 3*time.Second)
	require.True(t, ok)
	assert.Contains(t, h["msg"].(string), "HTTP 404")
}

// TestPull_SizeCap Exceeding the cap must abort; we must not leave a half file.
// Silent truncation is the worst outcome: the hash won't match the content, and the
// error only surfaces when someone else pulls it much later.
func TestPull_SizeCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length: force LimitReader to discover the limit in the stream
		for i := 0; i < 10; i++ {
			_, _ = w.Write(make([]byte, 32*1024)) // 320KB, exceeds the 64KB cap
		}
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	svc.cfg.MaxUploadBytes = 64 * 1024
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/big", "name": "big.bin", "reqId": "r-cap",
	}))

	h, ok := waitSent(sess, "err", 5*time.Second)
	require.True(t, ok, "exceeding the cap must error, not truncate and store")
	assert.Contains(t, h["msg"].(string), "exceeds this node's limit")

	// A leftover half-file would cause subsequent same-name uploads/Create to misjudge,
	// so it must be cleaned up
	files, err := svc.fileIndex.List(0, 100)
	require.NoError(t, err)
	for _, f := range files {
		assert.NotEqual(t, "big.bin", f.Name, "an over-cap pull should not leave a half-file")
	}
}

// TestPull_NameFallsBackToURLBasename When no name is given, use the last segment
// of the URL path.
func TestPull_NameFallsBackToURLBasename(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("fallback-name"))
	}))
	defer srv.Close()

	allowOnly(t, srv.URL)
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest("peer-x", []string{CapReq, CapShare, CapPull})

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": srv.URL + "/deep/path/report.csv", "reqId": "r-name",
	}))

	h, ok := waitSent(sess, "pulled", 3*time.Second)
	require.True(t, ok)
	assert.Equal(t, "report.csv", h["name"])
}

// TestPull_GatedByPSK pull must pass the PSK gate: a node with a configured key,
// a peer that hasn't presented a key **should not even get the chance to attempt SSRF**.
func TestPull_GatedByPSK(t *testing.T) {
	initTestDB(t) // in-memory SQLite: WriteFile ultimately writes the file_index table
	svc := newTestPeerJSService(t)
	svc.cfg.PeerPSK = "secret"
	sess := &fakeSession{id: "peer-x"}
	svc.bindConn(sess)

	svc.dispatchFrame(sess, svc.pending[sess], pskFrame(t, map[string]any{
		"type": "pull", "url": "http://169.254.169.254/latest/meta-data/", "reqId": "r-psk",
	}))

	h, ok := waitSent(sess, "err", 2*time.Second)
	require.True(t, ok)
	assert.Equal(t, pskErrCode, h["code"], "when no key is presented, should reply PSK_REQUIRED")
}
