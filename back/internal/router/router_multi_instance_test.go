package router

// ── Multi-instance / parallel-safety tests for the dependency-injection refactor ──
//
// Discovery background (2026-10): before the refactor, the router package held its
// dependencies as package-level mutable globals written by `router.SetPeerJSService`,
// `router.SetSourceManager`, `router.SetRegServer`/`SetAdminToken`, etc., and read
// them back while SetupRouter registered routes. That produced three failures that
// are all *invisible to the compiler*:
//
//   1. Order dependence — wiring in the wrong order compiled and then silently
//      registered a subset of routes.
//   2. One instance per process — a second Router overwrote the first's deps.
//   3. Serialized tests — every test had to reset the globals first.
//
// These tests pin the replacement behavior. Each one constructs its own Router (or
// Authenticator) from a Deps value and asserts that a concurrently-constructed
// neighbor with different dependencies cannot affect it. All of them are
// `t.Parallel()`, which is only sound because no test touches shared package state.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
)

// testCfg builds a minimal, fully local Config.
//
// Why construct it field-by-field instead of config.Load(): Load() reads process
// environment variables, which are shared across every test in the package — a
// second instance setting PEERDRIVE_* would silently change the first one's config.
// That would reintroduce exactly the coupling these tests exist to rule out.
func testCfg(overrides ...func(*config.Config)) *config.Config {
	cfg := &config.Config{
		StorageDir:  defaultTestStorageDir,
		DownloadDir: defaultTestDownloadDir,
		// Rate limiting off: these tests assert on routing/auth, and a limiter
		// would make results depend on how many requests ran before.
		RateLimitRPS:   0,
		DisableSwagger: true,
	}
	for _, f := range overrides {
		f(cfg)
	}
	return cfg
}

// Plain relative paths: these tests only read config values, never touch disk,
// and nothing here writes into either directory.
const (
	defaultTestStorageDir  = "./storage"
	defaultTestDownloadDir = "./downloads"
)

// TestTwoRouters_DifferentAdminTokens_IndependentAuth is the headline case: two
// Routers in one process, each with a *different* admin token, each serving the
// same route. Under the old package-global scheme the second SetAdminToken would
// have silently replaced the first, so only one of these two assertions could ever
// have held.
func TestTwoRouters_DifferentAdminTokens_IndependentAuth(t *testing.T) {
	t.Parallel()

	newRouterWithToken := func(token string) *Router {
		cfg := testCfg(func(c *config.Config) { c.AdminToken = token })
		rt, err := NewRouter(Deps{Cfg: cfg})
		require.NoError(t, err)
		return rt
	}

	strict := newRouterWithToken("test-mock-auth-A")
	permissive := newRouterWithToken("test-mock-auth-B")

	probe := func(rt *Router) *httptest.ResponseRecorder {
		// A tiny engine over the same Authenticator, so the assertion targets the
		// auth middleware rather than the full route table (which needs a DB).
		engine := newAuthTestEngine(rt.auth)
		return authRequest(engine, "/protected", "Bearer test-mock-auth-A")
	}

	assert.Equal(t, http.StatusOK, probe(strict).Code,
		"router A must accept its own token")
	assert.Equal(t, http.StatusUnauthorized, probe(permissive).Code,
		"router B must reject router A's token")
}

// TestTwoRouters_OneAuthDisabled_OtherAuthEnforced pins the nastier version of the
// same bug: one router runs with auth fully disabled (single-machine mode) while
// the other enforces a token. With globals, building the disabled one last would
// have opened up the enforced one too.
func TestTwoRouters_OneAuthDisabled_OtherAuthEnforced(t *testing.T) {
	t.Parallel()

	open, err := NewRouter(Deps{Cfg: testCfg()}) // no reg server, no admin token
	require.NoError(t, err)
	guarded, err := NewRouter(Deps{Cfg: testCfg(func(c *config.Config) { c.AdminToken = "s3cret" })})
	require.NoError(t, err)

	openEngine := newAuthTestEngine(open.auth)
	guardedEngine := newAuthTestEngine(guarded.auth)

	assert.Equal(t, http.StatusServiceUnavailable,
		authRequest(openEngine, "/protected", "").Code,
		"auth-disabled router returns 503 (Issue #282: no more pass-through)")
	assert.Equal(t, http.StatusUnauthorized,
		authRequest(guardedEngine, "/protected", "").Code,
		"the disabled router must not have weakened its neighbor")
}

// TestTwoAuthenticators_TokenCachesAreNotShared is the cache-isolation test.
//
// The token cache used to be a single package-level map, so a token validated by
// one router was pre-seeded for every other router in the process — including ones
// pointed at a different registration server. It is now a field of Authenticator.
// This asserts isolation at three levels: the maps are distinct, a write through
// one instance is invisible to the other, and each instance still caches its own.
func TestTwoAuthenticators_TokenCachesAreNotShared(t *testing.T) {
	t.Parallel()

	a := NewAuthenticator("http://reg-a.invalid", "")
	b := NewAuthenticator("http://reg-b.invalid", "")

	require.Equal(t, 0, a.tokenCache.len(), "a fresh Authenticator starts with an empty cache")
	require.Equal(t, 0, b.tokenCache.len(), "a fresh Authenticator starts with an empty cache")

	// Write through A's cache. (Calling the unexported cache methods directly is
	// the point: no remote server is involved, so the test stays offline and
	// deterministic, while still exercising the exact map that used to be shared.)
	a.tokenCache.put("test-cache-key-1", "alice", "user")

	assert.Equal(t, 1, a.tokenCache.len(), "A must see its own entry")
	assert.Equal(t, 0, b.tokenCache.len(), "B must NOT see A's entry — caches are per-instance")

	// B's cache knows nothing about that token, so B is forced to do its own
	// validation rather than trusting A's result.
	_, _, ok := b.tokenCache.get("test-cache-key-1")
	assert.False(t, ok, "a token cached by A must not be visible to B")

	// B caches its own entry independently; A's count must not move.
	b.tokenCache.put("test-cache-key-2", "bob", "user")
	assert.Equal(t, 1, a.tokenCache.len(), "B's write must not reach A")
	assert.Equal(t, 1, b.tokenCache.len())
}

// TestTwoRouters_ConcurrentConstruction_IndependentDeps builds many Routers at
// once, each with its own distinct dependency set, and then drives requests
// through all of them concurrently.
//
// Why "many" and not just two: with two instances a sequential test could pass by
// accident. Running construction under -race with distinct per-instance values is
// what actually proves there is no shared mutable state left — if any package
// global survived, this is the test that trips the race detector.
func TestTwoRouters_ConcurrentConstruction_IndependentDeps(t *testing.T) {
	t.Parallel()

	const instances = 8
	type built struct {
		rt     *Router
		engine *gin.Engine
		token  string
	}

	var wg sync.WaitGroup
	builtRouters := make([]built, instances)

	for i := 0; i < instances; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			token := "mock-auth-key-" + string(rune('A'+i))
			cfg := testCfg(func(c *config.Config) { c.AdminToken = token })
			rt, err := NewRouter(Deps{Cfg: cfg})
			if err != nil {
				return
			}
			builtRouters[i] = built{rt: rt, engine: newAuthTestEngine(rt.auth), token: token}
		}(i)
	}
	wg.Wait()

	// Every instance must have been built, and each must answer for exactly its
	// own token — not for its neighbours', and not for the last one built.
	for i, b := range builtRouters {
		require.NotNil(t, b.rt, "router %d failed to construct", i)
		assert.Equal(t, http.StatusOK,
			authRequest(b.engine, "/protected", "Bearer "+b.token).Code,
			"router %d must accept its own token", i)
		other := "mock-auth-key-" + string(rune('A'+(i+1)%instances))
		assert.Equal(t, http.StatusUnauthorized,
			authRequest(b.engine, "/protected", "Bearer "+other).Code,
			"router %d must reject another instance's token", i)
	}
}

// TestTwoRouters_ConcurrentRequestServing_NoCrossTalk drives many authenticated
// requests through concurrently-built routers at the same time. Under the old
// scheme this was the failure mode where one test's token would leak into
// another's request path.
func TestTwoRouters_ConcurrentRequestServing_NoCrossTalk(t *testing.T) {
	t.Parallel()

	const instances = 6
	engines := make([]*gin.Engine, instances)
	tokens := make([]string, instances)

	for i := 0; i < instances; i++ {
		token := "tok-" + string(rune('a'+i))
		tokens[i] = token
		rt, err := NewRouter(Deps{Cfg: testCfg(func(c *config.Config) { c.AdminToken = token })})
		require.NoError(t, err)
		engines[i] = newAuthTestEngine(rt.auth)
	}

	var wg sync.WaitGroup
	for i := 0; i < instances; i++ {
		for round := 0; round < 10; round++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				engine := engines[i]
				if got := authRequest(engine, "/protected", "Bearer "+tokens[i]).Code; got != http.StatusOK {
					t.Errorf("router %d rejected its own token: got %d", i, got)
				}
				// A neighbour's token must always be rejected.
				other := tokens[(i+1)%instances]
				if got := authRequest(engine, "/protected", "Bearer "+other).Code; got != http.StatusUnauthorized {
					t.Errorf("router %d accepted another instance's token: got %d", i, got)
				}
			}(i)
		}
	}
	wg.Wait()
}

// TestNewRouter_DepsAreCapturedByValue verifies that the Router keeps its own copy
// of the Deps struct: mutating the caller's struct afterwards cannot reach into an
// already-built Router. (Assignment copies the struct, so this holds for the
// fields the Router itself holds.)
func TestNewRouter_DepsAreCapturedByValue(t *testing.T) {
	t.Parallel()

	cfg := testCfg(func(c *config.Config) { c.AdminToken = "original" })
	peerSvc := transport.NewPeerJSService(cfg, defaultTestStorageDir)
	t.Cleanup(peerSvc.Close)

	deps := Deps{Cfg: cfg, PeerJSService: peerSvc, SourceManager: source.New()}
	rt, err := NewRouter(deps)
	require.NoError(t, err)

	// Caller mutates both the struct and the Config it points at.
	deps.SourceManager = nil
	cfg.AdminToken = "mutated"

	assert.NotNil(t, rt.deps.SourceManager, "Router must keep its own copy of Deps")
	assert.Equal(t, "original", rt.auth.adminToken,
		"Router must capture its config at construction time")
}

// TestNewRouter_PeerBackedDepsRequirePeerJSService checks the up-front validation
// that replaced the old silent behavior. Previously a PeerPuller or source manager
// handed to the router with no PeerJS service produced routes that registered fine
// and then nil-panicked on first use; now it is a startup error naming the field.
func TestNewRouter_PeerBackedDepsRequirePeerJSService(t *testing.T) {
	t.Parallel()

	// A NodeDirectory/NodeShare/PeerPuller/SourceManager with no PeerJS transport
	// behind it cannot function: each of them dials, fetches from, or is answered
	// by that transport.
	cases := []struct {
		name string
		deps Deps
	}{
		{"NodeDirectory", Deps{Cfg: testCfg(), NodeDirectory: &service.NodeDirectory{}}},
		{"NodeShare", Deps{Cfg: testCfg(), NodeShare: &service.NodeShare{}}},
		{"PeerPuller", Deps{Cfg: testCfg(), PeerPuller: service.NewPeerPuller(defaultTestDownloadDir)}},
		{"SourceManager", Deps{Cfg: testCfg(), SourceManager: source.New()}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewRouter(tc.deps)
			require.Error(t, err, "%s without PeerJSService must be rejected", tc.name)
			assert.Contains(t, err.Error(), "PeerJSService")
		})
	}
}

// TestNewRouter_NoPeerJS_IsValid documents the "feature off" case: a node with
// PeerJS disabled is a completely normal configuration, so NewRouter must accept
// it rather than treating "no peer transport" as a misconfiguration.
func TestNewRouter_NoPeerJS_IsValid(t *testing.T) {
	t.Parallel()

	rt, err := NewRouter(Deps{Cfg: testCfg()})
	require.NoError(t, err)
	require.NotNil(t, rt)
	assert.Nil(t, rt.deps.PeerJSService)
	assert.Nil(t, rt.deps.SourceManager)
	assert.Nil(t, rt.deps.NodeDirectory)
}

// TestNewRouter_PeerJSEnabled_KeepsItsOwnService is the direct multi-instance
// assertion on the dependency that used to be a single package global: two Routers
// given two *different* PeerJSService instances must each report their own node ID
// from GET /peerjs/node.
//
// This is the closest thing to the original bug report: before the refactor,
// registerPeerJSRoutes read the package global at registration time, so building a
// second router silently re-pointed the first one's /peerjs/node handler.
func TestNewRouter_PeerJSEnabled_KeepsItsOwnService(t *testing.T) {
	t.Parallel()

	mk := func(id string) *transport.PeerJSService {
		t.Helper()
		cfg := testCfg(func(c *config.Config) { c.PeerJSID = id })
		// NewPeerJSService does not dial anything until Start(), which we
		// deliberately do not call: the routes under test only read ID().
		svc := transport.NewPeerJSService(cfg, defaultTestStorageDir)
		// Close it anyway: constructing the service already starts the FileIndexService
		// reaper goroutine, and this repo's standing rule is that anything opened must
		// be closed (on Windows an unclosed handle keeps the file locked).
		t.Cleanup(svc.Close)
		return svc
	}

	svcA, svcB := mk("node-A"), mk("node-B")
	rtA, err := NewRouter(Deps{Cfg: testCfg(), PeerJSService: svcA})
	require.NoError(t, err)
	rtB, err := NewRouter(Deps{Cfg: testCfg(), PeerJSService: svcB})
	require.NoError(t, err)

	assert.Same(t, svcA, rtA.deps.PeerJSService, "router A must keep service A")
	assert.Same(t, svcB, rtB.deps.PeerJSService, "router B must keep service B")
	assert.NotSame(t, rtA.deps.PeerJSService, rtB.deps.PeerJSService,
		"the two routers must not share a peer transport")

	// Register the PeerJS route group on two separate engines. Doing this
	// sequentially is the crux: under the old globals, building B's engine after
	// A's is what made both handlers read service B.
	engineA := newPeerJSEngine(t, rtA)
	engineB := newPeerJSEngine(t, rtB)

	_, bodyA := serveGET(engineA, "/peerjs/node")
	_, bodyB := serveGET(engineB, "/peerjs/node")

	assert.Contains(t, bodyA, "node-A", "router A's /peerjs/node must report node A")
	assert.Contains(t, bodyB, "node-B", "router B's /peerjs/node must report node B")
	assert.NotContains(t, bodyA, "node-B", "router A must not have picked up B's service")
	assert.NotContains(t, bodyB, "node-A", "router B must not have picked up A's service")
}

// TestPeerJSNode_DoesNotLeakSignalKey 发现背景：审计 R2 HIGH（2026-10-08）。
// /peerjs/node 是匿名端点，之前返回 signal_key（完整信令凭据）——任何匿名客户端
// 读一次就拿到了。与 R1 对 signalserver /status 移除 key 的修复逻辑一致。
func TestPeerJSNode_DoesNotLeakSignalKey(t *testing.T) {
	t.Parallel()

	svc := transport.NewPeerJSService(testCfg(), defaultTestStorageDir)
	t.Cleanup(svc.Close)

	rt, err := NewRouter(Deps{Cfg: testCfg(), PeerJSService: svc})
	require.NoError(t, err)

	engine := newPeerJSEngine(t, rt)
	_, body := serveGET(engine, "/peerjs/node")

	// signal_key must NOT appear — it is a shared secret (R2 HIGH fix).
	assert.NotContains(t, body, "signal_key",
		"/peerjs/node must not return signal_key (anonymous endpoint)")
	// The other signal_* fields must still be present (panel bootstrap depends on them).
	assert.Contains(t, body, "signal_host",
		"/peerjs/node must still return signal_host for panel bootstrap")
	assert.Contains(t, body, "signal_port")
	assert.Contains(t, body, "signal_path")
	assert.Contains(t, body, "signal_secure")
}

// newPeerJSEngine registers just the PeerJS route group onto its own engine,
// with no auth in front, and returns it.
func newPeerJSEngine(t *testing.T, rt *Router) *gin.Engine {
	t.Helper()
	e := gin.New()
	// No auth dependency needed for the assertions below; pass through.
	rt.registerPeerJSRoutes(e, func(c *gin.Context) { c.Next() })
	return e
}

// serveGET drives one GET request through an engine and returns the body.
func serveGET(e *gin.Engine, path string) (int, string) {
	w := httptest.NewRecorder()
	e.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code, w.Body.String()
}
