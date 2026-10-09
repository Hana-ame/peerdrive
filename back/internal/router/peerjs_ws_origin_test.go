package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// TestWSPeer_OriginPolicyEndToEnd is the seam test for the wsconn split: it
// proves that the origin allowlist in the node config reaches the real
// /ws/peer handshake and that a successful handshake still registers a local
// session.
//
// 发现背景：拆分把 /ws/peer 的内联 websocket.Upgrader 换成了
// wsconn.NewUpgrader(wsconn.OriginPolicy{Allow: peerjsCfg.IsOriginAllowed})。
// 判定逻辑本身已在 wsconn 的 TestOriginPolicy_CheckOrigin 里逐条钉住，这里
// 钉的是 router → config → wsconn 这条接线：nil peerjsCfg 是否变成
// 「无白名单 = 放行」，以及握手成功后会话是否仍然挂上服务。
//
// 错误响应体不在此断言：gorilla 的 Upgrade 失败时会自己先写 http.Error，
// handler 随后的 c.JSON 是重复写。这是拆分前的既有行为，保持逐字一致，
// 不「修复」。
func TestWSPeer_OriginPolicyEndToEnd(t *testing.T) {
	t.Parallel()

	mkSvc := func(allowedOrigins string) (*transport.PeerJSService, *Router, *httptest.Server) {
		t.Helper()
		cfg := testCfg(func(c *config.Config) {
			c.PeerJSHost = "signal.example"
			c.PeerJSPort = "443"
			c.AllowedOrigins = allowedOrigins
		})
		svc := transport.NewPeerJSService(cfg, defaultTestStorageDir)
		t.Cleanup(svc.Close)

		rt, err := NewRouter(Deps{Cfg: cfg, PeerJSService: svc, PeerJSCfg: cfg})
		require.NoError(t, err)

		srv := httptest.NewServer(rt.Engine())
		t.Cleanup(srv.Close)
		return svc, rt, srv
	}

	wsURL := func(srv *httptest.Server) string {
		return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/peer"
	}

	dial := func(srv *httptest.Server, origin string) (*websocket.Conn, *http.Response, error) {
		d := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
		h := http.Header{}
		if origin != "" {
			h.Set("Origin", origin)
		}
		return d.Dial(wsURL(srv), h)
	}

	// 1. Origin in the allowlist → upgrade succeeds, local session bound.
	svcA, rtA, srvA := mkSvc("http://panel.local")
	t.Run("allowlisted origin is accepted and binds a local session", func(t *testing.T) {
		u, _, err := dial(srvA, "http://panel.local")
		require.NoError(t, err, "an allowlisted Origin must be upgraded")
		if !awaitLocalSession(t, svcA) {
			u.Close()
			t.Fatalf("no local session was registered; connections = %v", svcA.Connections())
		}
		u.Close()
	})

	// 2. Origin outside the allowlist → the handshake is refused. gorilla
	// writes the 403 itself, before the handler's c.JSON.
	_, _, srvB := mkSvc("http://panel.local")
	t.Run("non-allowlisted origin is refused", func(t *testing.T) {
		u, resp, err := dial(srvB, "http://evil.example")
		if err == nil {
			u.Close()
			t.Fatal("a non-allowlisted Origin was upgraded")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %v, want 403", statusOf(resp))
		}
	})

	// 3. No Origin at all: the dialer does not add one, so this takes the
	// IsLoopbackRemote branch. The httptest server is on 127.0.0.1, so it
	// must be accepted.
	svcC, _, srvC := mkSvc("*")
	t.Run("origin-less request from loopback is accepted", func(t *testing.T) {
		u, _, err := dial(srvC, "")
		require.NoError(t, err, "an Origin-less 127.0.0.1 handshake must be accepted")
		if !awaitLocalSession(t, svcC) {
			u.Close()
			t.Fatalf("no local session was registered; connections = %v", svcC.Connections())
		}
		u.Close()
	})

	// 4. Allowlist configured, but the request carries no Origin and comes
	// from a non-loopback address → refused by IsLoopbackRemote. This is the
	// security-critical branch: a script on the LAN must not get an admin
	// session. The handshake headers are all present so gorilla reaches the
	// origin check instead of failing on a missing token first.
	//
	// The body is the pre-existing double-write, kept byte-for-byte: gorilla
	// writes `http.Error(w, "Forbidden", 403)` and then the handler writes
	// its own JSON on top. The 403 wins because it is written first. Do not
	// "fix" this by dropping the c.JSON — that changes the response body.
	// The router's JSON is still observable in the body, which is what the
	// assertions below pin.
	_, rtD, _ := mkSvc("http://panel.local")
	t.Run("origin-less request from a remote address is refused", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ws/peer", nil)
		req.RemoteAddr = "203.0.113.7:4444"
		setHandshakeHeaders(req)
		rtD.Engine().ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Fatalf("status = %d body=%q, want 403 (origin not allowed)", w.Code, w.Body.String())
		}
		assert.Contains(t, w.Body.String(), "Forbidden", "gorilla's own error text")
		assert.Contains(t, w.Body.String(), `{"error":"websocket upgrade failed"}`,
			"the router's error body must still be there — this double-write is pre-existing")
	})

	// 4b. Same request shape, loopback address → accepted, which isolates the
	// decision to IsLoopbackRemote rather than to the allowlist.
	_, rtDLoop, _ := mkSvc("http://panel.local")
	t.Run("origin-less request from loopback passes the same gate", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ws/peer", nil)
		req.RemoteAddr = "127.0.0.1:4444"
		setHandshakeHeaders(req)
		rtDLoop.Engine().ServeHTTP(w, req)

		// A recorder cannot report the 101 gorilla writes to a hijacked
		// connection, so only the rejection direction is observable here:
		// 403 means the origin gate refused, anything else means it passed.
		if w.Code == http.StatusForbidden {
			t.Fatalf("loopback was rejected by the origin gate: status=%d body=%q",
				w.Code, w.Body.String())
		}
	})

	// 5. No allowlist configured at all → the historical default, accept.
	// peerjsCfg is passed, so this exercises the IsOriginAllowed("") == true
	// branch of config.Config.
	_, _, srvE := mkSvc("")
	t.Run("empty allowlist accepts any origin", func(t *testing.T) {
		u, _, err := dial(srvE, "http://anything.example")
		require.NoError(t, err, "an empty allowlist must accept every Origin")
		u.Close()
	})

	// 6. A plain GET without any handshake headers → 400, before the origin
	// check is even reached.
	t.Run("a request without handshake headers is refused", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ws/peer", nil)
		req.RemoteAddr = "127.0.0.1:4444"
		rtA.Engine().ServeHTTP(w, req)
		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "Bad Request")
		assert.Contains(t, w.Body.String(), `{"error":"websocket upgrade failed"}`)
	})
}

// awaitLocalSession 轮询等 /ws/peer 的 handler 完成 NewWSSession + BindLocal，
// 返回是否等到「local」这条会话。
//
// 发现背景：gorilla 的 Upgrade 一写完 101，客户端的 websocket.Dial 就返回了，
// 而 NewWSSession + BindLocal 还在 handler 里排队。原先写成
// 「dial → u.Close() → 立刻读 Connections()」，两头都是竞态：darwin amd64 CI
// 上红过一次（"no local session was registered; connections = map[]"）；而且
// 反过来也不安全——先 Close 再查，服务端读循环可能已经退出、把会话注销了，
// 于是「明明注册过」也会被读成空。
//
// 所以顺序必须是「先等到会话挂上（此时连接还开着，注册项不会消失）再 Close」。
// 真没注册时最多等 3 秒再报错。
func awaitLocalSession(t *testing.T, svc *transport.PeerJSService) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := svc.Connections()["local"]; ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// setHandshakeHeaders makes a plain request look like a WebSocket handshake
// so gorilla reaches the origin check. Without these it fails earlier, on a
// missing token, which would mask whatever the origin policy does.
func setHandshakeHeaders(req *http.Request) {
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
}

func statusOf(resp *http.Response) string {
	if resp == nil {
		return "<nil>"
	}
	return resp.Status
}
