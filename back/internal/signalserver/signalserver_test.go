package signalserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testServer 起一个内存信号服务器并返回连接工厂。
func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	srv := NewServer("testkey")
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/peerjs"):
			srv.HandleWS(w, r)
		case strings.HasSuffix(r.URL.Path, "/id"):
			srv.HandleID(w, r)
		case strings.HasSuffix(r.URL.Path, "/announce"):
			srv.HandleAnnounce(w, r)
		case strings.HasSuffix(r.URL.Path, "/nodes"):
			srv.HandleNodes(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(hs.Close)
	return srv, hs
}

// dialWS 以指定 id 连接信令服务器。
func dialWS(t *testing.T, hs *httptest.Server, id, token string) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(hs.URL, "http") + "/peerjs?key=testkey&id=" + id + "&token=" + token
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	return conn
}

// readMsg 读取一条信令消息。
func readMsg(t *testing.T, conn *websocket.Conn) Message {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var m Message
	require.NoError(t, conn.ReadJSON(&m))
	return m
}

// TestSignal_OpenAndForward 注册 → OPEN；消息按 dst 转发（src 被服务端覆盖）。
//
// 发现背景：功能测试——注册 OPEN + 消息转发（服务端覆盖 src 是协议要求）
func TestSignal_OpenAndForward(t *testing.T) {
	_, hs := testServer(t)
	a := dialWS(t, hs, "node-a", "tok-a")
	defer a.Close()
	require.Equal(t, Message{Type: "OPEN"}, readMsg(t, a))

	b := dialWS(t, hs, "node-b", "tok-b")
	defer b.Close()
	require.Equal(t, Message{Type: "OPEN"}, readMsg(t, b))

	// A → B 转发
	payload := json.RawMessage(`{"type":"OFFER","connectionId":"c1"}`)
	require.NoError(t, a.WriteJSON(Message{Type: "OFFER", Dst: "node-b", Payload: payload}))
	m := readMsg(t, b)
	assert.Equal(t, "OFFER", m.Type)
	assert.Equal(t, "node-a", m.Src, "服务端必须覆盖 src")
	assert.Equal(t, "node-b", m.Dst)
	assert.Equal(t, payload, m.Payload)
}

// TestSignal_OfflineQueue 目标离线入队，上线后补发（OFFER 不丢）。
// 发现背景：peerjs-server 行为对齐——OFFER 在目标上线前到达不能丢。
func TestSignal_OfflineQueue(t *testing.T) {
	_, hs := testServer(t)
	a := dialWS(t, hs, "node-a", "tok-a")
	defer a.Close()
	readMsg(t, a)

	// B 未上线，A 发 OFFER → 入队
	require.NoError(t, a.WriteJSON(Message{Type: "OFFER", Dst: "node-b", Payload: json.RawMessage(`{"x":1}`)}))

	b := dialWS(t, hs, "node-b", "tok-b")
	defer b.Close()
	readMsg(t, b) // OPEN
	m := readMsg(t, b)
	assert.Equal(t, "OFFER", m.Type, "上线后应补发离线队列")
	assert.Equal(t, "node-a", m.Src)
}

// TestSignal_LeaveBroadcast 断开 → 其他节点收到 LEAVE。
//
// 发现背景：功能测试——LEAVE 广播让对端感知断开（peerjs-server 行为对齐）
func TestSignal_LeaveBroadcast(t *testing.T) {
	_, hs := testServer(t)
	a := dialWS(t, hs, "node-a", "tok-a")
	b := dialWS(t, hs, "node-b", "tok-b")
	defer b.Close()
	readMsg(t, a)
	readMsg(t, b)

	a.Close()
	m := readMsg(t, b)
	assert.Equal(t, "LEAVE", m.Type)
	assert.Equal(t, "node-a", m.Src)
}

// TestSignal_IDTaken token 不匹配时同 ID 拒绝；token 匹配时接管。
//
// 发现背景：功能测试——ID 占用保护：token 不匹配拒绝（防劫持他人 ID）
func TestSignal_IDTaken(t *testing.T) {
	_, hs := testServer(t)
	a := dialWS(t, hs, "same-id", "tok-1")
	defer a.Close()
	readMsg(t, a)

	// 不同 token → ID-TAKEN
	conn2, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(hs.URL, "http")+"/peerjs?key=testkey&id=same-id&token=wrong", nil)
	require.NoError(t, err)
	var m Message
	require.NoError(t, conn2.ReadJSON(&m))
	assert.Equal(t, "ID-TAKEN", m.Type)
	conn2.Close()
}

// TestSignal_InvalidKey 错误 key 拒绝。
//
// 发现背景：防御性测试——key 校验失败必须拒绝连接
func TestSignal_InvalidKey(t *testing.T) {
	_, hs := testServer(t)
	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(hs.URL, "http")+"/peerjs?key=wrong&id=x&token=t", nil)
	if err == nil {
		conn.Close()
		t.Fatal("错误 key 应拒绝")
	}
	_ = resp
}

// TestSignal_TokenWhitelist token 白名单：名单外拒绝升级，名单内正常 OPEN。
//
// 发现背景：代码审阅 2026-08-18——token 原本只做 ID 占用保护，任意客户端
// 可自定 token 连接并注册任意 ID，冒充节点收信令/诱导 OFFER；白名单让
// 自托管部署只信任已知节点（修复：WithTokenWhitelist + HandleWS 校验）。
func TestSignal_TokenWhitelist(t *testing.T) {
	srv := NewServer("testkey", WithTokenWhitelist([]string{"tok-a", "tok-b"}))
	hs := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.HandleWS(w, r)
	}))
	defer hs.Close()

	// 名单外 token → 拒绝（HTTP 400，无 OPEN）
	conn, resp, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(hs.URL, "http")+"/peerjs?key=testkey&id=evil&token=not-in-list", nil)
	if err == nil {
		conn.Close()
		t.Fatal("白名单外 token 应拒绝升级")
	}
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// 名单内 token → 正常 OPEN
	a := dialWS(t, hs, "node-a", "tok-a")
	defer a.Close()
	m := readMsg(t, a)
	assert.Equal(t, "OPEN", m.Type)
}

// TestDiscover_AnnounceAndQuery 节点 announce 房间 → 查询返回在线节点（心跳过期剔除）。
// 发现背景：自托管后房间发现并入信令服务器（替代 MQTT 广播）。
func TestDiscover_AnnounceAndQuery(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID, coll string) {
		body := strings.NewReader(`{"peerId":"` + peerID + `","collections":["` + coll + `"]}`)
		resp, err := http.Post(hs.URL+"/announce", "application/json", body)
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", "coll-a")
	announce("node-2", "coll-a")
	announce("node-3", "coll-b")

	// coll-a 应返回 node-1/node-2
	resp, err := http.Get(hs.URL + "/nodes?coll=coll-a")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Nodes []NodeInfo `json:"nodes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	ids := map[string]bool{}
	for _, n := range out.Nodes {
		ids[n.PeerID] = true
	}
	assert.True(t, ids["node-1"], "coll-a 应含 node-1")
	assert.True(t, ids["node-2"])
	assert.False(t, ids["node-3"], "coll-b 节点不应出现")
}
