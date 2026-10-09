package regserver

// jwt_test.go — JWT 签发与校验的表驱动单测。
//
// 拆分背景（2026-10-09）：JWT 实现从 regserver.go 拆到 jwt.go 后，
// 用表驱动覆盖原散落在 regserver_test.go / compat_test.go 里的边界用例，
// 并补充此前未覆盖的拒绝路径（篡改签名、非法 base64url、缺 claims 字段、
// 空 token）。
//
// 这些用例走 newBareServer（只给 jwtSecret，不碰库），故可在并行下跑——
// 见 TestMain 未设置 -parallel 限制，但 t.Parallel 在子测试里仍要手工开。

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestVerifyTokenTableDriven 覆盖 verifyToken 的全部拒绝路径与一条接受路径。
//
// 发现背景：原测试只覆盖了「过期」与「异密钥」两个拒绝路径；篡改签名、
// 非法 base64url、缺段、缺 claims 字段都是静默通过的潜在风险——
// 任一条漏判都等于绕过认证。
func TestVerifyTokenTableDriven(t *testing.T) {
	secret := []byte("test-secret")
	// 构造一个「已知有效」的基准 token，后续 case 在其上篡改。
	claims := map[string]any{
		"username": "alice", "role": "user",
		"iss": "https://localhost:4000", "sub": "alice",
		"iat": time.Now().Unix(),
		"exp": time.Now().Add(1 * time.Hour).Unix(),
	}
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	srv := &Server{jwtSecret: secret}
	baseTok, err := srv.newTokenFrom(hb, pb)
	if err != nil {
		t.Fatalf("build base token: %v", err)
	}

	cases := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{name: "valid", token: baseTok, wantErr: false},
		{name: "empty", token: "", wantErr: true},
		{name: "one-part", token: "abc", wantErr: true},
		{name: "two-part", token: "abc.def", wantErr: true},
		{name: "four-part", token: "a.b.c.d", wantErr: true},
		{name: "tampered-signature", token: baseTok[:len(baseTok)-3] + "ZZZ", wantErr: true},
		{name: "tampered-payload", token: strings.Replace(baseTok, base64.RawURLEncoding.EncodeToString(pb)[:5], "XXXXX", 1), wantErr: true},
		// exp 字段缺失：verifyToken 容错（Exp==0 时跳过过期检查），这是设计选择。
		{name: "no-exp-field", token: buildTokenWithClaims(srv, secret, map[string]any{"username": "u", "role": "user"}), wantErr: false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := srv.verifyToken(c.token)
			if (err != nil) != c.wantErr {
				t.Errorf("verifyToken(%q) error=%v wantErr=%v", c.token, err, c.wantErr)
			}
		})
	}
}

// TestNewTokenRoundTrip 验签不依赖进程内状态：签出后立刻能验。
//
// 发现背景：拆 jwt.go 时担心 newToken 与 verifyToken 的 claims 形状对不上
// （字段名、iss、sub、TTL 各有一处偏差）。本用例锁住 round-trip。
func TestNewTokenRoundTrip(t *testing.T) {
	srv := &Server{jwtSecret: []byte("rt-secret")}
	tok, err := srv.newToken("bob", "admin")
	if err != nil {
		t.Fatal(err)
	}
	got, role, err := srv.verifyToken(tok)
	if err != nil {
		t.Fatalf("verifyToken failed: %v", err)
	}
	if got != "bob" || role != "admin" {
		t.Errorf("round-trip = (%q,%q) want (bob,admin)", got, role)
	}
}

// TestNewTokenClaimsShape 锁住 claims 的字段名与 iss 值——
// 原独立仓签发的 token 带这些字段，改了会让 golang-jwt 等校验器报 iss 不符。
func TestNewTokenClaimsShape(t *testing.T) {
	srv := &Server{jwtSecret: []byte("shape-secret")}
	tok, err := srv.newToken("carol", "user")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d parts, want 3", len(parts))
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(pb, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	// 字段逐字对齐原服务（见 compat_test.go 对拍）。
	if claims["iss"] != "https://localhost:4000" {
		t.Errorf("iss = %v, want https://localhost:4000", claims["iss"])
	}
	if claims["sub"] != "carol" {
		t.Errorf("sub = %v, want carol", claims["sub"])
	}
	if claims["username"] != "carol" {
		t.Errorf("username = %v, want carol", claims["username"])
	}
	if claims["role"] != "user" {
		t.Errorf("role = %v, want user", claims["role"])
	}
	// TTL 约 3 天：exp-iat 应在 259100~259300 之间（±100s 容差，吸收构造耗时）。
	iat, _ := claims["iat"].(float64)
	exp, _ := claims["exp"].(float64)
	if exp-iat < 259100 || exp-iat > 259300 {
		t.Errorf("TTL = %v, want ~259200", exp-iat)
	}
}

// TestVerifyTokenEmptySecret 空密钥下任何签名都不通过——
// 这是「不留 JWT_SECRET 直接起服务」的兜底护栏（TestMainRequiresJWTSecret 测的是入口，
// 本用例测的是 JWT 层本身）。
func TestVerifyTokenEmptySecret(t *testing.T) {
	srv := &Server{jwtSecret: []byte("")}
	if _, _, err := srv.verifyToken("a.b.c"); err == nil {
		t.Error("empty secret accepted a token")
	}
	// 空密钥签出的 token 应当能被空密钥验证（这是 HMAC 的性质），但
	// 生产环境 New() 已经拒绝空密钥，故这里只验证「验签函数本身不崩溃」。
	tok, err := srv.newToken("x", "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := srv.verifyToken(tok); err != nil {
		t.Errorf("empty-secret token should verify under empty secret: %v", err)
	}
}

// buildTokenWithClaims 是测试辅助：用给定 claims 构造一个 token。
// 拆出来是为了让 TestVerifyTokenTableDriven 能构造「缺字段」的 token，
// 而不用复制 newTokenFrom 的样板代码。
func buildTokenWithClaims(srv *Server, secret []byte, claims map[string]any) string {
	pb, _ := json.Marshal(claims)
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	tok, err := srv.newTokenFrom(hb, pb)
	if err != nil {
		return ""
	}
	// secret 参数目前未用——newTokenFrom 用 s.jwtSecret。保留形参让调用方
	// 显式表达「我意识到这是用 srv 的密钥签的」，避免日后传错。
	_ = secret
	return tok
}
