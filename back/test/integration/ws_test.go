//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"peerdrive/internal/service"
)

// TestLocalWSSessionFetch 本地 WebSocket 会话：浏览器直连本节点，
// 复用与 DataChannel 完全一致的帧协议拉取文件。
// 发现背景：架构决策——本地/局域网走 WS（无打洞/信令开销），远端走
// WebRTC DataChannel；两传输必须语义一致（同一 reqId 状态机），本测试
// 验证 WS 路径的 req/meta/data/done 全链路。
func TestLocalWSSessionFetch(t *testing.T) {
	storage := t.TempDir()
	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i * 3)
	}
	hash := writeTestFile(t, storage, content)

	svc := newService(t, randID("it-ws"), storage, false, nil)

	// 服务端：升级 WS 为 WSSession 并绑定（等价 router /ws/peer 处理）
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(service.NewWSSession("local", conn))
	}))
	defer srv.Close()

	// 客户端：发 req 帧，收 meta/data/done
	wsURL := "ws" + srv.URL[4:] + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	req := map[string]any{"type": "req", "hash": hash, "offset": 0, "size": -1, "reqId": "ws-1"}
	if err := conn.WriteJSON(req); err != nil {
		t.Fatalf("send req: %v", err)
	}

	var got []byte
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if mt == websocket.BinaryMessage {
			got = append(got, data...)
			continue
		}
		var resp struct {
			Type   string `json:"type"`
			Msg    string `json:"msg"`
			Offset int64  `json:"offset"`
			Size   int64  `json:"size"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		switch resp.Type {
		case "err":
			t.Fatalf("服务端错误: %s", resp.Msg)
		case "done":
			if !bytes.Equal(got, content) {
				t.Fatalf("WS 拉取内容不一致: got %d bytes, want %d", len(got), len(content))
			}
			if sum := sha256Hex(got); sum != hash {
				t.Fatalf("sha256 不一致: %s", sum)
			}
			return
		}
	}
	t.Fatal("WS 拉取超时（未收到 done）")
}

// TestLocalWSSession_FetchFromPeerReuse 本地会话注册后，
// FetchFromPeer("local", ...) 走同一状态机（服务端主动拉方向）。
// 发现背景：BindLocal 以 "local" 注册进 conns，FetchFromPeer 无需分支即可复用。
func TestLocalWSSession_FetchFromPeerReuse(t *testing.T) {
	storage := t.TempDir()
	content := []byte("local-session-bidirectional")
	hash := writeTestFile(t, storage, content)

	svc := newService(t, randID("it-ws2"), storage, false, nil)

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(service.NewWSSession("local", conn))
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// 等本地会话注册（bindConn 在 BindLocal 内同步完成，但等一个周期确保 readLoop 活跃）
	time.Sleep(200 * time.Millisecond)

	// 服务端主动经 "local" 会话拉取——客户端需要模拟响应帧
	done := make(chan []byte, 1)
	go func() {
		data, err := svc.FetchFromPeer("local", hash, 0, -1)
		if err != nil {
			t.Errorf("FetchFromPeer(local): %v", err)
			return
		}
		done <- data
	}()

	// 客户端读 req 帧并回复（meta 不需要，直接发 data+done）
	deadline := time.Now().Add(30 * time.Second)
	sent := 0
	for time.Now().Before(deadline) {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if mt == websocket.TextMessage {
			var req struct {
				Type   string `json:"type"`
				Offset int64  `json:"offset"`
				Size   int64  `json:"size"`
				ReqID  string `json:"reqId"`
			}
			if err := json.Unmarshal(data, &req); err != nil || req.Type != "req" {
				continue
			}
			// 模拟服务端分块响应
			const chunk = 64
			for off := 0; off < len(content); off += chunk {
				end := off + chunk
				if end > len(content) {
					end = len(content)
				}
				hdr := map[string]any{"type": "data", "offset": off, "size": end - off, "reqId": req.ReqID}
				if err := conn.WriteJSON(hdr); err != nil {
					t.Fatalf("write hdr: %v", err)
				}
				if err := conn.WriteMessage(websocket.BinaryMessage, content[off:end]); err != nil {
					t.Fatalf("write body: %v", err)
				}
			}
			doneMsg := map[string]any{"type": "done", "offset": 0, "size": len(content), "reqId": req.ReqID}
			if err := conn.WriteJSON(doneMsg); err != nil {
				t.Fatalf("write done: %v", err)
			}
			sent++
			if sent > 0 {
				break
			}
		}
	}

	select {
	case data := <-done:
		if !bytes.Equal(data, content) {
			t.Fatalf("双向拉取内容不一致: got %d bytes", len(data))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("FetchFromPeer(local) 超时")
	}
}
