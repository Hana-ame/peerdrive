// regserver.go — peerdrive 注册/认证/中继登记服务的**装配入口**。
//
// 本文件只负责 Server 结构、构造、路由装配与生命周期；具体功能拆到同包其它
// 文件（2026-10-09 起，见 feat/regserver-split）：
//
//	jwt.go             JWT 签发与校验（HS256 手写，与原服务字节级兼容）
//	auth.go            JSON 工具 + Bearer 认证中间件（net/http 版）
//	db.go              SQLite 打开与 schema
//	handlers_auth.go   /auth/* + /ping + /api/health 的 handler
//	handlers_relay.go  /p2p/relay/* 的 handler
//
// 拆分的理由（与 hashmap/collection 的「主模块 internal 纯逻辑包」口径一致，
// 但本包依赖 database/sql + bcrypt，不是纯逻辑）：
//   - 行为不改：每个 handler 函数签名、路由、状态码、返回体都逐字保留。
//   - 单文件 656 行 → 多文件各 ~80-200 行，便于单测按功能块组织。
//   - 不建独立 go.mod：regserver 只被主模块 internal 用（cmd/peerdrive、
//     services），没有跨模块消费方——参照 hashmap 的判定，不需要独立模块。
//   - 不建子包：Server 结构持有 db/jwtSecret/限流器，拆子包会让构造器跨包，
//     且破坏现有测试的 newBareServer 写法（直接构造 &Server{jwtSecret: ...}）。
//
// 归属：本包是 peerdrive 主仓的一部分（2026-10-05 自独立仓
// github.com/Hana-ame/registration-server 并入）。并入前该仓已被 archive，
// 但 peerdrive 的认证链路本来就依赖它——back/internal/router/auth_middleware.go
// 通过 GET {RegistrationServer}/auth/whoami 校验 Bearer token，
// RegistrationServer 由 PEERDRIVE_REG_SERVER 配置（见 internal/config/config.go）。
// 即「主仓依赖一个仓外服务」是既成事实；并入后这份依赖可以指向本仓自身。
//
// 行为与原独立仓逐条对齐（不重新设计，调用方与既有数据都依赖它）：
//
//	GET  /ping                  -> {"service":"peerdrive-registration","status":"ok"}  （无认证）
//	GET  /api/health            -> {"status":"ok"}                                     （需 Bearer）
//	POST /auth/register         -> {username, token}                                   （无认证）
//	POST /auth/login            -> {token}                                             （无认证）
//	GET  /auth/whoami           -> {username, role}                                    （需 Bearer）
//	GET  /auth/list             -> {"users":[...]}                                      （需 Bearer）
//	POST /p2p/relay/register    -> {"status":"registered"}                              （无认证）
//	POST /p2p/relay/heartbeat   -> {"status":"ok"}                                      （无认证）
//	GET  /p2p/relay/list        -> {"relays":[...]}                                     （无认证）
//
// 数据兼容：直接复用既有 SQLite 库（表 users / relay_nodes，密码 bcrypt
// cost 10）。同 JWT_SECRET 时原服务签发的旧 token 继续可验——这是「搬进主仓」
// 而非「重写」的前提，故 JWT 实现（HS256 手写、claims 字段、iss、TTL 3 天）
// 一律照抄，未改用第三方库。
//
// 用法（必须走 -tags nosqlite，与 cmd/server 同理：双 SQLite 驱动的 CGO
// 符号冲突）：
//
//	JWT_SECRET=xxx PEERDRIVE_REG_DB=./reg.db go run -tags nosqlite ./cmd/peerdrive/ reg
//
// 环境变量沿用原服务名以免改部署脚本：PORT / HOST / DB_PATH / JWT_SECRET。
// 另认 PEERDRIVE_REG_DB 作为 DB_PATH 的别名（主仓配置项统一带 PEERDRIVE_ 前缀）。
//
// 限流（2026-10-06，N2）：register / login / relay 三个无认证端点按来源 IP
// 限流，login 另有账号级指数退避。这些端点「无认证」是**既有对外行为**（不能改），
// 所以速率与退避是唯一的防线。参数：
//
//	PEERDRIVE_REG_RATE_REGISTER  默认 0.5 req/s（burst 3）——每次都跑一次 bcrypt cost10，最贵
//	PEERDRIVE_REG_RATE_LOGIN     默认 1   req/s（burst 5）——同样每次 bcrypt 比对
//	PEERDRIVE_REG_RATE_RELAY     默认 2   req/s（burst 20）——只写库，但能伪造 peer_id 污染名录
//
// 登录退避参数在 ratelimit.DefaultBackoff：连续 5 次失败后 15s 起指数翻倍，
// 上限 5 分钟；登录成功即清零。设为 0 或非法值退回默认，**不提供「不限流」**。

package regserver

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Hana-ame/go-peerserver/tracker"
	"peerdrive/internal/httpd"
	"peerdrive/internal/ratelimit"
)

// ---------------------------------------------------------------------------
// 限流中间件（2026-10-06，N2）
//
// 根因：`/auth/*` 与 `/p2p/relay/*` 都不经过 gin engine——
// 独立 `peerdrive reg` 是纯 net/http 进程；`peerdrive all` 模式下这两批路由
// 由 services.UnifiedMux 直接从 reg.Handler() 搬进总 mux。
// 而项目里原本**只有**一个限流实现，且它挂成 gin 中间件（router.go:55）。
// 于是这些公开端点从未被限过：
//
//	/auth/register        无认证 + bcrypt cost10 → 可无限刷号，且每个请求
//	                       都真跑一次 ~60-100ms 的 bcrypt，等于一个 CPU 放大器
//	/auth/login           无退避 → 可爆破（每次也是一次 bcrypt）
//	/p2p/relay/register   无认证 + 幂等 upsert → 可任意伪造 peer_id
//	                       污染中继名录（relay_nodes 表按 peer_id 主键）
//
// 挂载点选在 Handler() 里（而不是 Serve 或各子命令）的原因：
// Handler() 是这 9 条路由的**唯一装配点**，`peerdrive reg`（经 srv.Serve→
// s.Handler()）与 `peerdrive all`（经 services.UnifiedMux→reg.Handler()）
// 都从这里过，一条实现同时盖住两条部署路径；在 Serve 里挂则会漏掉 all 模式，
// 而 all 模式恰恰是**默认**给外部署用的那个。

// rateLimit 按来源 IP 限流，超限回 429 + Retry-After。
//
// 放在本文件（而不是 auth.go）是因为它是与 authRequired 并列的另一类中间件，
// 但只在 Handler 装配时用——与 Server 的生命周期绑定，不跨功能块。
func (s *Server) rateLimit(l *ratelimit.Limiter) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(ratelimit.ClientIP(r)) {
				ratelimit.TooManyRequests(w, time.Second)
				return
			}
			next(w, r)
		}
	}
}

// ---------------------------------------------------------------------------
// Server：可复用入口
//
// 为什么要 Server 而不是继续用包级全局：v0.2.0 起注册服务要和主服务、信令
// 装进同一个二进制，还可能被「同端口」模式挂到已有 mux 上。包级全局 db
// 在同进程内起两个实例就会互相覆盖，且测试无法并行。全局 s.jwtSecret 同理。
//
// 兼容性：迁入时逐函数对拍原实现（compat_test.go），JWT 字节级一致，
// 故把全局改成字段不改变任何对外行为——只是把状态从包级挪到实例上。

// Server 是一个注册/认证/中继登记服务实例。
type Server struct {
	db        *sql.DB
	jwtSecret []byte

	// 限流状态（2026-10-06，N2）。
	//
	// 为什么限流器挂在 Server 上而不是全局：全局会让同进程的两个实例
	// 共用一份桶状态，测试并行时互相干扰——这与 v0.2.0 把 db/jwtSecret
	// 挪进实例的同一条理由。
	//
	// 为什么这三类端点的默认桶要**分档**（见 Handler 里的注释）：
	// bcrypt cost 10 单次约 60-100ms，register 无认证且每次都真跑一次 bcrypt，
	// 与 1 字节查询的 whoami 共用一个 30rps 的桶，前者每秒最多只能过 ~10 次，
	// 后者却还有富余——一档就等于把最贵的端点限死在最低档。
	// 所以按「代价」分三档，而不是全站一刀切。
	registerLimiter *ratelimit.Limiter // 昂贵（bcrypt）+ 可刷号
	loginLimiter    *ratelimit.Limiter // 昂贵（bcrypt 比对）+ 可爆破
	relayLimiter    *ratelimit.Limiter // 廉价写库，但会污染中继名录
	loginBackoff    *ratelimit.Backoff // 账号级退避（防爆破的主力，限流只是兜底）

	// tracker is an optional BitTorrent HTTP tracker server (BEP 12/31).
	// When set, /announce, /scrape, and /tracker/bans are served alongside
	// the registration endpoints. Ban management uses JWT auth (Bearer token),
	// and user-level bans map to regserver usernames via JWT verification.
	tracker *tracker.Tracker
}

// New 打开（或创建）指定路径的库并返回服务实例。
// dbPath 为空时按 DB_PATH → PEERDRIVE_REG_DB → ./reg.db 解析。
// secret 为空直接报错——空密钥会让任何人伪造 token。
func New(dbPath string) (*Server, error) {
	if dbPath == "" {
		dbPath = ResolveDBPath()
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		return nil, errors.New("JWT_SECRET is required")
	}
	s := &Server{
		jwtSecret:      []byte(secret),
		registerLimiter: ratelimit.New(regRate("PEERDRIVE_REG_RATE_REGISTER", 0.5), 3),
		loginLimiter:    ratelimit.New(regRate("PEERDRIVE_REG_RATE_LOGIN", 1), 5),
		relayLimiter:    ratelimit.New(regRate("PEERDRIVE_REG_RATE_RELAY", 2), 20),
		loginBackoff:    ratelimit.DefaultBackoff(),
	}
	if err := s.openDB(dbPath); err != nil {
		return nil, fmt.Errorf("db open failed: %w", err)
	}
	return s, nil
}

// regRate 读一个限流速率；未配置时用默认值。
//
// 设成 0 会退回默认值而不是「不限流」：reg-server 的这些端点
// **按设计就是公开的**（register/relay 无认证是既有行为，不能改），
// 因此「关掉限流」不是一个安全选项，只能调快调慢。确实需要放开时，
// 调一个很大的值即可，语义仍然显式。
func regRate(env string, def float64) float64 {
	v := os.Getenv(env)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return def
	}
	return f
}

// ResolveDBPath 按 DB_PATH → PEERDRIVE_REG_DB → ./reg.db 的顺序解析库路径。
// 沿用原服务的变量名，避免改部署脚本；PEERDRIVE_REG_DB 是主仓风格别名。
func ResolveDBPath() string {
	if p := os.Getenv("DB_PATH"); p != "" {
		return p
	}
	if p := os.Getenv("PEERDRIVE_REG_DB"); p != "" {
		return p
	}
	return "./reg.db"
}

// Close 关闭数据库句柄。调用方必须关：Windows 上不关会导致文件删不掉。
func (s *Server) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// PingDB 探测数据库连通性；库已关闭时返回错误。
//
// 为什么要它而不是直接摸 s.db：Close 之后 *sql.DB 指针仍在，但内部连接池
// 已失效，直接调 db.Ping() 会 panic（nil 解引用）而不是返回 error。
// 用它才能把「关没关」安全地表达成一个布尔。
func (s *Server) PingDB() error {
	if s == nil || s.db == nil {
		return errors.New("db not opened")
	}
	return s.db.Ping()
}

// SetTracker sets the BitTorrent HTTP tracker server. When provided, the
// server exposes GET /announce, GET /scrape, and /tracker/bans alongside
// the registration endpoints. Ban management at /tracker/bans uses JWT
// Bearer auth (same protocol as /auth/whoami). The tracker's verifyUser
// callback uses the server's JWT verification, so announce requests with
// a valid Bearer token are automatically associated with the user's identity.
func (s *Server) SetTracker(tr *tracker.Tracker) {
	s.tracker = tr
}

// SetupTracker creates a BitTorrent HTTP tracker with the given options,
// wires JWT auth (ban management + user→peer_id mapping), and returns the
// tracker. The regserver's JWT verification (verifyToken) is used for
// both: ban management at /tracker/bans accepts any valid Bearer JWT,
// and announce requests with a valid Bearer token are associated with
// the user's identity for user-level bans.
//
// Typical usage:
//
//	tr := srv.SetupTracker(tracker.WithBanFile("tracker_bans.json"),
//	    tracker.WithAnnounceInterval(900), tracker.WithMaxPeers(100))
//	srv.SetTracker(tr)
func (s *Server) SetupTracker(opts ...tracker.Option) *tracker.Tracker {
	// Prepend auth options before user-supplied options so they take
	// precedence (user can override if needed).
	allOpts := append([]tracker.Option{
		tracker.WithBanAuth(s.jwtBanAuth()),
		tracker.WithVerifyUser(s.jwtVerifyUser()),
	}, opts...)
	return tracker.NewTracker(allOpts...)
}

// Handler 返回带全部路由的 http.Handler。
//
// 路由与原独立仓逐条一致，改这里等于改原仓的实现——
// peerdrive 的 auth_middleware.go 依赖 GET /auth/whoami，动了会静默改鉴权行为。
//
// 装配顺序：先限流（拦截最贵的请求），再 auth（解析身份），最后 handler。
// rateLimit 在外层是因为「限流住」连身份解析都不该做——省 CPU 且省枚举信道。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", s.apiPing)
	mux.HandleFunc("GET /api/health", s.authRequired(s.apiHealth))
	// 三档限流桶，理由见上面 rateLimit 的注释（按「每次请求的代价」分档）。
	mux.HandleFunc("POST /auth/register", s.rateLimit(s.registerLimiter)(s.authRegister))
	mux.HandleFunc("POST /auth/login", s.rateLimit(s.loginLimiter)(s.authLogin))
	mux.HandleFunc("GET /auth/whoami", s.authRequired(s.authWhoami))
	mux.HandleFunc("GET /auth/list", s.authRequired(s.authList))
	mux.HandleFunc("POST /p2p/relay/register", s.rateLimit(s.relayLimiter)(s.relayRegister))
	mux.HandleFunc("POST /p2p/relay/heartbeat", s.rateLimit(s.relayLimiter)(s.relayHeartbeat))
	mux.HandleFunc("GET /p2p/relay/list", s.relayList)

	// BitTorrent HTTP tracker routes (BEP 12/31) — mounted after the reg
	// routes. Ban management at /tracker/bans uses JWT Bearer auth, same
	// protocol as /auth/whoami.
	if s.tracker != nil {
		mux.HandleFunc("/announce", s.tracker.HandleAnnounce)
		mux.HandleFunc("/scrape", s.tracker.HandleScrape)
		mux.HandleFunc("/tracker/bans", s.tracker.HandleBans())
	}

	return mux
}

// Serve 在 addr 上启动并阻塞，直到出错。
// 只给 certFile 或 keyFile 其一即报错——静默降级成明文会让「以为在跑 HTTPS」
// 的部署上线，代价远大于启动失败。
//
// 实现已搬到 internal/httpd（密钥加载、半参数报错、普通 Serve 都在那里）；
// 签名 (addr, certFile, keyFile) 保持不变，runReg 按这个顺序传参。
func (s *Server) Serve(addr, certFile, keyFile string) error {
	srv, err := httpd.New(httpd.Config{
		Addr:     addr,
		CertFile: certFile,
		KeyFile:  keyFile,
	}, s.Handler())
	if err != nil {
		return err
	}
	return srv.Serve(context.Background())
}
