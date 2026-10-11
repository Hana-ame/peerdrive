package router

import (
	"strings"
	"time"

	"github.com/Hana-ame/go-peerdrive-bt"
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

	// Initialize BitTorrent DHT service if enabled.
	var btSvc *p2p_bt.BTDHTService
	if cfg.BTDHTEnabled {
		var err error
		btSvc, err = p2p_bt.NewBTDHT(cfg.BTDHTListenAddr)
		if err != nil {
			log.LogWarn("router: BT DHT init warning: %v", err)
		}
	}
	controller.InitBTController(btSvc)

	controller.InitAnonController(service.NewAnonService(cfg))

	// Initialize BitTorrent client for torrent/magnet downloads.
	// The source manager (when present) gets the BT control wired here: this is the
	// one place that knows both the client and the manager, so it is the one place
	// that can join them. Previously read from a package global.
	btClient := p2p_bt.NewBTClient(cfg.DownloadDir)
	if rt.deps.SourceManager != nil && btClient != nil {
		rt.deps.SourceManager.SetBTControl(source.NewBTControl(btClient))
		log.LogInfo("router: BT control injected")
	}
	if btClient != nil {
		if btSvc != nil && btSvc.Server != nil {
			p2p_bt.SetGlobalDHT(btSvc)
		}
		// When a torrent download completes, register files in peerdrive storage.
		if cfg.StorageEnable {
			btClient.SetOnComplete(func(infohash string, files []p2p_bt.CompletedFile) {
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
	} else {
		log.LogWarn("router: BT client initialization failed, torrent/magnet features disabled")
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
		btSvc,
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
