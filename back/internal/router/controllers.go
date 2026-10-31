package router

import (
	"strings"
	"time"

	"peerdrive/internal/btloader"
	"peerdrive/internal/controller"
	"peerdrive/internal/downloader"
	"peerdrive/internal/log"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"
	"peerdrive/internal/source"
)

// initControllers initializes backend service dependencies, registers them with their
// respective controllers, and returns the sync controller needed for route binding.
func (rt *Router) initControllers() *controller.SyncController {
	cfg := rt.cfg
	fileSvc := service.NewFileService(cfg)
	controller.InitFileController(fileSvc)
	// Probe: inject "can the database connect" as a dependency (controller doesn't touch repository directly).
	controller.InitHealth(repository.Ping)
	// M2 layering assembly: collection/share/pin services injected into controller (replaces previous direct repository calls)
	controller.InitCollectionController(service.NewCollectionService())
	controller.InitShareController(service.NewShareService())
	controller.InitPinController(service.NewPinService())

	// Port forwarding service (PeerJS DataChannel version, forward.go): the rule
	// table is configured on the service itself (PEERDRIVE_FORWARD_RULES) before
	// the router is built; runtime endpoints can dynamically add rules.
	// Peer node share list query (cloud drive target M2) and the marketplace /
	// share-scope / pull endpoints are wired in the same call — see injectControllerDeps.
	rt.injectControllerDeps()

	// Initialize the BitTorrent runtime — on demand, and only on demand.
	//
	// 发现背景：这里原本无条件调 p2p_bt.NewBTClient(cfg.DownloadDir)，而
	// PEERDRIVE_BT_ENABLE 只用来对外宣告能力（capabilities.go），从未控制客户端
	// 创建。结果是：README 写着「BitTorrent off by default」，实际每个进程启动都
	// 会开 BT 监听端口、跑 mainline DHT、联网拉公共 tracker（newBTClient →
	// buildPublicTrackers）。DHT 那支同样是半条件（BTDHTEnabled 默认 false 但
	// NewBTDHT 会 bootstrap 公共路由并 sleep 1s）。
	//
	// 现在 BT 运行时被编进 peerdrive_bt.dll：cfg.BTEnable 为 false 时连
	// LoadLibrary 都不会发生，进程里不存在 torrent 栈；为 true 时才加载并启动。
	var btClient *btloader.Client
	if cfg.BTEnable {
		if cfg.BTDHTEnabled && !btloader.Available() {
			log.LogWarn("router: BT DHT is enabled but no BT runtime library is present; " +
				"build peerdrive_bt.dll (make build-bt-dll) or set PEERDRIVE_BT_DLL")
		}
		c, err := btloader.Open(btloader.Config{
			DataDir:       cfg.DownloadDir,
			StorageDir:    cfg.StorageDir,
			DHTEnabled:    cfg.BTDHTEnabled,
			DHTListenAddr: cfg.BTDHTListenAddr,
		})
		if err != nil {
			// A failure here is not fatal: every /bt/* route answers 503 with the
			// reason, and the rest of the server is unaffected. That is the whole
			// point of moving the runtime out of the main binary.
			log.LogWarn("router: BT enabled but runtime unavailable, torrent features disabled: %v", err)
		} else {
			btClient = c
		}
	} else {
		log.LogInfo("router: BT disabled (PEERDRIVE_BT_ENABLE=0), runtime not loaded")
		if cfg.BTDHTEnabled {
			// Loading is gated by BTEnable alone, so a lone DHT flag is a no-op.
			// Say so once, instead of letting the operator wonder why /bt/status
			// reports disabled.
			log.LogWarn("router: PEERDRIVE_BT_DHT_ENABLE=1 has no effect without PEERDRIVE_BT_ENABLE=1")
		}
	}
	controller.InitBTController(btClient)

	controller.InitAnonController(service.NewAnonService(cfg))

	// The source manager (when present) gets the BT control wired here: this is the
	// one place that knows both the client and the manager, so it is the one place
	// that can join them. Previously read from a package global.
	if rt.deps.SourceManager != nil && btClient != nil {
		rt.deps.SourceManager.SetBTControl(source.NewBTControl(btClient))
		log.LogInfo("router: BT control injected")
	}
	if btClient != nil {
		// When a torrent download completes, register files in peerdrive storage.
		if cfg.StorageEnable {
			btClient.OnComplete(func(infohash string, files []btloader.CompletedFile) {
				log.LogInfo("router: BT download complete infohash=%s files=%d", infohash, len(files))
				for _, f := range files {
					if f.SHA256 == "" {
						continue
					}
					// M2 layering: registration logic consolidated into FileService.RegisterBTFile (previously inline DB write)
					if err := fileSvc.RegisterBTFile(f.SHA256, f.Size, f.Path); err != nil {
						log.LogWarn("router: register BT file %s failed: %v", f.SHA256, err)
					} else {
						log.LogInfo("router: registered BT file %s -> storage", f.SHA256)
					}
				}
			})
		}
		controller.InitBTClient(btClient)
	}

	// Initialize the IPFS gateway provider and register with manager.
	var ipfsProv *provider.IPFSProvider
	if cfg.IPFSGatewayEnable {
		gateways := strings.Split(cfg.IPFSGateways, ",")
		for i := range gateways {
			gateways[i] = strings.TrimSpace(gateways[i])
		}
		if len(gateways) > 0 {
			ipfsProv = provider.NewIPFSProvider(gateways)
			if rt.deps.SourceManager != nil && ipfsProv != nil {
				rt.deps.SourceManager.SetIPFSControl(source.NewIPFSControl(ipfsProv, cfg.StorageDir))
				log.LogInfo("router: IPFS control injected")
			}
		}
	}
	controller.InitIPFSProvider(ipfsProv)

	// Initialize the universal multi-protocol downloader.
	downloadTimeout := time.Duration(cfg.DownloadTimeoutSecs) * time.Second
	uniDownloader := downloader.NewUniversalDownloader(
		btClient,
		cfg.StorageDir,
		cfg.DownloadOrder,
		downloadTimeout,
		ipfsProv,
	)
	controller.InitUniversalDownloader(uniDownloader)

	// Sync controller initialization
	syncRepo := repository.NewSyncRepository()
	syncSvc := service.NewSyncService(syncRepo, uniDownloader, cfg.StorageDir)
	return controller.NewSyncController(syncSvc)
}
