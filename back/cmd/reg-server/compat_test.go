package main

// 迁移兼容性的核心断言：新实现在同密钥下必须与**原独立仓**逐字节一致地签发
// token，并且能验原实现签出的 token。
//
// 为什么必须字节级一致：peerdrive 生产环境里已经存在一批由旧 reg-server
// 签发的 token（peerdrive 的 auth_middleware.go 靠 GET /auth/whoami 校验它们）。
// 换实现后哪怕只是 claims 字段顺序变一点，那些 token 全部 401，等于把在线
// 用户全部踢下线——而这种破坏只在生产暴露，本地测不出来。

import (
	"encoding/json"
	"testing"
	"time"
)

// TestTokenByteIdenticalToOriginal 对拍签发结果。
func TestTokenByteIdenticalToOriginal(t *testing.T) {
	secret := []byte("shared-secret")
	cases := []struct{ u, r string }{
		{"alice", "user"}, {"admin", "admin"}, {"bob", "user"},
		{"名字带空格", "user"}, {"with-dash_and.dot", "user"},
	}
	for _, c := range cases {
		// 固定时刻，消掉 iat/exp 的时间漂移。
		now := time.Unix(1750000000, 0)
		old, err := origNewToken(secret, c.u, c.r, now)
		if err != nil {
			t.Fatalf("orig sign %s: %v", c.u, err)
		}

		jwtSecret = secret
		claims := map[string]any{
			"username": c.u, "role": c.r,
			"iss": "https://localhost:4000", "sub": c.u,
			"iat": now.Unix(), "exp": now.Add(tokenTTL).Unix(),
		}
		hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
		pb, err := json.Marshal(claims)
		if err != nil {
			t.Fatal(err)
		}
		got, err := newTokenFrom(hb, pb)
		if err != nil {
			t.Fatalf("new sign %s: %v", c.u, err)
		}

		if got != old {
			t.Errorf("token bytes differ for %s/%s\n new=%s\n old=%s", c.u, c.r, got, old)
		}
	}
}

// TestNewVerifiesOldTokens 生产已有旧 token：新实现必须仍能验出正确 claims。
func TestNewVerifiesOldTokens(t *testing.T) {
	secret := []byte("prod-like-secret")
	jwtSecret = secret
	for _, c := range []struct{ u, r string }{{"alice", "user"}, {"admin", "admin"}} {
		old, err := origNewToken(secret, c.u, c.r, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		u, role, err := verifyToken(old)
		if err != nil {
			t.Fatalf("new impl rejected a token signed by the original impl (%s): %v", c.u, err)
		}
		if u != c.u || role != c.r {
			t.Errorf("claims = %s/%s, want %s/%s", u, role, c.u, c.r)
		}
		// 反向：新签的，旧实现也得能验（灰度切换期两个版本并存）。
		jwtSecret = secret
		got, _ := newToken(c.u, c.r)
		if ou, orr, err := origVerifyToken(secret, got); err != nil || ou != c.u || orr != c.r {
			t.Errorf("original impl rejected a token signed by new impl (%s): %s/%s err=%v", c.u, ou, orr, err)
		}
	}
}

// TestOldAndNewAgreeOnRejects 拒绝行为也要一致，否则灰度期会出现
// 「旧版本放行、新版本拒绝」的诡异差异。
func TestOldAndNewAgreeOnRejects(t *testing.T) {
	secret := []byte("s")
	jwtSecret = secret
	good, _ := origNewToken(secret, "u", "user", time.Now())
	other, _ := origNewToken([]byte("different"), "u", "user", time.Now())
	expired, _ := origNewToken(secret, "u", "user", time.Now().Add(-100*time.Hour))

	for _, tc := range []struct {
		name string
		tok  string
	}{
		{"not-three-parts", "a.b"},
		{"garbage", "garbage"},
		{"signed-with-other-secret", other},
		{"expired", expired},
	} {
		_, _, nerr := verifyToken(tc.tok)
		_, _, oerr := origVerifyToken(secret, tc.tok)
		if (nerr == nil) != (oerr == nil) {
			t.Errorf("%s: new err=%v, old err=%v — behaviour diverged", tc.name, nerr, oerr)
		}
	}
	if _, _, err := verifyToken(good); err != nil {
		t.Errorf("sanity: new impl rejected a valid token: %v", err)
	}
}
