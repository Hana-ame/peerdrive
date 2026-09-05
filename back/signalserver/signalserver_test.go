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

// TestGraph_AnnouncePeersCreatesLinks 两个节点 announce peers 后 /nodes 返回对应边。
func TestGraph_AnnouncePeersCreatesLinks(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID string, peers []string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "peers": peers})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", []string{"node-2"})
	announce("node-2", []string{"node-1"})

	resp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Nodes []NodeInfo  `json:"nodes"`
		Links []GraphLink `json:"links"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Len(t, out.Nodes, 2)
	require.Len(t, out.Links, 1, "A→B 和 B→A 应去重为一条边")
	assert.Equal(t, "node-1", out.Links[0].Source)
	assert.Equal(t, "node-2", out.Links[0].Target)
}

// TestGraph_EmptyPeersClearsLinks 再次 announce 空 peers 清空旧边。
func TestGraph_EmptyPeersClearsLinks(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID string, peers []string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "peers": peers})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", []string{"node-2"})
	announce("node-2", []string{"node-1"})
	// 两端都清空 peers，旧边才应消失（仅一端清空时另一端仍可能上报该边）
	announce("node-1", []string{})
	announce("node-2", []string{})

	resp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Links []GraphLink `json:"links"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Empty(t, out.Links, "两端空 peers 应清空旧边")
}

// TestGraph_LeaveRemovesLinks 节点 leave 后相关边消失。
func TestGraph_LeaveRemovesLinks(t *testing.T) {
	srv, hs := testServer(t)
	announce := func(peerID string, peers []string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "peers": peers})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", []string{"node-2"})
	announce("node-2", []string{"node-1"})

	body, _ := json.Marshal(map[string]string{"peerId": "node-2"})
	req := httptest.NewRequest(http.MethodPost, "/discover/leave", strings.NewReader(string(body)))
	w := httptest.NewRecorder()
	srv.HandleLeave(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	getResp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer getResp.Body.Close()
	var out struct {
		Nodes []NodeInfo  `json:"nodes"`
		Links []GraphLink `json:"links"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&out))
	assert.Len(t, out.Nodes, 1, "node-2 下线后只剩 node-1")
	assert.Empty(t, out.Links, "node-2 下线后边应消失")
}

// TestGraph_SelfPeerIgnored announce peers 包含自身 ID 时忽略。
func TestGraph_SelfPeerIgnored(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID string, peers []string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "peers": peers})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", []string{"node-1", "node-2"})
	announce("node-2", []string{"node-1"})

	getResp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer getResp.Body.Close()
	var out struct {
		Links []GraphLink `json:"links"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&out))
	require.Len(t, out.Links, 1)
	assert.NotEqual(t, out.Links[0].Source, out.Links[0].Target, "自身边应被忽略")
}

// TestNodes_EmptyCollReturnsAll 空 coll 返回所有 collection 节点（行为与 wintools 对齐）。
func TestNodes_EmptyCollReturnsAll(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID, coll string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{coll}})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("node-1", "coll-a")
	announce("node-2", "coll-b")

	resp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Nodes []NodeInfo `json:"nodes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	assert.Len(t, out.Nodes, 2)
}

// TestNodes_TypeFilter 支持 ?type= 过滤节点类型（行为与 wintools 对齐）。
func TestNodes_TypeFilter(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID, nodeType string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "nodeType": nodeType})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("go-1", "go-persistent")
	announce("web-1", "web-temp")

	resp, err := http.Get(hs.URL + "/nodes?type=go-persistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Nodes []NodeInfo `json:"nodes"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Nodes, 1)
	assert.Equal(t, "go-1", out.Nodes[0].PeerID)
}

// TestNodes_IncludesNodeMetadata announce 上报 nodeType/collections/loadInfo 后 nodes 返回完整元数据。
func TestNodes_IncludesNodeMetadata(t *testing.T) {
	_, hs := testServer(t)
	body, _ := json.Marshal(map[string]any{
		"peerId": "node-1", "collections": []string{"media"}, "nodeType": "go-persistent",
		"loadInfo": map[string]any{"connections": 3},
	})
	resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()

	getResp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer getResp.Body.Close()
	var out struct {
		Nodes []NodeInfo `json:"nodes"`
	}
	require.NoError(t, json.NewDecoder(getResp.Body).Decode(&out))
	require.Len(t, out.Nodes, 1)
	n := out.Nodes[0]
	assert.Equal(t, "go-persistent", n.NodeType)
	assert.Equal(t, []string{"media"}, n.Collections)
	assert.Equal(t, float64(3), n.LoadInfo["connections"])
}

// TestGraph_TypeFilterLinksExcludeFilteredNodes 验证 type 过滤时，graph 边不会包含被过滤掉的节点。
func TestGraph_TypeFilterLinksExcludeFilteredNodes(t *testing.T) {
	_, hs := testServer(t)
	announce := func(peerID, nodeType string, peers []string) {
		body, _ := json.Marshal(map[string]any{"peerId": peerID, "collections": []string{"media"}, "nodeType": nodeType, "peers": peers})
		resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
		require.NoError(t, err)
		resp.Body.Close()
	}
	announce("go-1", "go-persistent", []string{"web-1"})
	announce("web-1", "web-temp", []string{"go-1"})

	// 只查 go-persistent 类型：nodes 只有 go-1，links 应为空（web-1 被过滤）
	resp, err := http.Get(hs.URL + "/nodes?type=go-persistent")
	require.NoError(t, err)
	defer resp.Body.Close()
	var out struct {
		Nodes []NodeInfo  `json:"nodes"`
		Links []GraphLink `json:"links"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&out))
	require.Len(t, out.Nodes, 1)
	assert.Empty(t, out.Links, "被过滤节点不应出现在 graph 边中")
}

// TestNodes_EmptyReturnsEmptyArray 空节点/空边时 JSON 应返回 [] 而不是 null。
func TestNodes_EmptyReturnsEmptyArray(t *testing.T) {
	_, hs := testServer(t)
	resp, err := http.Get(hs.URL + "/nodes")
	require.NoError(t, err)
	defer resp.Body.Close()
	var raw map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&raw))
	require.Contains(t, raw, "nodes")
	require.Contains(t, raw, "links")
	assert.True(t, len(raw["nodes"]) > 0 && raw["nodes"][0] == '[', "nodes 应为 JSON 数组")
	assert.True(t, len(raw["links"]) > 0 && raw["links"][0] == '[', "links 应为 JSON 数组")
}

// TestSweepDiscovery_CleansExpiredNodes 过期节点应从 disc/peerLinks/peerStats/peerColls 清理。
func TestSweepDiscovery_CleansExpiredNodes(t *testing.T) {
	srv, hs := testServer(t)
	// announce 一个节点，让它进入 disc/peerStats/peerColls
	body, _ := json.Marshal(map[string]any{
		"peerId": "node-1", "collections": []string{"media"}, "nodeType": "go-persistent",
		"peers": []string{"node-2"}, "loadInfo": map[string]any{"connections": 1},
	})
	resp, err := http.Post(hs.URL+"/announce", "application/json", strings.NewReader(string(body)))
	require.NoError(t, err)
	resp.Body.Close()

	// 把它的 lastSeen 改到心跳 TTL 之前
	srv.mu.Lock()
	srv.disc["media"]["node-1"] = time.Now().Add(-2 * srv.heartbeatTTL)
	srv.mu.Unlock()

	srv.sweepDiscovery()

	srv.mu.Lock()
	_, hasDisc := srv.disc["media"]["node-1"]
	_, hasLinks := srv.peerLinks["node-1"]
	_, hasStats := srv.peerStats["node-1"]
	_, hasColls := srv.peerColls["node-1"]
	srv.mu.Unlock()
	assert.False(t, hasDisc, "过期节点应从 disc 清理")
	assert.False(t, hasLinks, "过期节点应从 peerLinks 清理")
	assert.False(t, hasStats, "过期节点应从 peerStats 清理")
	assert.False(t, hasColls, "过期节点应从 peerColls 清理")
}
