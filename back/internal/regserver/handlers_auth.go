package regserver

// handlers_auth.go — 认证相关的 HTTP handler（注册 / 登录 / whoami / list / ping / health）。
//
// 与 handlers_relay.go 对称：两个业务线各自的 HTTP 入口，共享本包的 JSON 工具
// 与 authRequired 中间件（见 auth.go）。业务逻辑尽量薄——真正的安全关键路径
// （bcrypt 比对、JWT 签发）抽到 jwt.go 与本文件的 small helpers（dummyHash /
// validHash）以便单测。
//
// 三个端点的「无认证」是既有对外行为（不能改），所以速率限制（Handler 装配处）
// 与登录退避（loginBackoff）是唯一的防线。

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// apiHealth 与 apiPing 是探活端点。
//
// /ping 无认证：让负载均衡器/监控能直接探活，不必先注册账号。
// /api/health 需 Bearer：暴露「库连通性」这一内部信息，故走 authRequired。
//
// 返回体文案被前端与 CI 依赖，不要改。
func (s *Server) apiHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) apiPing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"service": "peerdrive-registration", "status": "ok"})
}

// authRegister 注册新用户，返回 token。
//
// 安全设计（与原服务一致）：
//   - bcrypt cost 10：与原服务及既有库一致。改大会让旧 hash 仍可验（bcrypt 自带
//     cost 前缀）但新登录变慢，故保持不动。
//   - 重名 409：INSERT 冲突即返回，不让调用方拿到「是否已存在」之外的信息。
//   - 注册即签 token：省一次登录往返，但 token 只给一次（前端应立即持久化）。
func (s *Server) authRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Username) == "" || req.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
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

// authLogin 登录并签发 token。
//
// 账号枚举防护（与原服务一致）：
//   - 用户不存在与密码错误返回同一句 "invalid username or password"。
//   - **两者必须同样耗时**：把 hash 兜底成 dummyHash，让两个分支都真跑一次
//     bcrypt。写 `errors.Is(err, sql.ErrNoRows) || bcrypt.Compare...` 会在账号
//     不存在时短路掉 bcrypt，于是「不存在」快、「存在但密码错」慢——响应时间
//     就成了账号枚举信道。
//   - validHash 先判格式：损坏的 hash 不该让 bcrypt 抛错（错误信息会泄露长度），
//     也不该让它跑比 dummyHash 更快的路径。
//
// 退避放在查库之前：被退避住的请求应当**一次 bcrypt 都不花**——否则「退避」
// 只是把爆破窗口拉长，攻击者仍能拿满 CPU。
func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if d := s.loginBackoff.RetryAfter(req.Username); d > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts")
		return
	}

	var hash, role string
	err := s.db.QueryRow(`SELECT password_hash, role FROM users WHERE username = ?`, req.Username).Scan(&hash, &role)
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

// authWhoami 返回当前 token 对应用户的 username 与 role。
//
// role 以库为准：管理员可能改了某个用户的 role，token 里缓存的 role 不该被信。
// 库查不到（用户被删）时回退 token 里的 role——此时 username 也已失效，
// 调用方应把响应当作「用户已不存在」处理。
func (s *Server) authWhoami(w http.ResponseWriter, r *http.Request) {
	u := authInfoOf(r)
	role := u.role
	if err := s.db.QueryRow(`SELECT role FROM users WHERE username = ?`, u.username).Scan(&role); err != nil {
		role = u.role
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": u.username, "role": role})
}

// authList 列出所有用户。
//
// 空库返回 [] 而非 null：前端常直接 .map()，null 会抛。
// 这里不做分页——用户量级很小（运营者/管理员），全量返回更简单。
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
	out := []u{}
	for rows.Next() {
		var x u
		if rows.Scan(&x.Username, &x.Role) == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out})
}
