package wsconn

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestIsLoopbackRemote(t *testing.T) {
	// 发现背景：这是「无 Origin 的连接只放行本机」这条安全判据的实现。
	// 判定过宽会让局域网内的脚本拿到 /ws/peer 的 admin 面，而这些会话被
	// 当作 "self"（见 transport.WSSession.IsLocal）。只判 RemoteAddr、
	// 不看 XFF：反代后 XFF 部分由客户端控制。
	for _, tc := range []struct {
		remote string
		want   bool
	}{
		{"127.0.0.1:54321", true},
		{"127.0.0.1:1", true},
		{"[::1]:54321", true},
		{"localhost:5173", true},
		{"localhost", true},
		// close, but not the loopback
		{"127.0.0.2:1", false},
		{"127.0.0.11:1", false},
		{"[::2]:1", false},
		{"[::ffff:127.0.0.1]:1", false},
		{"10.0.0.1:1", false},
		{"192.168.1.5:1", false},
		{"172.16.0.1:1", false},
		{"203.0.113.7:8080", false},
		{"10.0.0.1", false},
		{"", false},
		{":1", false},
	} {
		if got := IsLoopbackRemote(tc.remote); got != tc.want {
			t.Errorf("IsLoopbackRemote(%q) = %v, want %v", tc.remote, got, tc.want)
		}
	}
}

func TestOriginPolicy_CheckOrigin(t *testing.T) {
	// 发现背景：两条分支的不对称正是设计要点——无 Origin 的是脚本，只
	// 放行本机；有 Origin 时 nil Allow 走历史默认（放行），否则交给调用
	// 方的白名单。拆分时曾把「无 Origin + 远程」误判成放行，这里钉住。
	for _, tc := range []struct {
		name   string
		origin string
		remote string
		allow  func(string) bool
		want   bool
	}{
		{"no origin, loopback remote → accept", "", "127.0.0.1:1", nil, true},
		{"no origin, remote → reject", "", "10.0.0.1:1", nil, false},
		{"no origin, remote, allowlist set → still reject", "", "203.0.113.7:1",
			func(string) bool { return true }, false},
		{"origin, nil allowlist → accept (historical default)", "http://example.com", "10.0.0.1:1", nil, true},
		{"origin, allowlisted → accept", "http://good.example", "10.0.0.1:1",
			func(o string) bool { return o == "http://good.example" }, true},
		{"origin, not allowlisted → reject", "http://evil.example", "127.0.0.1:1",
			func(o string) bool { return o == "http://good.example" }, false},
		{"origin, allowlist rejects everything", "http://good.example", "127.0.0.1:1",
			func(string) bool { return false }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/ws/peer", nil)
			r.RemoteAddr = tc.remote
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			p := OriginPolicy{Allow: tc.allow}
			if got := p.CheckOrigin(r); got != tc.want {
				t.Fatalf("CheckOrigin = %v, want %v (Origin=%q RemoteAddr=%q)",
					got, tc.want, tc.origin, tc.remote)
			}
		})
	}
}

// TestUpgrader_RealHandshake exercises the handshake over real loopback TCP
// with a real gorilla client. It proves the wsconn.Upgrader is a drop-in
// replacement for the inline websocket.Upgrader the router used to build.
func TestUpgrader_RealHandshake(t *testing.T) {
	var remoteSeen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteSeen = r.RemoteAddr
		u := NewUpgrader(OriginPolicy{})
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]any{"op": mt, "n": len(data)})
	}))
	defer srv.Close()

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/"

	// 1. Origin present + nil allowlist: the historical default, accepted.
	u, _, err := dialer.Dial(wsURL, http.Header{"Origin": []string{"http://any.where"}})
	if err != nil {
		t.Fatalf("dial with Origin: %v", err)
	}
	defer u.Close()
	if err := u.WriteJSON(map[string]string{"type": "req", "reqId": "r1"}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	mt, data, err := u.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("reply opcode = %d, want TextMessage", mt)
	}
	if !strings.Contains(string(data), `"op":1`) {
		t.Fatalf("unexpected echo %s", data)
	}
	if !strings.Contains(remoteSeen, ":") {
		t.Fatalf("server saw no RemoteAddr: %q", remoteSeen)
	}

	// 2. Origin present + strict allowlist: rejected with 403.
	srvReject := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := NewUpgrader(OriginPolicy{
			Allow: func(o string) bool { return o == "http://good.example" },
		})
		_, _ = u.Upgrade(w, r, nil)
	}))
	defer srvReject.Close()
	wsURLReject := "ws" + strings.TrimPrefix(srvReject.URL, "http") + "/"
	_, resp, err := dialer.Dial(wsURLReject, http.Header{"Origin": []string{"http://evil.example"}})
	if err == nil {
		t.Fatal("handshake was accepted for a non-allowlisted Origin")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rejected handshake status = %d, want 403", respStatus(resp))
	}

	// 3. No Origin at all: the dialer does not add one, so this exercises
	// the IsLoopbackRemote branch. The httptest server is on 127.0.0.1, so
	// the request must be accepted.
	srvLoop := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Origin"); got != "" {
			t.Errorf("server unexpectedly saw Origin %q", got)
		}
		u := NewUpgrader(OriginPolicy{})
		conn, err := u.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.Close()
	}))
	defer srvLoop.Close()
	wsURLLoop := "ws" + strings.TrimPrefix(srvLoop.URL, "http") + "/"
	u2, _, err := dialer.Dial(wsURLLoop, http.Header{})
	if err != nil {
		t.Fatalf("no-Origin handshake from 127.0.0.1 was rejected: %v", err)
	}
	u2.Close()

	// 4. Malformed handshake (no Upgrade header) must fail cleanly.
	srvBad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = NewUpgrader(OriginPolicy{}).Upgrade(w, r, nil)
	}))
	defer srvBad.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(srvBad.URL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatalf("dial raw: %v", err)
	}
	_, _ = conn.Write([]byte("GET / HTTP/1.1\r\nHost: bad\r\n\r\n"))
	_ = conn.Close()
	time.Sleep(20 * time.Millisecond)
}

func respStatus(resp *http.Response) int {
	if resp == nil {
		return -1
	}
	return resp.StatusCode
}
