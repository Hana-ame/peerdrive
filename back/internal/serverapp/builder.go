package serverapp

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"peerdrive/internal/config"
	"peerdrive/internal/echproxy"
	"peerdrive/internal/echproxy/exhentai"
	"peerdrive/internal/echproxy/twimg"
	"peerdrive/internal/extractor"
	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"
	"peerdrive/internal/source"
	"peerdrive/internal/transport"
	"peerdrive/internal/twitterpic"
)

func buildRouter(cfg *config.Config) (http.Handler, func(), RouterInfo, error) {
	var shutdowns []func()
	shutdown := func() {
		// 后注册的先关：PeerJS 依赖 DB，关的时候按相反顺序。
		for i := len(shutdowns) - 1; i >= 0; i-- {
			shutdowns[i]()
		}
	}
	fail := func(err error) (http.Handler, func(), RouterInfo, error) {
		shutdown()
		return nil, nil, RouterInfo{}, err
	}
	storageDir := cfg.StorageDir
	log.LogInfo("main: config loaded, storageDir=%s, port=%s", storageDir, cfg.Port)

	// share is built inside the PeerJS block below but also read by logSecuritySummary
	// afterwards (whether external sharing is on). Declared at function scope so the
	// summary can see it; nil when PeerJS is disabled, which the summary treats as "not sharing".
	var share *service.NodeShare

	// deps is the router's dependency set, filled in as each module is built below
	// and handed to router.NewRouter in one piece.
	//
	// This is the whole point of the 2026-10 refactor: previously each module was
	// pushed into the router package with a `router.SetXxx(...)` call as it was
	// built, and the router read those package globals back while registering routes.
	// That made the wiring order load-bearing and unenforceable by the compiler.
	// Collecting into one value means "everything the router needs" is visible in
	// one place, and NewRouter validates it before any route is registered.
	deps := router.Deps{Cfg: cfg}

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
		return fail(err)
	}
	warnUnsupportedRoots(cfg)

	// Initialize the DB (with migrations). The path comes from config rather than being
	// hardcoded: changing the deployment directory or putting the DB on a different
	// disk is a common ops action, and hardcoding "./peerdrive.db" forces operators
	// to rely on "remember to cd to the right directory" as a safety net.
	log.LogInfo("main: initializing database at %s", cfg.DBPath)
	if err := repository.InitDB(cfg.DBPath); err != nil {
		return fail(fmt.Errorf("database initialization failed: %w", err))
	}
	shutdowns = append(shutdowns, func() {
		if err := repository.CloseDB(); err != nil {
			log.LogWarn("main: close db: %v", err)
		}
	})
	log.LogInfo("main: database initialized")

	// Initialize the anonymous storage directory (same directory as regular files)
	repository.SetAnonStorageDir(storageDir)

	// Initialize PeerJS signaling + WebRTC file service (the Go node acts as a persistent
	// peer providing files, interconnecting with browsers and other nodes via PeerJS signaling).
	// The default signaling server is the public cloud 0.peerjs.com; in production, set
	// PEERDRIVE_PEERJS_HOST/KEY to point to a self-hosted peerserver (see doc/PEERSIGNAL.md
	// / AGENTS.md for deployment).
	// Note: peerjsSvc is handed to the router below as a constructor argument
	// (router.Deps.PeerJSService), not injected into the router package beforehand.
	// The old "SetPeerJSService must be called before SetupRouter" constraint is gone:
	// there is no longer a window in which the router could observe a half-wired
	// dependency set.
	var peerjsSvc *transport.PeerJSService
	// urlClient 提升到函数作用域（原来在 ech-proxy 块内声明）：twitter-pic
	// 模块在 PeerJS 块外装配，也要能复用 ech-proxy/exhentai 可能产生的出口
	// client（其 Transport 走 ech-proxy 出口）。nil = 直连默认 client。
	var urlClient *http.Client
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
		shutdowns = append(shutdowns, func() { peerjsSvc.Close() })
		log.LogInfo("main: PeerJS node id=%s", peerjsSvc.ID())
	}

	// Node market directory (doc/NETDISK.md M1): market list = online nodes from discovery server ∪ joined list.
	// SetExtraPeers makes "nodes joined in the market" auto-dial on every
	// signaling reconnection (same status as PEERDRIVE_PEERJS_PEERS).
	//
	// The directory is collected into the router's Deps below; it used to be pushed
	// in with router.SetNodeDirectory before SetupRouter, which carried the same
	// "must be called before route registration" ordering constraint that the
	// constructor form removes.
	if peerjsSvc != nil {
		nodeDir := service.NewNodeDirectory(storageDir, cfg.DiscoverURL)
		nodeDir.SetSelfID(peerjsSvc.ID)
		nodeDir.SetConnected(peerjsSvc.ConnectedPeerIDs)
		nodeDir.SetDial(peerjsSvc.EnsureConnection)
		peerjsSvc.SetExtraPeers(nodeDir.JoinedPeerIDs)
		deps.NodeDirectory = nodeDir
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
		_, _ = anonReader.SeedSampleCollectionIfEmpty(storageDir, func(dir string) {
			if peerjsSvc != nil {
				peerjsSvc.FileIndex().AddReadRoot(dir)
			}
		})
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
		peerjsSvc.SetShareProviderWithToken(share.SnapshotForToken)
		// Download gate: private content is only served to friends and self (req frames, see ShareGate for details).
		peerjsSvc.SetShareGate(share)
		nodeDir.SetShareSummary(share.Summary)
		// Admin endpoints /peerjs/share* (GET/PUT/POST files): let the operator toggle what
		// to share from the admin panel without restarting the node to change env vars.
		deps.NodeShare = share

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
		// 拉取后自动解包（可选，默认关，PEERDRIVE_AUTO_EXTRACT）。开时把压缩包
		// 解到 <target>_extracted/ 并逐个登记进 file_index；安全边界（大小/
		// 压缩比/文件数/路径穿越）由 extractor 包强制执行。
		if cfg.AutoExtract {
			ex := extractor.New(extractor.Config{
				Enabled:        true,
				MaxSize:        cfg.AutoExtractMaxSize,
				MaxRatio:       cfg.AutoExtractMaxRatio,
				MaxFiles:       cfg.AutoExtractMaxFiles,
				DeleteOriginal: cfg.AutoExtractDeleteOrig,
			})
			puller.SetExtractor(ex)
			log.LogInfo("main: auto-extract enabled (max_size=%d max_ratio=%d max_files=%d delete_orig=%v)",
				cfg.AutoExtractMaxSize, cfg.AutoExtractMaxRatio, cfg.AutoExtractMaxFiles, cfg.AutoExtractDeleteOrig)
		}
		puller.RecoverIncompleteTasks()
		deps.PeerPuller = puller
	}

	// ── iwara.tv via ech-proxy (optional module, PEERDRIVE_IWARA_ENABLE) ──
	// When enabled, downloads the ech-proxy Windows executable, verifies it
	// against the published SHA256, starts it as a child process, and creates
	// an IwaraClient that routes API calls through the local TLS proxy.
	// Default off: the module runs an external process and injects a user
	// cookie into third-party requests — both are risky operations that must
	// be explicitly opted into.
	if cfg.IwaraEnable {
		iwaraCfg := &echproxy.ModuleConfig{
			Enable:          true,
			IWARACookie:     cfg.IwaraCookie,
			EchProxyDir:     "echproxy",
			EchProxyExeName: echproxy.DefaultEchProxyExeName,
			EchProxyVersion: cfg.IwaraEchProxyVersion,
			EntrySuffix:     cfg.IwaraEntrySuffix,
			UpstreamSuffix:  cfg.IwaraUpstreamSuffix,
			EntryPort:       strconv.Itoa(cfg.IwaraEchProxyPort),
			EntrySkipTLS:    cfg.IwaraEntrySkipTLS,
		}
		iwaraCfg.Normalize()

		// TLS verification of the iwara ech-proxy entry (PEERDRIVE_IWARA_ENTRY_SKIP_TLS,
		// default true = today's behaviour). Log it either way so the security posture is
		// visible in startup output instead of being implied by an absent warning.
		if iwaraCfg.EntrySkipTLS {
			log.LogWarn("main: iwara ech-proxy entry TLS verification is DISABLED — %s serves a self-signed cert "+
				"(loopback only, so no network exposure). Set PEERDRIVE_IWARA_ENTRY_SKIP_TLS=false to require a "+
				"chain-issued entry cert", iwaraCfg.ListenAddr)
		} else {
			log.LogInfo("main: iwara ech-proxy entry TLS verification ENABLED (chain enforced for %s)",
				iwaraCfg.ListenAddr)
		}

		pm := echproxy.NewProcessManager(iwaraCfg)
		client := echproxy.NewIwaraClient(iwaraCfg)
		// Expose the client to the HTTP surface (GET /iwara/video/:id for the
		// front/iwara page). The route is optional in router.Deps, so a default
		// node without PEERDRIVE_IWARA_ENABLE never advertises it.
		deps.IwaraClient = client

		// Attempt to start ech-proxy. If it fails (non-Windows, port conflict,
		// download failure), the IwaraClient still works for direct API access
		// (without ech-proxy URL rewriting).
		if err := pm.Start(context.Background()); err != nil {
			log.LogWarn("main: ech-proxy not started (%v); iwara direct API access still available", err)
		} else {
			// Route iwara.tv API calls through the local ech-proxy.
			rewriter := echproxy.RewriterFromConfig(iwaraCfg)
			client.SetRewriter(&rewriter)
			log.LogInfo("main: iwara/ech-proxy enabled (version=%s port=%d entry_suffix=%s)",
				cfg.IwaraEchProxyVersion, cfg.IwaraEchProxyPort, cfg.IwaraEntrySuffix)
			defer pm.Stop()
		}
	}

	// BT DHT auto-enable (PEERDRIVE_DISCOVER_MODE fallback): when no discovery server is
	// configured (mode=off or mode=peerjs), auto-enable BT DHT for **file discovery** (BEP-44).
	// Note: BT DHT is file discovery, not node discovery. Node interconnection still relies on
	// PEERDRIVE_PEERJS_PEERS static list. This fallback ensures the node can still find files
	// on the BitTorrent DHT network even without a discovery server.
	//
	// We check os.LookupEnv to distinguish "not set" from "explicitly set to false":
	// getEnvBool returns the default (false) for both cases, so we need the raw env check.
	if cfg.BTDHTEnabled == false {
		if _, explicit := os.LookupEnv("PEERDRIVE_BT_DHT_ENABLE"); !explicit {
			mode := cfg.DiscoverMode
			if mode == "off" || mode == "peerjs" {
				cfg.BTDHTEnabled = true
				log.LogInfo("main: BT DHT auto-enabled (file discovery fallback; discovery mode=%s). "+
					"Note: BT DHT is for file discovery, not node discovery - node interconnect "+
					"still relies on PEERDRIVE_PEERJS_PEERS static list", mode)
			}
		}
	}

	// Discovery configuration summary (PEERDRIVE_DISCOVER_MODE):
	// Print current discovery mode and its concrete URL/broker at startup.
	{
		mode := cfg.DiscoverMode
		if mode == "" {
			mode = "auto"
		}
		switch mode {
		case "off", "peerjs":
			log.LogInfo("main: discovery mode=%s (no HTTP/MQTT discovery; nodes connect via PEERDRIVE_PEERJS_PEERS only)", mode)
		case "discover":
			log.LogInfo("main: discovery mode=discover, url=%s", cfg.DiscoverURL)
		case "mqtt":
			log.LogInfo("main: discovery mode=mqtt, broker=%s", cfg.MQTTBroker)
		case "auto":
			if cfg.DiscoverURL != "" {
				log.LogInfo("main: discovery mode=auto, using HTTP url=%s", cfg.DiscoverURL)
			} else if cfg.MQTTEnable {
				log.LogInfo("main: discovery mode=auto, using MQTT broker=%s", cfg.MQTTBroker)
			} else {
				log.LogInfo("main: discovery mode=auto, no discovery server configured (no HTTP/MQTT discovery)")
			}
		default:
			log.LogWarn("main: unknown discovery mode=%s, falling back to auto", mode)
		}
	}

	// Set up routes (internally injects storageDir/downloader into context)
	//
	// Auth configuration is not passed separately any more: NewRouter derives its
	// Authenticator from Deps.Cfg (RegistrationServer / AdminToken). The old
	// `router.SetRegServer(cfg.RegistrationServer)` call is gone, and with it the
	// possibility of a registration server being configured after the auth
	// middleware had already been built.
	if peerjsSvc != nil {
		deps.PeerJSService = peerjsSvc
		deps.PeerJSCfg = cfg
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

		// Optional ech-proxy module (PEERDRIVE_ECH_PROXY_ENABLE, default off). It downloads
		// and runs ech-proxy as a child process, then routes pbs.twimg.com/<path>?<query>
		// through the local entry host https://twimg-pbs.l.moonchan.xyz:8443/<path>?<query>.
		// When the flag is off this whole block is skipped, so the URL source keeps the exact
		// nil-client (http.DefaultClient) path it has always had — the disabled case is a
		// byte-identical no-op.
		//
		// There is deliberately no silent fallback to direct pbs.twimg.com traffic: if the
		// download, the checksum, the port bind or the spawn fails, startup fails. An
		// operator who opted into the module must be told it did not come up.
		if cfg.ECHProxyEnable {
			if cfg.URLSourceTemplate == "" {
				log.LogWarn("main: PEERDRIVE_ECH_PROXY_ENABLE=true but PEERDRIVE_URL_SOURCE_TEMPLATE is " +
					"empty — the rewrite has no URL source to serve, ech-proxy was not started")
			} else {
				mod, err := buildECHProxyModule(cfg)
				if err != nil {
					return fail(fmt.Errorf("ech-proxy module: %w", err))
				}
				if err := mod.Start(context.Background()); err != nil {
					return fail(fmt.Errorf("ech-proxy: %w", err))
				}
				// Stop after the DB/PeerJS shutdown hooks: the child is not depended on by
				// anything else, but keeping it inside the same ordered list keeps the log
				// readable (last-registered runs first).
				shutdowns = append(shutdowns, func() {
					if err := mod.Stop(context.Background()); err != nil {
						log.LogWarn("main: stop ech-proxy: %v", err)
					}
				})
				urlClient = mod.Client()
				log.LogInfo("main: ech-proxy enabled — %s → %s://%s:%s via %s",
					twimg.DefaultSrcHost, "https", mod.Entry().EntryHost, mod.Entry().Port, mod.Addr())
				// Same trust model as the iwara module above: the entry is a loopback-only
				// child process with a self-signed cert, so skipping verification is the
				// default (PEERDRIVE_ECH_PROXY_SKIP_TLS). Say so at startup either way.
				if mod.Entry().SkipTLS {
					log.LogWarn("main: ech-proxy entry TLS verification is DISABLED — %s serves a self-signed "+
						"cert (loopback only, so no network exposure). Set PEERDRIVE_ECH_PROXY_SKIP_TLS=false to "+
						"require a chain-issued entry cert", mod.Addr())
				} else {
					log.LogInfo("main: ech-proxy entry TLS verification ENABLED (chain enforced for %s)",
						mod.Addr())
				}
			}
		}

		// Optional ExHentai module (PEERDRIVE_EXHENTA_ENABLE, default off).
		//
		// Unlike the two ech-proxy modules above there is no subprocess and no
		// local listener: the backend host is reached directly, so the module is
		// a pure http.RoundTripper that rewrites the authority of matching URLs.
		// It stacks on top of whatever urlClient the ech-proxy module produced —
		// the two modules serve different hosts and do not conflict.
		//
		// When the flag is off this whole block is skipped, so a node that never
		// enables it stays byte-identical to one without the module: nothing is
		// constructed and urlClient is still nil when the URL source is built.
		if cfg.ExhentaiEnable {
			if cfg.URLSourceTemplate == "" {
				log.LogWarn("main: PEERDRIVE_EXHENTA_ENABLE=true but PEERDRIVE_URL_SOURCE_TEMPLATE is " +
					"empty — the rewrite has no URL source to serve, the module was not started")
			} else {
				mod, err := exhentai.New(exhentai.Config{
					ConfigURL:         cfg.ExhentaiConfigURL,
					AllowInsecureHTTP: cfg.ExhentaiConfigInsecure,
					HTTPBase:          urlClient,
					AuthHeaders:       exhentaiAuthHeaders(cfg.ExhentaiConfigAuth),
					// log.LogInfo has the exact signature the module expects.
					Logf: log.LogInfo,
				})
				if err != nil {
					// A malformed config URL is a misconfiguration, so it fails
					// loudly here the way the ech-proxy module does. A reachable
					// URL that later goes down is not: that degrades to the
					// built-in table at runtime, which is deliberate — an
					// operator's config server must not be able to take the node
					// down with it.
					return fail(fmt.Errorf("exhentai module: %w", err))
				}
				if err := mod.Start(context.Background()); err != nil {
					return fail(fmt.Errorf("exhentai: %w", err))
				}
				shutdowns = append(shutdowns, func() {
					if err := mod.Stop(context.Background()); err != nil {
						log.LogWarn("main: stop exhentai module: %v", err)
					}
				})
				urlClient = mod.Client()
				if src := mod.Snapshot(); src.Version != "" {
					log.LogInfo("main: exhentai module enabled — routing table %q from %s (%d rules)",
						src.Version, src.Source, len(src.Config.Rules))
				}
			}
		}

		if cfg.URLSourceTemplate != "" {
			if err := mgr.Register(source.NewURLSource(cfg.URLSourceTemplate, urlClient)); err != nil {
				log.LogWarn("main: register url source: %v", err)
			}
		}

		// Optional OpenList source (PEERDRIVE_OPENLIST_ENABLE, default off).
		//
		// OpenList cannot be imported — its driver layer lives under internal/,
		// which Go's import rules keep outside this module — so this reaches it
		// over HTTP instead. Only /p/*path is used: it streams the bytes through
		// the OpenList process and honours Range, whereas /d/*path 302-redirects
		// to the cloud provider's direct URL and would let a request bypass the
		// source's sha256 check entirely.
		//
		// OpenList has no trustworthy content hash of its own, so the hash→path
		// mapping comes from an operator-supplied table. A missing or malformed
		// table fails startup instead of registering a source that could serve
		// nothing — an explicitly opted-in data source must be told it did not
		// come up, the same rule the ech-proxy and exhentai modules follow.
		//
		// When the flag is off this whole block is skipped: nothing is
		// constructed and nothing is registered, so a node that never enables it
		// stays byte-identical to one without the source.
		if cfg.OpenListEnable {
			if cfg.OpenListBaseURL == "" {
				log.LogWarn("main: PEERDRIVE_OPENLIST_ENABLE=true but PEERDRIVE_OPENLIST_BASE_URL is " +
					"empty — the openlist source was not registered")
			} else if cfg.OpenListIndexPath == "" && !cfg.OpenListCrawl {
				log.LogWarn("main: PEERDRIVE_OPENLIST_ENABLE=true but neither PEERDRIVE_OPENLIST_INDEX_FILE " +
					"nor PEERDRIVE_OPENLIST_CRAWL is set — without an index or crawler the source could serve nothing")
			} else {
				var index map[string]string
				if cfg.OpenListIndexPath != "" {
					var err error
					index, err = source.LoadOpenListIndex(cfg.OpenListIndexPath)
					if err != nil {
						return fail(err)
					}
				}
				// A bounded client, not http.DefaultClient: an unresponsive
				// OpenList must not be able to hold a fetch slot forever.
				olClient := &http.Client{Timeout: time.Duration(cfg.OpenListTimeoutSecs) * time.Second}
				if urlClient != nil {
					// Reuse the egress module's transport (ech-proxy / exhentai)
					// so an OpenList sitting behind ECH keeps working. Those
					// transports pass non-matching hosts straight through, which
					// is what happens for any OpenList that is not one of them.
					olClient.Transport = urlClient.Transport
				}
				olSrc, err := source.NewOpenListSource(source.OpenListConfig{
					Name:     cfg.OpenListName,
					BaseURL:  cfg.OpenListBaseURL,
					Client:   olClient,
					Token:    cfg.OpenListToken,
					Index:    index,
					Verify:   cfg.OpenListVerify,
					Priority: cfg.OpenListPriority,
				})
				if err != nil {
					return fail(err)
				}
				if err := mgr.Register(olSrc); err != nil {
					log.LogWarn("main: register openlist source: %v", err)
				}
				log.LogInfo("main: openlist source enabled — %s, %d indexed files",
					olSrc.Name(), olSrc.Count())

				if cfg.OpenListCrawl {
					crawlerTimeout := time.Duration(cfg.OpenListCrawlTimeoutSecs) * time.Second
					if crawlerTimeout <= 0 {
						crawlerTimeout = 30 * time.Second
					}
					crawlerClient := &http.Client{Timeout: crawlerTimeout}
					if urlClient != nil {
						crawlerClient.Transport = urlClient.Transport
					}
					crawler, err := source.NewOpenListCrawler(source.OpenListCrawlerConfig{
						BaseURL:     cfg.OpenListBaseURL,
						Client:      crawlerClient,
						Token:       cfg.OpenListToken,
						Concurrency: cfg.OpenListCrawlConcurrency,
						Timeout:     crawlerTimeout,
					})
					if err != nil {
						log.LogWarn("main: init openlist crawler failed: %v", err)
					} else {
						rootPath := cfg.OpenListCrawlRoot
						if rootPath == "" {
							rootPath = "/"
						}
						log.LogInfo("main: starting background openlist crawler for root %q (concurrency=%d)",
							rootPath, cfg.OpenListCrawlConcurrency)
						go func() {
							crawlCtx := context.Background()
							if err := crawler.CrawlAndReload(crawlCtx, rootPath, olSrc); err != nil {
								log.LogWarn("main: background openlist crawler finished with error: %v (indexed files: %d)",
									err, olSrc.Count())
							} else {
								log.LogInfo("main: background openlist crawler finished successfully (%d files indexed)",
									olSrc.Count())
							}
						}()
					}
				}
			}
		}
		deps.SourceManager = mgr
		// serveFile multi-source routing (3rd optimization on 2026-08-18): when a peer req
		// misses locally, fall back to the peer/URL template (loop prevention via dcReq.Trace).
		// HTTP download root requests already use mgr; here we reuse the same instance to keep
		// routing order consistent.
		peerjsSvc.SetFileRouter(mgr)
	}

	// Optional twitter-pic gallery module (PEERDRIVE_TWITTERPIC_ENABLE, default
	// off). twitter-pic-go 数据面的 peerdrive 整合：按「一个 user 作为一个
	// collection」把图库用户拉进 sha-文件系统（摄取媒体 + 内容寻址集合 JSON）。
	// 无子进程、无本地监听——纯出站 HTTP（API + 媒体下载），client 复用上面
	// ech-proxy/exhentai 模块可能产生的 urlClient（其 Transport 走出口；nil 时
	// 直连）。放在 PeerJS 块之外（与 iwara 同先例）：模块不依赖对端互联，
	// PeerJS 关闭的节点同样可用。开关关掉时整块跳过：默认节点与未启用时
	// 逐字节等价（零变化）。
	if cfg.TwitterPicEnable {
		svc, err := twitterpic.NewService(twitterpic.ServiceConfig{
			BaseURL:    cfg.TwitterPicBaseURL,
			ProxyBase:  cfg.TwitterPicProxyBase,
			StorageDir: cfg.StorageDir,
			MaxFiles:   cfg.TwitterPicMaxFiles,
			MaxBytes:   cfg.TwitterPicMaxBytes,
			Timeout:    time.Duration(cfg.TwitterPicTimeout) * time.Second,
			Client:     urlClient,
		})
		if err != nil {
			return fail(fmt.Errorf("twitter-pic module: %w", err))
		}
		deps.TwitterPic = svc
		log.LogInfo("main: twitter-pic module enabled — %s", svc.BaseURL())
	}

	// aria2c plugin integration bridge (Issue #236).
	// Always instantiate lightweight bridge to allow runtime toggle via /p2p/aria2/toggle.
	// When cfg.Aria2Enable is false, it initializes in disabled state with 0 background workers.
	aria2Bridge := service.NewAria2Bridge(cfg)
	deps.Aria2Bridge = aria2Bridge
	if cfg.Aria2Enable {
		log.LogInfo("main: aria2 plugin enabled (rpc=%s)", cfg.Aria2RPCURL)
	}

	// Hand the assembled dependency set to the router in one call.
	// NewRouter validates it before registering anything, so a wiring mistake
	// surfaces as a startup error with a message naming the field, rather than as
	// routes that silently went missing.
	log.LogInfo("main: setting up HTTP router")
	rt, err := router.NewRouter(deps)
	if err != nil {
		return fail(err)
	}
	if rt.Authenticator() != nil && share != nil {
		share.SetTokenVerifier(func(token string) string {
			u, _ := rt.Authenticator().ValidateToken(token)
			return u
		})
	}
	r := rt.Engine()

	// Security status summary (security_status.go): print "where exactly is this node open"
	// after the router is assembled. Read-only, doesn't change any default.
	// share is the NodeShare built in the PeerJS block above; nil when PeerJS is disabled,
	// which the summary itself treats as "sharing not enabled".
	logSecuritySummary(cfg, share)

	// NodeID is needed by the startup instructions (panel URL); PeerJS disabled → empty.
	var nodeID string
	if peerjsSvc != nil {
		nodeID = peerjsSvc.ID()
	}
	return r, shutdown, RouterInfo{NodeID: nodeID}, nil
}
