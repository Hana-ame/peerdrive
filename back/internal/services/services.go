// Package services 把 peerdrive 的三个组件收进同一个二进制，并提供组合启动。
//
// 背景（v0.2.0）：此前是三个独立二进制（peerdrive-server / peersignal /
// peerdrive-reg-server）、同一个 tag 对齐发布，但它们本来就是一套系统的三个
// 部件——主服务、它的信令与发现、它的注册认证。部署要下三份、升三份。
// go-peerjs（PeerJS 客户端库）本来就已经链接进主服务
// （internal/transport/conn.go 导入它），无需额外处理。
//
// 设计约束：**不改变任何既有环境变量与路由**。每个组件单跑时的行为与旧的
// 独立二进制一致（信令仍支持 TLS，路由前缀不变），旧部署脚本不需要改。
package services

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	signalserver "github.com/Hana-ame/go-peerserver"

	"peerdrive/internal/config"
	"peerdrive/internal/regserver"
)

// SignalHandler 构造信令 + 节点发现的 http.Handler（不监听端口）。
//
// 路由与 back/signalserver/cmd/peersignal/main.go 逐条一致（都由
// registerSignalRoutes 登记同一份清单）。改这里等于改那个文件——两边分叉会让
// 「peerdrive signal」和旧的 peersignal 行为不同，是个隐蔽的坑
// （三处装配点由 signals_compat_test.go 专门对拍）。
func SignalHandler(cfg *config.Config) http.Handler {
	return newSignalMux(cfg)
}

func newSignalMux(cfg *config.Config) *http.ServeMux {
	key := cfg.PeerJSKey
	tokens := cfg.SignalTokens
	corsOrigin := cfg.SignalCORS
	var opts []signalserver.Option
	if tokens != "" {
		opts = append(opts, signalserver.WithTokenWhitelist(strings.Split(tokens, ",")))
	}
	if corsOrigin != "" {
		opts = append(opts, signalserver.WithCORSOrigins(strings.Split(corsOrigin, ",")))
	}
	opts = append(opts, signalserver.WithRateLimit(SignalRateLimit(cfg)))
	srv := signalserver.NewServer(key, opts...)
	srv.Start() // 后台清理过期离线队列（H3）

	mux := http.NewServeMux()
	registerSignalRoutes(mux, srv)
	// 单跑时面板直接挂 "/"；合并模式的挂法不同，见 UnifiedMux。
	mux.HandleFunc("/", srv.HandleDashboard)
	return mux
}

// registerSignalRoutes 登记信令 + 发现 + 状态这 6 条**三处装配点共享**的路由。
//
// 抽出来是为了让 newSignalMux（peerdrive signal）与 UnifiedMux（peerdrive all）
// 读同一份清单。此前两处各自抄了同样的 6 行，是「改了一处、忘了另一处」的典型
// （与 SignalRateLimit 同一类问题）：任何只加在其中一处的东西都不会自动出现在
// 另一处。第三处装配点——独立模块 back/signalserver/cmd/peersignal/main.go——
// 跨 go.mod 没法共享这段代码，由 signals_compat_test.go 对拍守住。
//
// 不含 "/"：单跑时面板挂 "/"、合并模式挂 "/_signal"，各装配点的面板挂法本就不同。
// 不含 /status/key：那是独立 peersignal 的运维端点，而主仓二进制没有配置 ops
// token 的入口（/status 已由 ops token 网关），挂上也只会是个永远 401 的死端点。
func registerSignalRoutes(mux *http.ServeMux, srv *signalserver.Server) {
	// PeerJS 协议兼容端点
	mux.HandleFunc("/peerjs", srv.HandleWS)
	mux.HandleFunc("/peerjs/id", srv.HandleID)
	// 内置房间发现（取代 MQTT）
	mux.HandleFunc("/discover/announce", srv.HandleAnnounce)
	mux.HandleFunc("/discover/leave", srv.HandleLeave)
	mux.HandleFunc("/discover/nodes", srv.HandleNodes)
	// 状态 API
	mux.HandleFunc("/status", srv.HandleStatus)
}

// SignalRateLimit 返回信令端点的限流配置（2026-10-06，N3）。
//
// 抽成独立函数，是为了让 newSignalMux 与 UnifiedMux 读**同一个**来源。
// 此前这两个装配点各写各的，是「改了一处、忘了另一处」的典型：UnifiedMux
// 自己重建了一份 signalserver（不经过 newSignalMux），任何加在新SignalMux
// 上的 Option 都不会自动出现在 `peerdrive all` 里。
//
// 变量名沿用主仓 PEERDRIVE_ 前缀（信令本体的 PEERSIGNAL_* 是旧独立二进制的）。
// 默认值与 cmd/peersignal 的 -rate-* 保持一致，理由见那里的注释。
// 设为 0 或非法值退回默认：**不提供「关掉限流」**，因为这些端点按设计就是
// 公开的，速率是唯一防线。
func SignalRateLimit(cfg *config.Config) signalserver.RateLimitConfig {
	return signalserver.RateLimitConfig{
		AnnounceRPS:   cfg.SignalRateAnnounce,
		AnnounceBurst: 10,
		WSRPS:         cfg.SignalRateWS,
		WSBurst:       20,
		IDRPS:         cfg.SignalRateID,
		IDBurst:       20,
	}
}

// RegPatterns 是注册服务的全部路由。UnifiedMux 依赖它把路由逐条搬进总 mux，
// 测试也用它断言「没有漏搬」。
var RegPatterns = []string{
	"GET /ping", "GET /api/health",
	"POST /auth/register", "POST /auth/login",
	"GET /auth/whoami", "GET /auth/list",
	"POST /p2p/relay/register", "POST /p2p/relay/heartbeat",
	"GET /p2p/relay/list",
}

// RegHandler 构造注册/认证/中继登记的 http.Handler（不监听端口）。
// dbPath 为空则由 regserver 自行按 DB_PATH → PEERDRIVE_REG_DB → ./reg.db 解析。
func RegHandler(dbPath string) (*regserver.Server, error) {
	return regserver.New(dbPath)
}

// ServeHTTP 在 addr 上监听；certFile 与 keyFile 都非空时走 TLS。
//
// 与 signalserver.Serve 同语义：只给其一即报错退出，不做静默降级——
// 静默降级会让「以为在跑 HTTPS 其实是 HTTP」上线，代价远大于启动失败。
func ServeHTTP(addr, certFile, keyFile string, h http.Handler) error {
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return fmt.Errorf("load TLS keypair: %w", err)
		}
		srv := &http.Server{
			Addr:      addr,
			Handler:   h,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		}
		return srv.ListenAndServeTLS("", "")
	}
	if certFile != "" || keyFile != "" {
		return errors.New("TLS requires both -tls-cert and -tls-key (only one given)")
	}
	return http.ListenAndServe(addr, h)
}

	// (C-2 fix) SignalConfig / RegAddr / envOr / joinHostPort removed:
	// all signal-subcommand env parsing now lives in config.Load().
	// The `peerdrive signal` subcommand reads its defaults from config.Load()
	// and overrides them with flag values before calling SignalHandler(cfg).

// UnifiedMux 把主服务、信令、注册服务合并到一个 http.Handler（同一端口）。
//
// 路由归属：
//
//	主服务   其余全部路径（兜底 "/"）
//	信令     /peerjs /peerjs/id /discover/* /status
//	注册     /ping /api/health /auth/* /p2p/relay/*
//
// 冲突面：主服务自身路由里没有这批前缀（由 TestNoRouteConflict 守住），
// 故可以安全地由信令与注册服务接管。Go 1.22 的 ServeMux 按「最长的模式优先」，
// 所以 /peerjs 会盖过兜底的 "/"。
//
// 信令面板：signalserver 的 HandleDashboard 只认路径 "/"（硬判 r.URL.Path != "/"），
// 而合并模式下 "/" 归主服务兜底。解决方式是把它改挂到 /_signal 并剥掉前缀，
// 而不是让主服务把 "/" 让出来——那会牺牲主服务自己的首页。
func UnifiedMux(cfg *config.Config, ginHandler http.Handler) (*http.ServeMux, *regserver.Server, error) {
	mux := http.NewServeMux()

	// cfg 允许为 nil：测试里只想验证路由归属时不必构造整个配置。
	// nil 时用 config.Load() 读环境变量（C-2 fix：不再在 services.go 直读 env）。
	if cfg == nil {
		cfg = config.Load()
	}

	// 信令：显式登记，不用 newSignalMux 的 "/" 兜底（那是面板，会被主服务挡掉）。
	sigOpts := []signalserver.Option{}
	if toks := cfg.SignalTokens; toks != "" {
		sigOpts = append(sigOpts, signalserver.WithTokenWhitelist(strings.Split(toks, ",")))
	}
	if cors := cfg.AllowedOrigins; cors != "" {
		sigOpts = append(sigOpts, signalserver.WithCORSOrigins(strings.Split(cors, ",")))
	}
	// 与 newSignalMux 读同一个限流来源（SignalRateLimit）——这两条装配路径
	// 必须同时加限流，否则 `peerdrive all` 的信令端点又变回无限流。
	sigOpts = append(sigOpts, signalserver.WithRateLimit(SignalRateLimit(cfg)))
	sig := signalserver.NewServer(cfg.PeerJSKey, sigOpts...)
	sig.Start()
	// 与 newSignalMux 读同一份路由清单（registerSignalRoutes）。
	registerSignalRoutes(mux, sig)
	// 信令面板：HandleDashboard 硬拒非 "/" 的路径（signalserver/signalserver.go），
	// 而合并模式下 "/" 归主服务兜底，所以面板在这里是拿不到的。
	// 改挂 /_signal 面板 + 剥前缀，让合并模式下也能开面板——
	// 否则 `peerdrive all` 的运维就失去了唯一的信令可视化入口。
	mux.Handle("/_signal", stripPrefix("/_signal", http.HandlerFunc(sig.HandleDashboard)))

	// 注册服务：整块挂同一个 Handler——它的 Handler() 内部已按 9 条路由分派，
	// 交给 ServeMux 顶层路由比逐条搬运更不容易漏。
	reg, err := RegHandler(regDBPath(cfg))
	if err != nil {
		return nil, nil, err
	}
	regMux := reg.Handler().(*http.ServeMux)
	for _, p := range RegPatterns {
		if conflictsMainRoute(p) {
			// 见 prefixConflicts 的说明：静默换语义是最坏的一类破坏，
			// 宁可不挂。挂到 /_reg 前缀下仍然可达。
			mux.Handle(withPrefix(regPrefix, p), stripPrefix(regPrefix, regMux))
			continue
		}
		mux.Handle(p, regMux)
	}

	// 主服务兜底
	mux.Handle("/", ginHandler)
	return mux, reg, nil
}

// regPrefix 是与主服务冲突的注册路由改挂的前缀。
// `peerdrive reg` 单跑时仍用原路径（/ping），只有合并模式才搬到这里——
// 免得给单跑路径也改一遍，那样所有既有部署都得改 URL。
const regPrefix = "/_reg"

// prefixConflicts 列出「注册服务有、主服务也有」的同名路由。
//
// ⚠️ 这不是理论担忧，是实测到的：主服务 router.go:234 有 `r.GET("/ping", controller.Ping)`
// 返回 "pong"，而注册服务也有 `GET /ping` 返回 {"service":"peerdrive-registration"}。
// 谁先被匹配谁生效——合并模式下注册服务赢，主服务的 /ping 静默变成另一个响应体。
//
// 不 crash、不报错、监控上看「/ping 通了」，只有依赖旧响应体的脚本会炸。
// 所以这里显式登记冲突并改挂前缀，而不是让谁赢看注册顺序。
var prefixConflicts = map[string]string{
	"GET /ping": "主服务 controller.Ping 返回 pong",
}

// conflictsMainRoute 报告该注册路由是否与主服务撞名。
func conflictsMainRoute(pattern string) bool {
	_, hit := prefixConflicts[pattern]
	return hit
}

// stripPrefix 剥掉路径前缀后再交给内层 handler。
//
// 为什么必须剥：注册服务自己的 Handler() 内部是一个 ServeMux，它的路由写死在
// "/ping"、"/auth/login"。外层把模式改成 "/_reg/ping" 之后，请求到达内层时
// r.URL.Path 仍是 "/_reg/ping"，内层匹配不到 "/ping" —— 直接挂会得到 404。
// 这里复制一份 request 并改写 Path，让内层以为路径没变过。
func stripPrefix(prefix string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, prefix) {
			stripped := strings.TrimPrefix(r.URL.Path, prefix)
			if stripped == "" {
				// 恰好等于前缀（如 /_signal）：剥完是空串，
				// 而 HandleDashboard 硬判 Path != "/"，空串会被它当 404。
				// 所以要补回根路径——这是「/prefix == /」这个挂法的语义。
				stripped = "/"
			}
			r2 := new(http.Request)
			*r2 = *r
			r2.URL = new(url.URL)
			*r2.URL = *r.URL
			r2.URL.Path = stripped
			if r2.URL.RawPath != "" {
				r2.URL.RawPath = stripped
			}
			r = r2
		}
		h.ServeHTTP(w, r)
	})
}

// withPrefix 给 "METHOD /path" 形式的模式加上路径前缀。
// "GET /ping" -> "GET /_reg/ping"。
//
// 不要用切片拼（如 p[4:]）：那依赖方法名的长度，POST/DELETE 一变长就拼错，
// 且会悄悄丢掉方法词——得到 " /auth/register" 这种非法模式。
func withPrefix(prefix, pattern string) string {
	if i := strings.IndexByte(pattern, ' '); i >= 0 {
		return pattern[:i+1] + prefix + pattern[i+1:]
	}
	return prefix + pattern
}

func regDBPath(cfg *config.Config) string {
	if cfg == nil {
		cfg = config.Load()
	}
	if p := cfg.LegacyDBPath; p != "" {
		return p
	}
	if p := cfg.RegDBPath; p != "" {
		return p
	}
	if cfg.StorageDir != "" {
		return cfg.StorageDir + "/reg.db"
	}
	return "./reg.db"
}
