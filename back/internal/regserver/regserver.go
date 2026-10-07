// reg-server — peerdrive 注册/认证/中继登记服务。
//
// 归属：本文件是 peerdrive 主仓的一部分（2026-10-05 自独立仓
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
//	POST /auth/login            -> {token}                                              （无认证）
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
//	JWT_SECRET=xxx PEERDRIVE_REG_DB=./reg.db go run -tags nosqlite ./cmd/reg-server/
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
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite" // 驱动名由 driver_cgo.go / driver_pure.go 注册

	"peerdrive/internal/ratelimit"
)

// ---------------------------------------------------------------------------
// JSON 工具

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// ---------------------------------------------------------------------------
// JWT（HS256，手写实现，与原服务逐字节兼容）
//
// 为什么不用 golang-jwt：兼容性要求「同密钥下旧 token 直接可验」。手写实现
// 与原服务同构，claims 的字段名、iss、TTL 都一致；换库则要逐项对齐且多一处
// 依赖。安全属性（HS256、hmac.Equal 常数时间比较、exp 校验）保持原样。

// jwtSecret 移到 Server 实例上（见下方 Server 类型）。

const tokenTTL = 72 * time.Hour // 与原服务一致：旧 token 实测 exp-iat=259200s（3 天）

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func (s *Server) newToken(username, role string) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"username": username,
		"role":     role,
		"iss":      "https://localhost:4000", // 与原服务一致，勿改：已签发 token 带此值
		"sub":      username,
		"iat":      now.Unix(),
		"exp":      now.Add(tokenTTL).Unix(),
	}
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return s.newTokenFrom(hb, pb)
}

// newTokenFrom 给定 header/payload 字节拼出 HS256 签名串。拆出来是为了让
// 测试能构造「已过期」「异密钥」等 token，而不必伪造时间。
func (s *Server) newTokenFrom(hb, pb []byte) (string, error) {
	h := b64url(hb)
	p := b64url(pb)
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(h + "." + p))
	return h + "." + p + "." + b64url(mac.Sum(nil)), nil
}

// verifyToken 验签并提取 claims；返回 (username, role, error)。
func (s *Server) verifyToken(tok string) (string, string, error) {
	const bad = "invalid or expired token"
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", "", errors.New(bad)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", errors.New(bad)
	}
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) { // 常数时间比较，防时序侧信道
		return "", "", errors.New(bad)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New(bad)
	}
	var claims struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Exp      int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", errors.New(bad)
	}
	if claims.Exp != 0 && time.Now().Unix() > claims.Exp {
		return "", "", errors.New(bad)
	}
	return claims.Username, claims.Role, nil
}

// ---------------------------------------------------------------------------
// 认证中间件

type authCtxKey struct{}

type authInfo struct{ username, role string }

// authRequired 从 Authorization: Bearer 头取 token 验证；失败 401（文案与原服务一致）。
func (s *Server) authRequired(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if h == "" {
			writeErr(w, http.StatusUnauthorized, "missing authorization header")
			return
		}
		parts := strings.SplitN(h, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
			writeErr(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		user, role, err := s.verifyToken(parts[1])
		if err != nil {
			writeErr(w, http.StatusUnauthorized, err.Error())
			return
		}
		ctx := context.WithValue(r.Context(), authCtxKey{}, authInfo{user, role})
		next(w, r.WithContext(ctx))
	}
}

// authInfoOf 取出 authRequired 注入的身份；未经过中间件时返回零值。
func authInfoOf(r *http.Request) authInfo {
	if v, ok := r.Context().Value(authCtxKey{}).(authInfo); ok {
		return v
	}
	return authInfo{}
}

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
// 数据库

var db *sql.DB

// openDB 打开（必要时创建）库。表缺失时按原 schema 创建；沿用既有库时
// CREATE TABLE IF NOT EXISTS 不会动已有表，故旧数据与旧 token 均兼容。
func (s *Server) openDB(path string) error {
	d, err := sql.Open(sqliteDriver, path+dsnSuffix())
	if err != nil {
		return err
	}
	if err := d.Ping(); err != nil {
		d.Close()
		return err
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS relay_nodes (
			peer_id TEXT PRIMARY KEY,
			addrs TEXT NOT NULL,
			storage_mb INTEGER DEFAULT 0,
			load_pct REAL DEFAULT 0,
			version TEXT DEFAULT '',
			registered_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_heartbeat DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}
	// 注意变量名：循环变量不能叫 s，否则遮住 receiver，
	// 末尾的 s.db = d 会编译不过或写到错误的接收者上。
	for _, stmt := range stmts {
		if _, err := d.Exec(stmt); err != nil {
			d.Close()
			return err
		}
	}
	s.db = d
	return nil
}

// ---------------------------------------------------------------------------
// handlers

func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) apiPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "peerdrive-registration", "status": "ok"})
}

func (s *Server) authRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Username) == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	// cost 10：与原服务及既有库一致（DefaultCost 即 10）。改大会让旧 hash 仍可验
	// （bcrypt 自带 cost 前缀）但新登录变慢，故保持不动。
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	if _, err := s.db.Exec(`INSERT INTO users(username, password_hash, role) VALUES(?, ?, 'user')`,
		req.Username, string(hash)); err != nil {
		writeErr(w, http.StatusConflict, "username already exists")
		return
	}
	tok, err := s.newToken(req.Username, "user")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username, "token": tok})
}

// dummyHash 是一个**有效但永不匹配**的 bcrypt hash（cost 10）。
//
// 用途见 authLogin：用户不存在时也拿它跑一次 CompareHashAndPassword，
// 让「账号不存在」与「密码错误」耗时一致（都约 60-100ms）。
// 少了这一步，攻击者能用响应时间枚举出哪些账号真实存在。
//
// 值本身是随机生成的合法 bcrypt 串，改动无副作用；保持 cost 10 是必须的——
// bcrypt 的耗时由 cost 决定，用更低 cost 的 hash 当陪衬会让它比真实路径快，
// 等于没做。
var dummyHash = []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")

// validHash 报告这个 hash 能不能喂给 bcrypt 比对。
//
// 存在的意义：库里若存了**非 bcrypt 格式**的串（历史遗留、迁移事故、
// 或被手工改过），直接传给 CompareHashAndPassword 会返回
// ErrHashTooShort 之类的错误——那个错误里会带上输入串的长度信息。
// 更要紧的是：那样会让「密码错」这条路径**不执行** bcrypt 耗时，
// 于是「账号存在但 hash 损坏」比「账号不存在」还快，同样是枚举信道。
// 所以先判格式，判不过就走与「不存在」相同的陪衬分支。
func validHash(h string) bool {
	// bcrypt hash 形如 $2a$10$<53 字符>；costs / salt+hash 长度是固定的。
	// 这里只做长度与前缀的粗判，足够挡住非 bcrypt 输入。
	if len(h) != 60 {
		return false
	}
	return strings.HasPrefix(h, "$2a$") || strings.HasPrefix(h, "$2b$") || strings.HasPrefix(h, "$2y$")
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	// 账号级退避（2026-10-06，N2）。放在查库之前：被退避住的请求应当
	// **一次 bcrypt 都不花**——否则「退避」只是把爆破窗口拉长，
	// 攻击者仍能拿满 CPU。
	if d := s.loginBackoff.RetryAfter(req.Username); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts")
		return
	}

	var hash, role string
	err := s.db.QueryRow(`SELECT password_hash, role FROM users WHERE username = ?`, req.Username).Scan(&hash, &role)
	// 用户不存在与密码错误返回同一句，避免用户名枚举。
	// ⚠️ 且两者必须**同样耗时**：写 `errors.Is(err, sql.ErrNoRows) || bcrypt.Compare…`
	// 会在账号不存在时短路掉 bcrypt（hash 是空的，比对必然失败），
	// 于是「不存在」快、「存在但密码错」慢——响应时间就成了账号枚举信道。
	// 故这里把 hash 兜底成 dummyHash，让两个分支都真跑一次 bcrypt。
	compareTo := hash
	if errors.Is(err, sql.ErrNoRows) {
		compareTo = string(dummyHash)
	}
	if !validHash(compareTo) || bcrypt.CompareHashAndPassword([]byte(compareTo), []byte(req.Password)) != nil {
		if d := s.loginBackoff.Fail(req.Username); d > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
			writeErr(w, http.StatusTooManyRequests, "too many failed attempts")
			return
		}
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	s.loginBackoff.Success(req.Username)
	tok, err := s.newToken(req.Username, role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func (s *Server) authWhoami(w http.ResponseWriter, r *http.Request) {
	u := authInfoOf(r)
	// 以库里的 role 为准（可能被管理员改过），token 里的 role 只作兜底。
	role := u.role
	if err := s.db.QueryRow(`SELECT role FROM users WHERE username = ?`, u.username).Scan(&role); err != nil {
		role = u.role
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": u.username, "role": role})
}

func (s *Server) authList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT username, role FROM users ORDER BY id`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	type u struct {
		Username string `json:"username"`
		Role     string `json:"role"`
	}
	out := []u{} // 初始化为空切片而非 nil：空库时序列化成 [] 而非 null
	for rows.Next() {
		var x u
		if rows.Scan(&x.Username, &x.Role) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}

// ---- relay ----

func (s *Server) relayRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID    string  `json:"peer_id"`
		Addrs     any     `json:"addrs"`
		StorageMB int64   `json:"storage_mb"`
		LoadPct   float64 `json:"load_pct"`
		Version   string  `json:"version"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if strings.TrimSpace(req.PeerID) == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}
	addrs := stringifyAddrs(req.Addrs)
	// 幂等：同一 peer_id 重复注册即更新（顺带刷新心跳）。
	if _, err := s.db.Exec(`INSERT INTO relay_nodes(peer_id, addrs, storage_mb, load_pct, version)
		VALUES(?, ?, ?, ?, ?)
		ON CONFLICT(peer_id) DO UPDATE SET
			addrs=excluded.addrs, storage_mb=excluded.storage_mb,
			load_pct=excluded.load_pct, version=excluded.version,
			last_heartbeat=CURRENT_TIMESTAMP`,
		req.PeerID, addrs, req.StorageMB, req.LoadPct, req.Version); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "registered"})
}

func (s *Server) relayHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.PeerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}
	_, _ = s.db.Exec(`UPDATE relay_nodes SET last_heartbeat = CURRENT_TIMESTAMP WHERE peer_id = ?`, req.PeerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) relayList(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.Query(`SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat FROM relay_nodes`)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	defer rows.Close()
	type node struct {
		PeerID        string  `json:"peer_id"`
		Addrs         any     `json:"addrs"`
		StorageMB     int64   `json:"storage_mb"`
		LoadPct       float64 `json:"load_pct"`
		Version       string  `json:"version"`
		RegisteredAt  string  `json:"registered_at"`
		LastHeartbeat string  `json:"last_heartbeat"`
	}
	out := []node{}
	for rows.Next() {
		var n node
		var addrs string
		if rows.Scan(&n.PeerID, &addrs, &n.StorageMB, &n.LoadPct, &n.Version, &n.RegisteredAt, &n.LastHeartbeat) != nil {
			continue
		}
		n.Addrs = parseAddrs(addrs)
		out = append(out, n)
	}
	writeJSON(w, http.StatusOK, map[string]any{"relays": out})
}

// stringifyAddrs 把 addrs 规整成入库用的字符串（数组则 JSON 编码）。纯函数。
func stringifyAddrs(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

// parseAddrs 把库里存的 addrs 还原成 JSON 形态：数组则回数组，否则原样字符串。
// 纯函数，不依赖 Server（参数名曾与 receiver 冲突，故不做方法）。
func parseAddrs(raw string) any {
	if raw == "" {
		return nil
	}
	var arr []string
	if json.Unmarshal([]byte(raw), &arr) == nil {
		return arr
	}
	return raw
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

// Handler 返回带全部 9 条路由的 http.Handler。
//
// 路由与原独立仓逐条一致，改这里等于改原仓的实现——
// peerdrive 的 auth_middleware.go 依赖 GET /auth/whoami，动了会静默改鉴权行为。
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
	return mux
}

// Serve 在 addr 上启动并阻塞，直到出错。
// 只给 certFile 或 keyFile 其一即报错——静默降级成明文会让「以为在跑 HTTPS」
// 的部署上线，代价远大于启动失败。
func (s *Server) Serve(addr, certFile, keyFile string) error {
	h := s.Handler()
	if certFile != "" && keyFile != "" {
		srv := &http.Server{Addr: addr, Handler: h}
		return srv.ListenAndServeTLS(certFile, keyFile)
	}
	if certFile != "" || keyFile != "" {
		return fmt.Errorf("TLS requires both cert and key (cert=%q key=%q)", certFile, keyFile)
	}
	return http.ListenAndServe(addr, h)
}
