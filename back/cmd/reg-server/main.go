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

package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	_ "modernc.org/sqlite" // 驱动名由 driver_cgo.go / driver_pure.go 注册
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

var jwtSecret []byte

const tokenTTL = 72 * time.Hour // 与原服务一致：旧 token 实测 exp-iat=259200s（3 天）

func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newToken(username, role string) (string, error) {
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
	return newTokenFrom(hb, pb)
}

// newTokenFrom 给定 header/payload 字节拼出 HS256 签名串。拆出来是为了让
// 测试能构造「已过期」「异密钥」等 token，而不必伪造时间。
func newTokenFrom(hb, pb []byte) (string, error) {
	h := b64url(hb)
	p := b64url(pb)
	mac := hmac.New(sha256.New, jwtSecret)
	mac.Write([]byte(h + "." + p))
	return h + "." + p + "." + b64url(mac.Sum(nil)), nil
}

// verifyToken 验签并提取 claims；返回 (username, role, error)。
func verifyToken(tok string) (string, string, error) {
	const bad = "invalid or expired token"
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", "", errors.New(bad)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", errors.New(bad)
	}
	mac := hmac.New(sha256.New, jwtSecret)
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
func authRequired(next http.HandlerFunc) http.HandlerFunc {
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
		user, role, err := verifyToken(parts[1])
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
// 数据库

var db *sql.DB

// openDB 打开（必要时创建）库。表缺失时按原 schema 创建；沿用既有库时
// CREATE TABLE IF NOT EXISTS 不会动已有表，故旧数据与旧 token 均兼容。
func openDB(path string) error {
	d, err := sql.Open(sqliteDriver, path+dsnSuffix())
	if err != nil {
		return err
	}
	if err := d.Ping(); err != nil {
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
	for _, s := range stmts {
		if _, err := d.Exec(s); err != nil {
			return err
		}
	}
	db = d
	return nil
}

// ---------------------------------------------------------------------------
// handlers

func apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func apiPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "peerdrive-registration", "status": "ok"})
}

func authRegister(w http.ResponseWriter, r *http.Request) {
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
	if _, err := db.Exec(`INSERT INTO users(username, password_hash, role) VALUES(?, ?, 'user')`,
		req.Username, string(hash)); err != nil {
		writeErr(w, http.StatusConflict, "username already exists")
		return
	}
	tok, err := newToken(req.Username, "user")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": req.Username, "token": tok})
}

func authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var hash, role string
	err := db.QueryRow(`SELECT password_hash, role FROM users WHERE username = ?`, req.Username).Scan(&hash, &role)
	// 用户不存在与密码错误返回同一句，避免用户名枚举。
	if errors.Is(err, sql.ErrNoRows) || bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	tok, err := newToken(req.Username, role)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "internal error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func authWhoami(w http.ResponseWriter, r *http.Request) {
	u := authInfoOf(r)
	// 以库里的 role 为准（可能被管理员改过），token 里的 role 只作兜底。
	role := u.role
	if err := db.QueryRow(`SELECT role FROM users WHERE username = ?`, u.username).Scan(&role); err != nil {
		role = u.role
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": u.username, "role": role})
}

func authList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT username, role FROM users ORDER BY id`)
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

func relayRegister(w http.ResponseWriter, r *http.Request) {
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
	if _, err := db.Exec(`INSERT INTO relay_nodes(peer_id, addrs, storage_mb, load_pct, version)
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

func relayHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PeerID string `json:"peer_id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.PeerID == "" {
		writeErr(w, http.StatusBadRequest, "peer_id is required")
		return
	}
	_, _ = db.Exec(`UPDATE relay_nodes SET last_heartbeat = CURRENT_TIMESTAMP WHERE peer_id = ?`, req.PeerID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func relayList(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`SELECT peer_id, addrs, storage_mb, load_pct, version, registered_at, last_heartbeat FROM relay_nodes`)
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

func stringifyAddrs(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if b, err := json.Marshal(v); err == nil {
		return string(b)
	}
	return ""
}

func parseAddrs(s string) any {
	if s == "" {
		return nil
	}
	var arr []string
	if json.Unmarshal([]byte(s), &arr) == nil {
		return arr
	}
	return s
}

// ---------------------------------------------------------------------------
// main

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "4000"
	}
	host := os.Getenv("HOST") // 指定监听 IP；空 = 全网卡（与原服务一致）
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = os.Getenv("PEERDRIVE_REG_DB") // 主仓风格别名
	}
	if dbPath == "" {
		dbPath = "./reg.db"
	}
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		fmt.Fprintln(os.Stderr, "JWT_SECRET is required")
		os.Exit(1)
	}
	jwtSecret = []byte(secret)

	if err := openDB(dbPath); err != nil {
		fmt.Fprintf(os.Stderr, "db open failed: %v\n", err)
		os.Exit(1)
	}

	addr := ":" + port
	if host != "" {
		addr = net.JoinHostPort(host, port)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ping", apiPing)
	mux.HandleFunc("GET /api/health", authRequired(apiHealth))
	mux.HandleFunc("POST /auth/register", authRegister)
	mux.HandleFunc("POST /auth/login", authLogin)
	mux.HandleFunc("GET /auth/whoami", authRequired(authWhoami))
	mux.HandleFunc("GET /auth/list", authRequired(authList))
	mux.HandleFunc("POST /p2p/relay/register", relayRegister)
	mux.HandleFunc("POST /p2p/relay/heartbeat", relayHeartbeat)
	mux.HandleFunc("GET /p2p/relay/list", relayList)

	fmt.Printf("Registration server starting on %s (db=%s)\n", addr, dbPath)
	if err := http.ListenAndServe(addr, mux); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start: %v\n", err)
		os.Exit(1)
	}
}
