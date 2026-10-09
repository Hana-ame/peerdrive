package regserver

// jwt.go — JWT 签发与校验（HS256，手写实现）。
//
// 为什么不用 golang-jwt：兼容性要求「同密钥下旧 token 直接可验」。手写实现
// 与原服务同构，claims 的字段名、iss、TTL 都一致；换库则要逐项对齐且多一处
// 依赖。安全属性（HS256、hmac.Equal 常数时间比较、exp 校验）保持原样。
//
// 这一层是纯函数式的（只读 s.jwtSecret），可脱离 Server/db 单测——
// newBareServer 只需给 jwtSecret，不碰库。字节级兼容由 compat_test.go 与
// compat_old_test.go 对拍原实现锁住。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// tokenTTL 与原服务一致：旧 token 实测 exp-iat=259200s（3 天）。
const tokenTTL = 72 * time.Hour

// b64url 是 base64url 编码的薄封装。拆成函数是为了让 compat_old_test.go 的
// origB64url 与之对拍；两侧必须用同一个编码表，否则字节级一致就破。
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// newToken 用当前时间签一个 JWT，claims 字段与原服务逐字段一致。
// iss 写死 "https://localhost:4000" 是为了兼容既有 token——已签发 token 带此值，
// 改了会让存量 token 校验失败（虽然 iss 当前没被校验，但字段必须存在且一致，
// 因为 golang-jwt 等校验器会读它）。
func (s *Server) newToken(username, role string) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"username": username,
		"role":     role,
		"iss":      "https://localhost:4000",
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

// newTokenFrom 给定 header/payload 字节拼出 HS256 签名串。
// 拆出来是为了让测试能构造「已过期」「异密钥」等 token，而不必伪造时间。
// 签名输入是 "<header-b64>.<payload-b64>"，签名结果 base64url 后拼在第三段——
// 与 RFC 7515 的 JWS Compact Serialization 同构。
func (s *Server) newTokenFrom(hb, pb []byte) (string, error) {
	h := b64url(hb)
	p := b64url(pb)
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(h + "." + p))
	return h + "." + p + "." + b64url(mac.Sum(nil)), nil
}

// verifyToken 验签并提取 claims；返回 (username, role, error)。
//
// 拒绝路径文案统一 "invalid or expired token"——避免通过错误消息泄露
// 「格式错 / 签名错 / 过期」的区别，那会让攻击者能区分哪些 token 是「差一点
// 就对」的，从而缩小爆破空间。
//
// 常数时间比较：hmac.Equal 防时序侧信道，不要用 ==。
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
	if !hmac.Equal(sig, mac.Sum(nil)) {
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
