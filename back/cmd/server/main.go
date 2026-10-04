// Peerdrive server entry point.
// Starts the Gin HTTP server, initializes the SQLite metadata database, content-addressable
// file storage, and PeerJS signaling + WebRTC file service (the Go node acts as a persistent
// peer providing files). Supports PORT (listen port) and PEERDRIVE_STORAGE (storage directory,
// default ./storage) environment variables.
// Usage (must use -tags nosqlite due to dual SQLite driver CGO symbol conflict):
//   go run -tags nosqlite ./cmd/server/main.go
//   PORT=3000 PEERDRIVE_STORAGE=./storage go run -tags nosqlite ./cmd/server/main.go
// Internal flow: InitDB → PeerJSService.Start → register local/peer/url source → SetupRouter
//
// storageDir is injected into the Gin Context for use by controller/anon.go etc.
// Historical note: the original libp2p interconnection layer was entirely removed on
// 2026-08-16 (see doc/archive/LEGACY.md §A); this comment previously described the old
// "libp2p P2P node / NewP2PService→IPFSService→UniversalDownloader" flow, which is no
// longer accurate. Corrected here to reflect the current flow.

package main

import (
	"context"
	"errors"
	"fmt"
	stdlog "log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	_ "peerdrive/docs"
	"peerdrive/internal/config"
	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
)

// @title Peerdrive API
// @version 1.0
// @description P2P file sharing with content-addressable storage, collection management, versioning, merge/fork/pull.
// @host localhost:3000
// @BasePath /

// main is the Peerdrive server entry point; it initializes the DB, P2P, HTTP router and listens on the port.
func main() {
	log.LogInfo("main: Peerdrive server starting")

	cfg := config.Load()
	// Startup-time validation: misconfigured environment variables won't crash the process,
	// they just cause subtle misbehavior (files written to the wrong location, port not
	// binding), and by the time you notice it's too late. Here we fail fast with a clear
	// message (see config.Validate for details).
	if err := config.Validate(cfg); err != nil {
		stdlog.Fatalf("%v", err)
	}
	storageDir := cfg.StorageDir
	log.LogInfo("main: config loaded, storageDir=%s, port=%s", storageDir, cfg.Port)

	// share is built inside the PeerJS block below but also read by logSecuritySummary
	// afterwards (whether external sharing is on). Declared at function scope so the
	// summary can see it; nil when PeerJS is disabled, which the summary treats as "not sharing".
	var share *service.NodeShare

	// Reject "volume root" configuration at startup (doc/NETDISK.md §11.3).
	//
	// pathutil.Within is a pure containment check: when root is configured as `/`
	// (or `C:\` on Windows), it will naturally allow `/etc/passwd` — and that's
	// not a bug, that's literally what the config says. But almost no one actually
	// wants to share an entire drive; values like `PEERDRIVE_STORAGE=/` are almost
	// always mistakes (commonly caused by an unexpanded env var). Rather than
	// letting the node silently allow the whole drive, we refuse at startup.
	// Operators who truly need this can set PEERDRIVE_ALLOW_UNSAFE_ROOT=1.
	if err := checkUnsafeRoots(cfg); err != nil {
		stdlog.Fatalf("%v", err)
	}
	warnUnsupportedRoots(cfg)

	// Initialize the DB (with migrations). The path comes from config rather than being
	// hardcoded: changing the deployment directory or putting the DB on a different
	// disk is a common ops action, and hardcoding "./peerdrive.db" forces operators
	// to rely on "remember to cd to the right directory" as a safety net.
	log.LogInfo("main: initializing database at %s", cfg.DBPath)
	if err := repository.InitDB(cfg.DBPath); err != nil {
		stdlog.Fatalf("database initialization failed: %v", err)
	}
	defer func() {
		if err := repository.CloseDB(); err != nil {
			log.LogWarn("main: close db: %v", err)
		}
	}()
	log.LogInfo("main: database initialized")

	// Initialize the anonymous storage directory (same directory as regular files)
	repository.SetAnonStorageDir(storageDir)

	// Initialize PeerJS signaling + WebRTC file service (the Go node acts as a persistent
	// peer providing files, interconnecting with browsers and other nodes via PeerJS signaling).
	// The default signaling server is the public cloud 0.peerjs.com; in production, set
	// PEERDRIVE_PEERJS_HOST/KEY to point to a self-hosted peerserver (see doc/PEERSIGNAL.md
	// / AGENTS.md for deployment).
	// Note: SetPeerJSService must be called before SetupRouter; it's read during route registration.
	var peerjsSvc *transport.PeerJSService
	if cfg.PeerJSEnable {
		log.LogInfo("main: initializing PeerJS WebRTC service")
		peerjsSvc = transport.NewPeerJSService(cfg, storageDir)

		// The **readable** root directories (distinct from "writable/registered" roots,
		// see AddReadRoot for details):
		//   - storage root: files registered via the HTTP API (register_local/folder) usually land here;
		//   - PEERDRIVE_SHARE_DIRS: directories the operator declares for sharing, which may be on any mount point.
		// Without this step, when a shared directory is not under the download directory, the
		// listing shows up but the peer gets "read failed" — the registration side allowed it,
		// but the read side considered it out-of-bounds and fell back to a content-addressed copy
		// that doesn't exist.
		peerjsSvc.FileIndex().AddReadRoot(storageDir)
		for _, d := range pathutil.SplitList(cfg.ShareDirs) {
			peerjsSvc.FileIndex().AddReadRoot(d)
		}

		peerjsSvc.Start()
		defer peerjsSvc.Close()
		log.LogInfo("main: PeerJS node id=%s", peerjsSvc.ID())
	}

	// Node market directory (doc/NETDISK.md M1): market list = online nodes from discovery server ∪ joined list.
	// Injection order matters: SetExtraPeers makes "nodes joined in the market" auto-dial on every
	// signaling reconnection (same status as PEERDRIVE_PEERJS_PEERS); SetNodeDirectory must be
	// called before SetupRouter (read during route registration).
	if peerjsSvc != nil {
		nodeDir := service.NewNodeDirectory(storageDir, cfg.DiscoverURL)
		nodeDir.SetSelfID(peerjsSvc.ID)
		nodeDir.SetConnected(peerjsSvc.ConnectedPeerIDs)
		nodeDir.SetDial(peerjsSvc.EnsureConnection)
		peerjsSvc.SetExtraPeers(nodeDir.JoinedPeerIDs)
		router.SetNodeDirectory(nodeDir)
		log.LogInfo("main: node directory ready (joined=%d)", len(nodeDir.JoinedPeerIDs()))

		// Node sharing scope (doc/NETDISK.md M2): data source for share frames + announce summary.
		// AnonService is a stateless read service (only holds cfg); here we create a separate instance
		// exclusively for sharing resolution, without sharing state with the router's internal instance
		// (and sharing state isn't needed anyway).
		// storageDir is also passed in: the sharing scope is **runtime-mutable** (admin panel
		// checkbox / PUT /peerjs/share), stored in storageDir/share_scope.json; the env var is
		// only the initial value at first startup (see service.NodeShare header comment).
		share = service.NewNodeShare(cfg, storageDir)
		// Runtime-added share directories must be registered as readable roots, otherwise you get
		// "listing shows up but read failed" (the read side considers it out-of-bounds). The batch
		// added at startup is registered by the AddReadRoot loop above; here we only cover those
		// added after startup.
		share.SetDirHook(func(dirs []string) {
			for _, d := range dirs {
				peerjsSvc.FileIndex().AddReadRoot(d)
			}
		})
		anonReader := service.NewAnonService(cfg)
		share.SetAnonAccess(anonReader.GetCollectionByHash, anonReader.ListCollections)
		// File sharing filters by directory prefix only; List is internally capped at 1000
		// (repository layer clamps); when the sharing list exceeds 1000 files, only the first 1000
		// by sequence order are taken — enough for market display and selection; the real bulk
		// pull goes through collections (doesn't depend on this list).
		share.SetFileLister(func() ([]transport.FileInfo, error) {
			return peerjsSvc.FileIndex().List(0, 1000)
		})
		// Hash-based single lookup: when the index exceeds 1000 entries, checked files rely on
		// this as a fallback (otherwise "I checked it but it didn't take effect"). See service.NodeShare.resolveFiles.
		share.SetFileInfoReader(peerjsSvc.FileIndex().Info)
		// SnapshotFor carries the requester id: friends can see private entries (otherwise
		// permissions are granted but the directory is hidden). Share frames go over an established
		// connection, so the peer id is known.
		peerjsSvc.SetShareProvider(share.SnapshotFor)
		// Download gate: private content is only served to friends and self (req frames, see ShareGate for details).
		peerjsSvc.SetShareGate(share)
		nodeDir.SetShareSummary(share.Summary)
		// Admin endpoints /peerjs/share* (GET/PUT/POST files): let the operator toggle what
		// to share from the admin panel without restarting the node to change env vars.
		router.SetNodeShare(share)

		// Cross-node pull-save (doc/NETDISK.md M3): peer content → local disk + registration.
		// downloadRoot must be an allowed root in file_index (cfg.DownloadDir); otherwise
		// registration will be rejected by the H2 security boundary ("path outside allowed root").
		puller := service.NewPeerPuller(cfg.DownloadDir)
		puller.SetSource(peerjsSvc)
		puller.SetFileAccess(
			func(hash string) bool {
				fi, err := peerjsSvc.FileIndex().Info(hash)
				return err == nil && fi != nil && fi.Path != "" && fi.Size > 0
			},
			func(path string) (string, int64, error) {
				fi, err := peerjsSvc.FileIndex().Create(path)
				if err != nil {
					return "", 0, err
				}
				return fi.Hash, fi.Size, nil
			},
		)
		router.SetPeerPuller(puller)
	}

	// Set up routes (internally injects storageDir/downloader into context)
	if cfg.RegistrationServer != "" {
		router.SetRegServer(cfg.RegistrationServer)
	}
	if peerjsSvc != nil {
		router.SetPeerJSService(peerjsSvc)
		router.SetPeerJSConfig(cfg)
		// Port-forwarding authorization rules (forward v2): PEERDRIVE_FORWARD_RULES="key:port,key2:port2".
		// key is the credential (the server uses the raw text for HMAC verification) — configure as a
		// sensitive file, recommended chmod 600.
		if cfg.ForwardRules != "" {
			rules := map[string][]int{}
			for _, pair := range strings.Split(cfg.ForwardRules, ",") {
				kv := strings.SplitN(pair, ":", 2)
				if len(kv) != 2 || kv[0] == "" {
					log.LogWarn("main: ignore bad forward rule %q", pair)
					continue
				}
				port, err := strconv.Atoi(kv[1])
				if err != nil || port <= 0 || port > 65535 {
					log.LogWarn("main: ignore bad forward rule port %q", pair)
					continue
				}
				rules[kv[0]] = append(rules[kv[0]], port)
			}
			if len(rules) > 0 {
				peerjsSvc.SetForwardRules(rules)
				log.LogInfo("main: forward rules loaded (%d keys)", len(rules))
			}
		}
		// Unified source system assembly: local disk (file_index + CAS) → P2P passthrough → URL source.
		// Route semantics: local hit returns immediately; on miss, fall back to peer; URL source is
		// registered via template (PEERDRIVE_URL_SOURCE_TEMPLATE) and may be empty. Admin GET /sources.
		mgr := source.New()
		if err := mgr.Register(source.NewLocalSource(storageDir, peerjsSvc.FileIndex())); err != nil {
			log.LogWarn("main: register local source: %v", err)
		}
		if err := mgr.Register(source.NewPeerSource(peerjsSvc)); err != nil {
			log.LogWarn("main: register peer source: %v", err)
		}
		if cfg.URLSourceTemplate != "" {
			if err := mgr.Register(source.NewURLSource(cfg.URLSourceTemplate, nil)); err != nil {
				log.LogWarn("main: register url source: %v", err)
			}
		}
		router.SetSourceManager(mgr)
		// serveFile multi-source routing (3rd optimization on 2026-08-18): when a peer req
		// misses locally, fall back to the peer/URL template (loop prevention via dcReq.Trace).
		// HTTP download root requests already use mgr; here we reuse the same instance to keep
		// routing order consistent.
		peerjsSvc.SetFileRouter(mgr)
	}
	log.LogInfo("main: setting up HTTP router")
	r := router.SetupRouter(cfg)

	// Security status summary (security_status.go): print "where exactly is this node open"
	// after the router is assembled. Read-only, doesn't change any default.
	// share is the NodeShare built in the PeerJS block above; nil when PeerJS is disabled,
	// which the summary itself treats as "sharing not enabled".
	logSecuritySummary(cfg, share)

	// Listen address: PEERDRIVE_HOST empty = listen on all interfaces (historical behavior).
	// The admin surface has no account system; "who can reach this port" is its only boundary.
	// If the admin panel is only used locally, setting PEERDRIVE_HOST=127.0.0.1 is the
	// cheapest wall.
	addr := net.JoinHostPort(cfg.Host, cfg.Port)

	// Using an explicit http.Server instead of r.Run():
	//   1. r.Run() calls Fatalf internally, which does os.Exit on error — all main defers
	//      are skipped, and PeerJS connections and DB handles are left to the process exit
	//      to clean up (on Windows, DB file handles don't close and may prevent opening
	//      the next time);
	//   2. No graceful shutdown: on SIGTERM it just exits, leaving in-progress large file
	//      transfers truncated mid-way — the peer gets a corrupt partial file and thinks
	//      the transfer succeeded.
	// ReadHeaderTimeout is the minimum Slowloris protection (gin's default r.Run() doesn't set it).
	srv := &http.Server{
		Addr:              addr,
		Handler:           r,
		ReadHeaderTimeout: 15 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.LogInfo("main: starting HTTP server on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		log.LogError("main: HTTP server failed: %v", err)
	case <-quit:
		log.LogInfo("main: shutting down server")
	}

	// Graceful shutdown: first stop accepting new requests, then give in-progress requests
	// some time to finish (whether the half-written file from an in-progress pull/upload
	// can complete depends on this 20-second window).
	// After timeout, force-close — we can't hold the shutdown open indefinitely for one slow
	// request (container orchestrators will SIGKILL).
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.LogWarn("main: graceful shutdown timed out, forcing close: %v", err)
		if err := srv.Close(); err != nil {
			log.LogWarn("main: force close: %v", err)
		}
	}
	log.LogInfo("main: stopped")
}

// checkUnsafeRoots checks whether any directory was configured as a volume root (`/`, `C:\`).
//
// Why a separate check: such configs **pass** all runtime boundary checks (when root=`/`,
// `/etc/passwd` is indeed "inside the root", the check is correct), so they can only be
// blocked at startup based on configuration intent.
// Covers three entry points: storage (HTTP registration + anonymous upload landing),
// download (peer writes), share dirs (external sharing list, may be on any mount point).
//
// Escape hatch: PEERDRIVE_ALLOW_UNSAFE_ROOT=1 (when you really want to run an entire drive as storage).
// configuredDirs returns all config-specified directories and their source env var names.
func configuredDirs(cfg *config.Config) []struct {
	name string
	val  string
} {
	candidates := []struct {
		name string
		val  string
	}{
		{"PEERDRIVE_STORAGE", cfg.StorageDir},
		{"PEERDRIVE_DOWNLOAD_DIR", cfg.DownloadDir},
	}
	for i, d := range pathutil.SplitList(cfg.ShareDirs) {
		candidates = append(candidates, struct {
			name string
			val  string
		}{fmt.Sprintf("PEERDRIVE_SHARE_DIRS[%d]", i), d})
	}
	return candidates
}

func checkUnsafeRoots(cfg *config.Config) error {
	if os.Getenv("PEERDRIVE_ALLOW_UNSAFE_ROOT") == "1" {
		log.LogWarn("main: PEERDRIVE_ALLOW_UNSAFE_ROOT=1, skipping volume-root configuration check")
		return nil
	}
	var bad []string
	for _, c := range configuredDirs(cfg) {
		if pathutil.IsUnsafeRoot(c.val) {
			bad = append(bad, fmt.Sprintf("%s=%q", c.name, c.val))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	return fmt.Errorf("refusing to start: a directory was configured as a filesystem volume root (%s). This would make all files under it readable/writable externally. "+
		"Please change it to a specific subdirectory. To confirm and run anyway, set PEERDRIVE_ALLOW_UNSAFE_ROOT=1",
		strings.Join(bad, ", "))
}

// warnUnsupportedRoots is a startup self-check: whether these directories can establish a
// secure boundary via os.Root.
//
// Why say it early: when os.Root fails, the symptom is subtle — **files in that directory
// simply can't be shared** — and the log only shows an errno, so operators will chase
// unrelated issues (chmod/chown/path reconfiguration, all the wrong direction). Here we
// clearly report the conclusion and next steps for each directory at startup.
func warnUnsupportedRoots(cfg *config.Config) {
	for _, c := range configuredDirs(cfg) {
		if strings.TrimSpace(c.val) == "" {
			continue
		}
		if err := pathutil.ProbeRootSupport(c.val); err != nil {
			// The directory not existing on first startup is normal — don't report it as "misconfigured"
			if errors.Is(err, os.ErrNotExist) {
				log.LogInfo("main: %s=%s does not exist yet, will be created automatically on first write", c.name, c.val)
				continue
			}
			log.LogWarn("main: %s=%s cannot be used as a secure root directory: %s",
				c.name, c.val, pathutil.ExplainRootFailure(c.val, err))
		}
	}
}
