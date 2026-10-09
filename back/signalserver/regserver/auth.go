package regserver

// auth.go — JSON 响应工具 + Bearer 认证中间件。
//
// 这两个工具是 regserver 的横切基础设施：
//   - writeJSON / writeErr：所有 handler 都调，统一 Content-Type 与错误文案。
//   - authRequired：net/http 版中间件（不依赖 gin）——这是 regserver 能挂到
//     services.UnifiedMux 的前提。peerdrive 主服务用的 auth_middleware.go 是
//     gin 版，两者语义一致但实现独立（gin 依赖只该出现在 router 包内）。
//
// 身份通过 context.Value 传递（authCtxKey 是未导出类型，防外部包伪造）。
// authInfoOf 在 handler 里读；未经中间件时返回零值，调用方按空字符串处理。

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// writeJSON 统一 JSON 响应：Content-Type 带 charset，写 status 再 encode。
// 用 NewEncoder 而非 Marshal+Write，是为了让前端能直接读 body（无尾部换行差异）。
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// writeErr 是所有错误响应的统一形状 {"error": "..."}。
// 文案要稳定：前端、CI 断言都依赖它（见 TestLoginRejectsWrongPassword）。
func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// authCtxKey 是 context 里放身份的 key。
// 用未导出 struct 类型而不是 string：防止外部包用 string key 塞一个伪造的
// authInfo——那会让调用方绕过 authRequired 直接构造身份。
type authCtxKey struct{}

// authInfo 是 authRequired 注入的身份。role 可能被 authWhoami 用库里的值覆盖，
// 故分开存，不要合并成一个字符串。
type authInfo struct{ username, role string }

// authRequired 从 Authorization: Bearer 头取 token 验证；失败 401。
//
// 文案与原服务逐字一致（"missing authorization header" / "invalid or expired
// token"）——前端 auth_middleware.go 的调用方按文案分支，改了会静默改行为。
//
// 只认 "Bearer "（空格分隔、scheme 大小写不敏感）。Basic / 无 scheme / 多空格
// 一律 401，不尝试猜测。
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
// 调用方应检查 username 是否为空再使用（authWhoami 就这么做）。
func authInfoOf(r *http.Request) authInfo {
	if v, ok := r.Context().Value(authCtxKey{}).(authInfo); ok {
		return v
	}
	return authInfo{}
}
