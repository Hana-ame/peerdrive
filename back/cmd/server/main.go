// Peerdrive 服务端入口点。
// 启动 Gin HTTP 服务器，初始化 SQLite 元数据库、内容寻址文件存储与
// PeerJS 信令 + WebRTC 文件服务（Go 节点作为常驻 peer 提供文件）。
// 支持环境变量 PORT（监听端口）和 PEERDRIVE_STORAGE（存储目录，默认 ./storage）。
// 使用方式（必须 -tags nosqlite，双 SQLite 驱动 CGO 符号冲突）：
//   go run -tags nosqlite ./cmd/server/main.go
//   PORT=3000 PEERDRIVE_STORAGE=./storage go run -tags nosqlite ./cmd/server/main.go
// 内部流程：InitDB → PeerJSService.Start → 注册 local/peer/url source → SetupRouter
//
// storageDir 注入到 Gin Context，供 controller/anon.go 等使用。
// 历史背景：原 libp2p 互联层于 2026-08-16 全删（见 doc/LEGACY.md §A），
// 本注释曾描述 "libp2p P2P 节点 / NewP2PService→IPFSService→UniversalDownloader"
// 旧流程，与现状不符，本次校正为新流程。

package main

import (
	"errors"
	"fmt"
	stdlog "log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

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

// main 是 Peerdrive 服务器入口，初始化 DB、P2P、HTTP 路由并监听端口。
func main() {
	log.LogInfo("main: Peerdrive server starting")

	cfg := config.Load()
	storageDir := cfg.StorageDir
	log.LogInfo("main: config loaded, storageDir=%s, port=%s", storageDir, cfg.Port)

	// 启动期拒绝"卷根"配置（doc/NETDISK.md §11.3）。
	//
	// pathutil.Within 是纯粹的包含判定：root 配成 `/`（Windows 的 `C:\`）时它
	// 当然会放行 `/etc/passwd`，而且那不是 bug——那是配置字面上的意图。
	// 但几乎没人真的想把整个盘共享出去，这种值基本都是配错（`PEERDRIVE_STORAGE=/`
	// 常见于环境变量没展开）。与其让节点安静地全盘放行，不如启动就拒绝；
	// 真要用的运营者自己设 PEERDRIVE_ALLOW_UNSAFE_ROOT=1。
	if err := checkUnsafeRoots(cfg); err != nil {
		stdlog.Fatalf("%v", err)
	}
	warnUnsupportedRoots(cfg)

	// 初始化 DB（含迁移）
	log.LogInfo("main: initializing database")
	if err := repository.InitDB("./peerdrive.db"); err != nil {
		stdlog.Fatalf("数据库初始化失败: %v", err)
	}
	log.LogInfo("main: database initialized")

	// 初始化匿名存储目录（与普通文件同一目录）
	repository.SetAnonStorageDir(storageDir)

	// 初始化 PeerJS 信令 + WebRTC 文件服务（Go 节点作为常驻 peer 提供文件，
	// 与浏览器/其它节点经 PeerJS 信令互联）。信令服务器默认公共云
	// 0.peerjs.com，生产经 PEERDRIVE_PEERJS_HOST/KEY 指向自托管 peerserver
	// （见 doc/PEERSIGNAL.md / AGENTS.md 线上部署）。
	// 注意：SetPeerJSService 必须在 SetupRouter 之前调用，路由注册时读取。
	var peerjsSvc *transport.PeerJSService
	if cfg.PeerJSEnable {
		log.LogInfo("main: initializing PeerJS WebRTC service")
		peerjsSvc = transport.NewPeerJSService(cfg, storageDir)

		// 对外**可读**的根目录（与"可登记/可写入"是两回事，见 AddReadRoot 注释）：
		//   - storage 根：运营者经 HTTP API 登记的文件（register_local/folder）常在这里；
		//   - PEERDRIVE_SHARE_DIRS：运营者自己声明要共享的目录，可能在任意挂载点。
		// 少了这一步，共享目录不在下载目录下时会出现「清单列得出、对端一拉
		// read failed」——登记侧放行了，读取侧却判它越权，回退到并不存在的
		// 内容寻址副本。
		peerjsSvc.FileIndex().AddReadRoot(storageDir)
		for _, d := range pathutil.SplitList(cfg.ShareDirs) {
			peerjsSvc.FileIndex().AddReadRoot(d)
		}

		peerjsSvc.Start()
		defer peerjsSvc.Close()
		log.LogInfo("main: PeerJS node id=%s", peerjsSvc.ID())
	}

	// 节点市场目录（doc/NETDISK.md M1）：市场列表 = 发现服务器在线节点 ∪ 已加入清单。
	// 注入顺序敏感：SetExtraPeers 让「市场里加入的节点」在每次信令重连后自动拨号
	// （与配置 PEERDRIVE_PEERJS_PEERS 同等地位）；SetNodeDirectory 必须在
	// SetupRouter 之前（路由注册时读取）。
	if peerjsSvc != nil {
		nodeDir := service.NewNodeDirectory(storageDir, cfg.DiscoverURL)
		nodeDir.SetSelfID(peerjsSvc.ID)
		nodeDir.SetConnected(peerjsSvc.ConnectedPeerIDs)
		nodeDir.SetDial(peerjsSvc.EnsureConnection)
		peerjsSvc.SetExtraPeers(nodeDir.JoinedPeerIDs)
		router.SetNodeDirectory(nodeDir)
		log.LogInfo("main: node directory ready (joined=%d)", len(nodeDir.JoinedPeerIDs()))

		// 节点共享范围（doc/NETDISK.md M2）：share 帧的数据源 + announce 摘要。
		// AnonService 是无状态读服务（只持 cfg），这里再建一个实例专供共享
		// 解析用，不与 router 内部那个实例共享状态（也不需要共享）。
		share := service.NewNodeShare(cfg)
		anonReader := service.NewAnonService(cfg)
		share.SetAnonAccess(anonReader.GetCollectionByHash, anonReader.ListCollections)
		// 文件共享只按目录前缀过滤；List 内部上限 1000（repository 层 clamp），
		// 共享清单超过 1000 个文件时按 seq 序取前 1000 —— 够市场展示与选择，
		// 真正的批量拉取走合集（不依赖这份清单）。
		share.SetFileLister(func() ([]transport.FileInfo, error) {
			return peerjsSvc.FileIndex().List(0, 1000)
		})
		peerjsSvc.SetShareProvider(share.Snapshot)
		nodeDir.SetShareSummary(share.Summary)

		// 跨节点拉取保存（doc/NETDISK.md M3）：对端内容 → 本节点落盘 + 登记。
		// downloadRoot 必须是 file_index 的允许根目录（cfg.DownloadDir），
		// 否则登记会被 H2 安全边界拒绝（"path outside allowed root"）。
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

	// 设置路由（内部注入 storageDir/downloader 到 context）
	if cfg.RegistrationServer != "" {
		router.SetRegServer(cfg.RegistrationServer)
	}
	if peerjsSvc != nil {
		router.SetPeerJSService(peerjsSvc)
		router.SetPeerJSConfig(cfg)
		// 端口转发授权规则（forward v2）：PEERDRIVE_FORWARD_RULES="key:port,key2:port2"。
		// key 即凭证（服务端 HMAC 验证用原文）——配置为敏感文件，建议 chmod 600。
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
		// 统一 source 体系装配：本地磁盘（file_index + CAS）→ p2p 透传 → URL 源。
		// 路由语义：本地优先命中即返回，未命中降级 peer；URL 源经模板注册
		// （PEERDRIVE_URL_SOURCE_TEMPLATE），可为空。管理面 GET /sources。
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
		// serveFile 多源路由（第 3 项优化 2026-08-18）：对端 req 未命中本地
		// 时回源对端/URL 模板（trace 防环见 dcReq.Trace）。HTTP 下载等根
		// 请求已走 mgr，这里复用同一实例保持路由顺序一致。
		peerjsSvc.SetFileRouter(mgr)
	}
	log.LogInfo("main: setting up HTTP router")
	r := router.SetupRouter(cfg)

	port := ":" + cfg.Port

	go func() {
		log.LogInfo("main: starting HTTP server on %s", port)
		if err := r.Run(port); err != nil {
			stdlog.Fatalf("Gin 服务器启动失败: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.LogInfo("main: shutting down server")
}

// checkUnsafeRoots 检查有没有目录被配成了卷根（`/`、`C:\`）。
//
// 为什么要单独检查：这类配置**过得了**所有运行时边界判定（root=`/` 时
// `/etc/passwd` 确实"在根内"，判定没错），所以只能在启动期按配置意图拦。
// 覆盖三个入口：storage（HTTP 登记 + 匿名上传落点）、download（对端写入）、
// share dirs（对外共享清单，可位于任意挂载点）。
//
// 逃生阀：PEERDRIVE_ALLOW_UNSAFE_ROOT=1（真的要把整盘当存储跑时）。
// configuredDirs 全部由配置指定的目录及其来源环境变量名。
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
		log.LogWarn("main: PEERDRIVE_ALLOW_UNSAFE_ROOT=1，跳过卷根配置检查")
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
	return fmt.Errorf("拒绝启动：目录被配成了文件系统卷根（%s）。这会让它下面的所有文件都对外可读/可写；"+
		"请改成具体子目录。确认要这么跑请设 PEERDRIVE_ALLOW_UNSAFE_ROOT=1",
		strings.Join(bad, ", "))
}

// warnUnsupportedRoots 启动自检：这些目录能不能用 os.Root 立起安全边界。
//
// 为什么要提前说：os.Root 建立失败时的表现很隐蔽——**那个目录里的文件就是共享
// 不出去**，而日志只有一句 errno，运维会当成别的问题查半天（chmod/chown/重配
// 路径，全都对不上病因）。这里在启动时就把每个目录的结论和下一步说清楚。
func warnUnsupportedRoots(cfg *config.Config) {
	for _, c := range configuredDirs(cfg) {
		if strings.TrimSpace(c.val) == "" {
			continue
		}
		if err := pathutil.ProbeRootSupport(c.val); err != nil {
			// 首次启动时目录还没建出来是很正常的，别把它报成"配错了"
			if errors.Is(err, os.ErrNotExist) {
				log.LogInfo("main: %s=%s 尚不存在，首次写入时会自动创建", c.name, c.val)
				continue
			}
			log.LogWarn("main: %s=%s 无法作为安全根目录：%s",
				c.name, c.val, pathutil.ExplainRootFailure(c.val, err))
		}
	}
}
