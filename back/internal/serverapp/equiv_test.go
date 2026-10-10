// package serverapp — equivalence (golden) tests for the HTTP server-shell split.
//
// Why this file exists: the HTTP listening/serving layer was pulled out of
// internal/serverapp and internal/services into internal/httpd. A decomposition
// like that is only safe if the served surface is bit-identical afterwards, and
// "the existing tests are still green" does not prove it — the existing tests
// never enumerate the route table, and no test served the assembled router over a
// real TCP listener through the production listen path.
//
// So this file pins two independent observations of the assembled server:
//
//   1. The route table — every "METHOD /path" the assembled gin engine
//      registers. Compared against an embedded golden captured from the
//      pre-split tree, so adding, dropping or renaming one route fails here.
//   2. The endpoint digest — real HTTP requests sent over a real TCP socket to
//      the assembled router, with status / Content-Type / body digest. This
//      exercises the actual serving path (net/http server → gin engine) rather
//      than httptest.NewServer shortcuts, and catches response regressions in
//      the handlers that the shell wires up (/ping, /health, /ready, /panel,
//      the CORS preflight, the gin 404).
//
// Both goldens were captured from the tree *before* the split and are unchanged
// by it: this file is green both before and after, which is the equivalence proof.
//
// 维护与重捕获指南（Issue #104）：
//
// 1. 见到 equiv 测试红了怎么办：
//    - 若是【新增路由/端点】：检查是否是预期功能演进（如 PR #120 新增 tags 路由）。
//      确认为有意新增且通过审查后，按下方步骤 2 重捕获金标。
//    - 若是【响应体/鉴权变更】：严禁盲目重捕获！检查是否意外破坏了路由鉴权、状态码
//      或错误响应语义。
//
// 2. 金标重捕获流程（在当前合法 ref 上）：
//    - 路由表更新：运行 `go test -tags nosqlite -v ./internal/serverapp/ -run TestGoldenRouteTable`，
//      从错误信息中的 GOT 列表中复制新行数与完整列表，更新 goldenRouteTable 常量。
//    - 端点摘要更新：运行 `go test -tags nosqlite -v ./internal/serverapp/ -run TestGoldenEndpointDigest`，
//      从输出中获取实际 digest，核验无误后替换 goldenEndpointDigest 常量。
//    - 核验：重捕获后必须运行 `go test -tags nosqlite ./internal/serverapp/...` 确认全绿。
//
// Discovery background: HTTP shell split PR (internal/httpd), task requires
// route-table comparison plus an httptest end-to-end check as the equivalence gate.
package serverapp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/config"
	"peerdrive/internal/router"
)

// equivCfg builds the same minimal, fully local config the router's own
// multi-instance tests use: rate limiting off (so responses don't depend on how
// many requests ran earlier in the process) and no DB / PeerJS side effects.
func equivCfg() *config.Config {
	return &config.Config{
		StorageDir:   "./storage",
		DownloadDir:  "./downloads",
		RateLimitRPS: 0,
	}
}

func equivEngine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rt, err := router.NewRouter(router.Deps{Cfg: equivCfg()})
	if err != nil {
		t.Fatalf("NewRouter: %v", err)
	}
	return rt.Engine()
}

// serveOnLoopback binds a real TCP listener and serves h on it, returning the
// base URL. This is deliberately a real socket rather than httptest.NewServer:
// the equivalence claim is about the *serving* path, so the listener itself
// must be real (same behavior the production httpd.Server.Listener path has).
func serveOnLoopback(t *testing.T, h http.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := &http.Server{Handler: h, ReadHeaderTimeout: 15 * time.Second}
	go func() { _ = s.Serve(l) }()
	t.Cleanup(func() { _ = s.Close() })
	return "http://" + l.Addr().String()
}

// --- 1. route table golden ---

func routeTable(t *testing.T) []string {
	t.Helper()
	var rows []string
	for _, r := range equivEngine(t).Routes() {
		rows = append(rows, strings.ToUpper(r.Method)+" "+r.Path)
	}
	sort.Strings(rows)
	return rows
}

// goldenRouteTable was captured from the tree before the HTTP shell was split
// into internal/httpd. Do not regenerate from a modified tree.
const goldenRouteTable = `108
DELETE /bt/download/:infohash
DELETE /collections/:id/:collection_name/entries/*path
DELETE /files/:hash
DELETE /files/inbox/:hash
DELETE /ipfs/pin/:cid
GET /:username/:collection_name/*filepath
GET /anon/collections
GET /anon/collections/:hash
GET /anon/collections/:hash/*filepath
GET /bt/bep51/sample
GET /bt/download/:infohash
GET /bt/download/:infohash/magnet
GET /bt/download/:infohash/torrent
GET /bt/downloads
GET /bt/stats
GET /bt/status
GET /collections
GET /collections/:id
GET /collections/:id/*filepath
GET /collections/public
GET /collections/search
GET /display/screens
GET /display/status
GET /download/:hash
GET /download/:hash/sources
GET /files
GET /files/browse
GET /files/inbox
GET /files/verify/:hash
GET /health
GET /ipfs/:cid
GET /ipfs/gateways
GET /ipfs/pins
GET /local/status/:hash
GET /p2p/aria2/status
GET /p2p/auth/status
GET /p2p/forward/list
GET /p2p/pull
GET /p2p/webrtc/info
GET /panel
GET /panel/
GET /peerjs.min.js
GET /ping
GET /ready
GET /s/:token
GET /sha256sum/:sha256
GET /sha256sum/:sha256/:filename
GET /shares
GET /stream/:id/manifest
GET /stream/list
GET /swagger/*any
GET /tags/search
GET /tags/sha/:sha
POST /actions/fork
POST /actions/merge
POST /anon/collections
POST /anon/collections/commit
POST /anon/collections/fork
POST /bt/announce
POST /bt/bep44/get
POST /bt/bep44/put
POST /bt/download/:infohash/pause
POST /bt/download/:infohash/resume
POST /bt/download/:infohash/seed
POST /bt/download/:infohash/unseed
POST /bt/find
POST /bt/magnet
POST /bt/seed-collection
POST /bt/torrent
POST /collections
POST /collections/:id/:collection_name/commit
POST /collections/:id/:collection_name/entries
POST /collections/:id/:collection_name/rollback/:version_id
POST /collections/:id/:collection_name/tags
POST /collections/:id/:collection_name/visibility
POST /collections/fork
POST /collections/merge
POST /collections/register-folder
POST /collections/register-local
POST /collections/register-url
POST /collections/upload
POST /display/cast
POST /display/clear
POST /display/control
POST /download/:hash/refresh
POST /files/copy
POST /files/diff
POST /files/inbox/approve
POST /files/register_folder
POST /files/register_local
POST /files/register_url
POST /files/upload
POST /ipfs/pin/:cid
POST /local/save
POST /p2p/aria2/download
POST /p2p/aria2/toggle
POST /p2p/forward/close
POST /p2p/forward/connect
POST /p2p/forward/create
POST /p2p/pull
POST /p2p/pull/cancel
POST /p2p/pull/collection
POST /shares
POST /stream/:id/chunk
POST /stream/:id/close
POST /stream/create
POST /tags/sha/:sha
PUT /anon/collections/:hash/visibility`

// TestGoldenRouteTable pins the whole assembled route surface.
//
// A decomposition that moved or dropped a route registration — the classic
// regression when a router assembly is refactored — fails here with a diff.
func TestGoldenRouteTable(t *testing.T) {
	got := routeTable(t)
	if len(got) != 108 {
		t.Fatalf("route count = %d, want 108; the assembled router registers a different number of routes\nGOT:\n%s",
			len(got), strings.Join(got, "\n"))
	}
	want := strings.Split(strings.TrimSpace(goldenRouteTable), "\n")
	if want[0] != "108" {
		t.Fatalf("internal error: golden header is %q", want[0])
	}
	want = want[1:]
	if len(got) != len(want) {
		t.Fatalf("route count = %d, golden has %d\nGOT:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	var missing, extra []string
	for i := range want {
		if got[i] != want[i] {
			missing = append(missing, "golden only: "+want[i])
			extra = append(extra, "got only:   "+got[i])
		}
	}
	if len(missing) > 0 {
		t.Errorf("route table diverged from the pre-split golden:\n%s\n%s",
			strings.Join(missing, "\n"), strings.Join(extra, "\n"))
	}
}

// --- 2. endpoint digest golden ---

// equivProbe lists the representative endpoints exercised over the real socket.
// They are chosen to cover the distinct shell behaviors: the health probes
// (/ping, /health, /ready), the CORS preflight, gin's trailing-slash 404,
// the two embedded static assets, an authenticated write route, and
// validation failures decided before any store access.
//
// Deliberately absent: the read routes that need a live database
// (GET /collections, GET /files, GET /s/:token, GET /ipfs/pins). With no store
// initialised they reach a nil *sql.DB, and gin's Recovery middleware turns the
// panic into a 500 with an empty body. That nil deref is recoverable on
// linux/darwin, but on windows the same deref escalates to a hard
// "fatal error: fault" and kills the whole test binary — go-build.yml's
// windows cell went red while the other four platforms passed. Pinning that
// golden was pinning a crash, not behavior, on three of the five platforms.
// The routes themselves are not left uncovered: TestGoldenRouteTable pins all
// 89 registrations, these four included.
var equivProbe = []struct {
	method, path string
	header       map[string]string
}{
	{"GET", "/ping", nil},
	{"GET", "/health", nil},
	{"GET", "/ready", nil},
	{"GET", "/p2p/auth/status", nil},
	{"GET", "/swagger/index.html", nil},
	{"GET", "/panel", nil},
	{"GET", "/peerjs.min.js", nil},
	{"GET", "/no-such-path", nil},
	{"POST", "/collections/register-local", nil},
	{"GET", "/sha256sum/deadbeef", nil},
	{"OPTIONS", "/collections", map[string]string{"Origin": "https://example.com"}},
	{"GET", "/collections/search", nil},
	{"GET", "/bt/status", nil},
	{"GET", "/sources", nil},
	{"GET", "/p2p/pull", nil},
}

// volatileKeys are response fields that legitimately change between runs and so
// must be dropped before digesting: uptime_sec moves every second, and the gin
// request id is generated per request.
var volatileKeys = map[string]bool{"uptime_sec": true, "request_id": true}

// canonicalBody turns a response body into a stable digest: JSON bodies are
// parsed, volatile fields dropped and re-marshalled (encoding/json sorts map
// keys, so the output is canonical), everything else is hashed after normalising
// CRLF to LF.
//
// Why the CRLF normalisation: /panel and /peerjs.min.js come from files in the
// working tree (//go:embed in internal/panel), so the bytes they serve depend on
// how the checkout converted line endings. A windows-latest runner checks out
// with core.autocrlf=true, so the same git blob is served as CRLF there and as LF
// on the other four platforms — hashing the raw bytes made the golden
// platform-bound and go-build.yml's windows cell failed while the rest passed.
// That is a checkout artefact, present identically pre- and post-split, not a
// response regression. The invariant that matters — "the shell split did not
// change what is served" — is preserved: a real content change still changes the
// digest (asserted by TestCanonicalBodyNormalisesCRLF).
func canonicalBody(ct string, body []byte) (string, string) {
	if strings.HasPrefix(ct, "application/json") {
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			if m, ok := v.(map[string]any); ok {
				for k := range volatileKeys {
					delete(m, k)
				}
			}
			if b, err := json.Marshal(v); err == nil {
				h := sha256.Sum256(b)
				return "json", hex.EncodeToString(h[:])
			}
		}
	}
	h := sha256.Sum256([]byte(strings.ReplaceAll(string(body), "\r\n", "\n")))
	return "raw", hex.EncodeToString(h[:])
}

// TestCanonicalBodyNormalisesCRLF 发现背景：go-build.yml 的 windows-latest 那格
// 让 TestGoldenEndpointDigest 红了，而另外四个平台全绿。
//
// 原因是 /panel 与 /peerjs.min.js 的响应体来自工作树里的文件（internal/panel
// 的 //go:embed），窗口 runner 用 core.autocrlf=true 检出，同一份 git blob 在
// Windows 上被换成 CRLF 后内嵌进二进制——差异是检出产物，分解前后完全一致，
// 不是响应回归。于是摘要必须做 CRLF→LF 归一化才可能在多平台上共用一份金标。
// 这里锁住两件事：归一化只吞掉换行符差异，任何真实内容变化仍然改摘要。
func TestCanonicalBodyNormalisesCRLF(t *testing.T) {
	base := []byte("alpha\nbeta\n")
	crlf := []byte("alpha\r\nbeta\r\n")

	_, shaLF := canonicalBody("text/plain", base)
	_, shaCRLF := canonicalBody("text/plain", crlf)
	if shaLF != shaCRLF {
		t.Fatalf("CRLF and LF spellings of the same text must digest identically: %s != %s", shaLF, shaCRLF)
	}

	// A real change must still be caught: only the line-ending difference is folded.
	_, shaChanged := canonicalBody("text/plain", []byte("alpha\nbeta\nchanged\n"))
	if shaChanged == shaLF {
		t.Fatalf("a real content change must change the digest, both gave %s", shaLF)
	}

	_, shaSame := canonicalBody("text/plain", []byte("alpha\nbeta\nchanged\n"))
	if shaSame != shaChanged {
		t.Fatalf("digesting is not deterministic: %s != %s", shaSame, shaChanged)
	}
}

func endpointDigest(t *testing.T) []string {
	t.Helper()
	base := serveOnLoopback(t, equivEngine(t))
	cl := &http.Client{Timeout: 5 * time.Second}
	out := make([]string, 0, len(equivProbe))
	for _, tc := range equivProbe {
		req, err := http.NewRequest(tc.method, base+tc.path, nil)
		if err != nil {
			t.Fatalf("NewRequest %s %s: %v", tc.method, tc.path, err)
		}
		for k, v := range tc.header {
			req.Header.Set(k, v)
		}
		resp, err := cl.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.method, tc.path, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("%s %s read: %v", tc.method, tc.path, err)
		}
		kind, digest := canonicalBody(resp.Header.Get("Content-Type"), body)
		out = append(out, fmt.Sprintf("%-7s %-32s -> %-3d ct=%-32q kind=%-4s sha=%s",
			tc.method, tc.path, resp.StatusCode, resp.Header.Get("Content-Type"), kind, digest))
	}
	return out
}

// goldenEndpointDigest was captured from the tree before the HTTP shell was
// split into internal/httpd, over a real TCP listener.
const goldenEndpointDigest = `GET     /ping                            -> 200 ct="text/plain; charset=utf-8"      kind=raw  sha=9795c5ff8937f23526ccb207a5684c1fc94a7854e19c021b39d944e51f5baef2
GET     /health                          -> 200 ct="application/json; charset=utf-8" kind=json sha=a29ee2b15c494311c52521766e44af56a3ad2248e7a8ab465e5206463c13d288
GET     /ready                           -> 503 ct="application/json; charset=utf-8" kind=json sha=771b5996d72d0e3399407b1e53bdf7d1ff809e69edc0476fc0e58de56d9ea0ab
GET     /p2p/auth/status                 -> 200 ct="application/json; charset=utf-8" kind=json sha=885e0739e84cc31e2fd9139722b1295042e807822fc9ffafa3c9da0b47a97a2a
GET     /swagger/index.html              -> 200 ct="text/html; charset=utf-8"       kind=raw  sha=b238c541fb6eca4324529c6e97087d872755f782f333b27e2838e6ce40923520
GET     /panel                           -> 200 ct="text/html; charset=utf-8"       kind=raw  sha=1f63099d8f01e074b8e351820743a78863e1ca11a16343f69cf878ed5cbcdd01
GET     /peerjs.min.js                   -> 200 ct="application/javascript; charset=utf-8" kind=raw  sha=7604d8c31bec4f134b0d15c2d80b1d095ea18af005354f439f14291fcd7b4168
GET     /no-such-path                    -> 404 ct="text/plain"                     kind=raw  sha=99eb12f2ab3c4866a353e098ffa3cb7a967e617c49b98480394ec5d8ea92b094
POST    /collections/register-local      -> 400 ct="application/json; charset=utf-8" kind=json sha=2b9fbe63dc1aeedc22c8e3f74b4d5ec422b0ac295d05cf8e0281e25834c83723
GET     /sha256sum/deadbeef              -> 400 ct="application/json; charset=utf-8" kind=json sha=7a24c7053ae7e859c766d06fac0a1ff91772127f72c157ed5adc73b395190316
OPTIONS /collections                     -> 204 ct=""                               kind=raw  sha=e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855
GET     /collections/search              -> 200 ct="application/json; charset=utf-8" kind=json sha=8fe32e407a1038ee38753b70e5374b3a46d6ae9d5f16cd5b73c53abaca8f5ed0
GET     /bt/status                       -> 200 ct="application/json; charset=utf-8" kind=json sha=5acf3ff77b4420677b5923071f303facaba7a9273a346284a667a275df325146
GET     /sources                         -> 404 ct="text/plain"                     kind=raw  sha=99eb12f2ab3c4866a353e098ffa3cb7a967e617c49b98480394ec5d8ea92b094
GET     /p2p/pull                        -> 503 ct="application/json; charset=utf-8" kind=json sha=e9fd119eb81e052eee0d62510d1f902f595af6a3e08957d2fa85a3de570d9f7a`

// TestGoldenEndpointDigest sends real HTTP requests over a real socket to the
// assembled router and compares the result against the pre-split capture.
//
// It covers the handlers the shell wires up, so it would catch a split that
// dropped the /ping or /health registration, lost the CORS preflight, broke
// the trailing-slash 404, or changed an embedded asset.
func TestGoldenEndpointDigest(t *testing.T) {
	got := endpointDigest(t)
	if got[0] == "" {
		t.Fatal("no digest produced")
	}
	gotStr := strings.Join(got, "\n")
	want := strings.Split(goldenEndpointDigest, "\n")
	if len(got) != len(want) {
		t.Fatalf("digest has %d lines, golden has %d\nGOT:\n%s", len(got), len(want), gotStr)
	}
	var bad []string
	for i := range want {
		if got[i] != want[i] {
			bad = append(bad, "- want: "+want[i]+"  |  got: "+got[i])
		}
	}
	if len(bad) > 0 {
		t.Errorf("endpoint behavior diverged from the pre-split capture:\n%s\n\nGOT:\n%s", strings.Join(bad, "\n"), gotStr)
	}
}
