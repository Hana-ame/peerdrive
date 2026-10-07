// deps.go: Router's dependency set and its constructor — the single place that
// says "what this package needs from outside".
//
// ── Why this file exists (2026-10 refactor: package-level injection → constructor injection) ──
//
// The router used to hold its dependencies as package-level mutable globals:
//
//	var peerjsService *transport.PeerJSService   // peerjs_routes.go
//	var peerjsCfg     *config.Config
//	var nodeDirectory *service.NodeDirectory
//	var sourceManager *source.Manager             // source_routes.go
//	var regServerURL  string                      // auth_middleware.go
//	var adminToken    string
//	var tokenCache    = struct{...}{...}          // shared, package-wide
//
// wired by `router.SetPeerJSService(...)`-style calls from serverapp and read
// *during* SetupRouter. Three real consequences, all of them bugs waiting to happen:
//
//  1. **Order dependence, with no compiler help.** "SetPeerJSService must be called
//     before SetupRouter; it's read during route registration" was a comment — the
//     wrong order compiles fine and fails at runtime, usually as *missing routes*
//     (registerPeerJSRoutes returns early when peerjsService == nil) rather than as
//     an error.
//  2. **One instance per process.** Two Router instances in the same process would
//     share (and overwrite) each other's PeerJS service, source manager and auth
//     config. That makes it impossible to test one route group against a stub
//     without also mutating the instance the other tests are using.
//  3. **Tests could not run in parallel.** Every test had to serialize on a
//     `resetAuthState()` helper whose whole job was to undo the previous test's
//     global writes.
//
// The fix is to make the dependency set a *value* the caller passes in and to
// construct the Router once from it. Order dependence disappears (there is no
// "before SetupRouter" any more — there is only "here is the Router"), multiple
// instances become ordinary (each holds its own fields), and tests become
// parallel-safe because each test builds its own Router.
//
// Scope note: this covers **the router package's own globals**. The `controller`
// package has its own `Init*` globals (InitFileController, InitNodeDirectory, …)
// which NewRouter still calls; those are a separate, larger change (see
// serverapp's assembly note) and are deliberately left alone here.
package router

import (
	"fmt"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/config"
	"peerdrive/internal/service"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
)

// Deps is everything the router needs from the assembly layer.
//
// The zero value is not usable (Cfg is required, but NewRouter promotes nil to a
// default Config), and every optional field has an explicit "nil means that
// feature is off" meaning — exactly the meaning the old `Set*(nil)` calls had.
// So moving from setters to Deps does not change which routes get registered for
// any given configuration.
//
// A Deps value is copied into the Router at construction and never mutated
// afterwards, so a Router is safe to read from many goroutines.
type Deps struct {
	// Cfg is the node configuration. It drives middleware (rate limit, CSP, CORS
	// whitelist, trusted proxies), the Swagger switch, the storage dir put into
	// the request context, and Auth's reg-server/admin-token pair.
	Cfg *config.Config

	// PeerJSService is the WebRTC node service. nil → the /peerjs/*, /ws/peer and
	// /peerjs/fetch routes are not registered at all (they cannot work without a
	// signaling connection). Same meaning as SetPeerJSService(nil).
	PeerJSService *transport.PeerJSService

	// PeerJSCfg is the config used for the WS local-session Origin whitelist
	// (/ws/peer CheckOrigin) and for the signal_* hints returned by /peerjs/node.
	//
	// Why is this not just Deps.Cfg? Because a test wants a router whose Origin
	// policy is under test without reproducing an entire Config. When nil,
	// NewRouter falls back to Cfg, which is what the assembly layer always did.
	PeerJSCfg *config.Config

	// NodeDirectory is the node marketplace directory (doc/NETDISK.md M1).
	// nil → the marketplace endpoints are not registered.
	NodeDirectory *service.NodeDirectory

	// NodeShare is the runtime share scope (doc/NETDISK.md M2). nil → /peerjs/share*
	// is not registered. Sharing state is operator-controlled at runtime, so this
	// must be the *same* instance the transport layer uses to answer `share`
	// frames — see serverapp's assembly comment.
	NodeShare *service.NodeShare

	// PeerPuller is the cross-node pull-and-save service (doc/NETDISK.md M3).
	// nil → /p2p/pull* is not registered.
	PeerPuller *service.PeerPuller

	// SourceManager is the unified multi-protocol source manager. nil → the
	// /sources/* management endpoints are not registered at all, and the BT/IPFS
	// controls are not wired into it. Same meaning as SetSourceManager(nil).
	SourceManager *source.Manager
}

// Router owns one fully-assembled HTTP surface plus the auth state that goes
// with it. Construct it with NewRouter; do not build the fields by hand.
//
// Each Router is independent: two Routers in the same process have separate
// engines, separate rate limiters, separate token caches and separate config.
// That is the property the package-level globals could not offer, and the one the
// tests in router_multi_instance_test.go pin down.
type Router struct {
	deps Deps
	cfg  *config.Config

	// auth holds this instance's auth backend choice and token cache.
	auth *Authenticator

	// authOptional/authRequired are the two middleware closures, built once in
	// NewRouter so they capture `auth` rather than reading globals at request
	// time.
	authOptional gin.HandlerFunc
	authRequired gin.HandlerFunc

	// engine is the assembled gin engine, created by Engine().
	engine *gin.Engine
}

// NewRouter builds a Router from deps.
//
// It validates up front that the dependency set is coherent and returns an error
// rather than registering a half-working engine. Previously a misconfigured wiring
// produced a server that started fine and then 404'd (or, for auth, silently ran
// with auth disabled).
//
// A nil Deps.Cfg is promoted to a default Config here rather than rejected: a
// zero Config is a legitimate description of a default node, and tests want it.
func NewRouter(deps Deps) (*Router, error) {
	if deps.Cfg == nil {
		deps.Cfg = &config.Config{}
	}
	if deps.PeerJSCfg == nil {
		// Fall back to the node config: that is what the assembly layer always
		// passed (SetPeerJSConfig(cfg) sat right next to SetPeerJSService(cfg)).
		deps.PeerJSCfg = deps.Cfg
	}
	if err := validate(deps); err != nil {
		return nil, err
	}
	auth := NewAuthenticator(deps.Cfg.RegistrationServer, deps.Cfg.AdminToken)
	r := &Router{
		deps: deps,
		cfg:  deps.Cfg,
		auth: auth,
	}
	r.authOptional = auth.AuthOptional()
	r.authRequired = auth.AuthRequired()
	return r, nil
}

// Deps returns the dependency set this Router was built from. Returning it by
// value (rather than exposing the fields) keeps Deps immutable from the outside.
func (r *Router) Deps() Deps { return r.deps }

// AuthRequired returns this instance's "reject without a valid token" middleware.
// Exposed so a caller that mounts extra routes (there are none today) gets the
// same auth semantics instead of reaching for a package-level constructor.
func (r *Router) AuthRequired() gin.HandlerFunc { return r.authRequired }

// AuthOptional returns this instance's "validate a token if present" middleware.
func (r *Router) AuthOptional() gin.HandlerFunc { return r.authOptional }

// validate checks the dependency set for combinations that cannot serve their
// documented purpose.
//
// The nil feature-off combinations are deliberately NOT errors: they are how
// "PeerJS disabled" and "no login service" are expressed, and rejecting them would
// make the common minimal deployment fail to start.
func validate(d Deps) error {
	// PeerJSService nil means "don't register the PeerJS routes" — consistent with
	// the old code, and the safe default.
	//
	// What is worth catching is the inverse: a peer-backed dependency handed over
	// with no peer transport to back it. app.go only builds these when PeerJS is
	// enabled, so this cannot happen there, but a test or a future caller could,
	// and the symptom would be a nil-pointer deep inside a request handler.
	if d.PeerJSService == nil {
		for _, dep := range []struct {
			name string
			set  bool
		}{
			{"SourceManager", d.SourceManager != nil},
			{"NodeDirectory", d.NodeDirectory != nil},
			{"NodeShare", d.NodeShare != nil},
			{"PeerPuller", d.PeerPuller != nil},
		} {
			if dep.set {
				return fmt.Errorf("router: Deps.%s requires Deps.PeerJSService "+
					"(it is built on the peer transport and cannot work without it)", dep.name)
			}
		}
	}
	return nil
}
