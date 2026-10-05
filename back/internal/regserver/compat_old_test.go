package regserver

// 本文件持有**原独立仓的原始实现**（逐行照抄 registration-server/main.go，
// 函数加 orig 前缀避免与本包重名），只用于对拍：证明新实现在同密钥下
// 签出与旧实现逐字节相同的 token，且能验旧 token。
// 若哪天真要删这份拷贝，先想清楚谁在证明「旧 token 还能用」。

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

func origB64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func origNewToken(secret []byte, username, role string, now time.Time) (string, error) {
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
	h := origB64url(hb)
	p := origB64url(pb)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(h + "." + p))
	return h + "." + p + "." + origB64url(mac.Sum(nil)), nil
}

func origVerifyToken(secret []byte, tok string) (string, string, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return "", "", errors.New("invalid or expired token")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return "", "", errors.New("invalid or expired token")
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(parts[0] + "." + parts[1]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", "", errors.New("invalid or expired token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New("invalid or expired token")
	}
	var claims struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Exp      int64  `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", "", errors.New("invalid or expired token")
	}
	if claims.Exp != 0 && time.Now().Unix() > claims.Exp {
		return "", "", errors.New("invalid or expired token")
	}
	return claims.Username, claims.Role, nil
}
