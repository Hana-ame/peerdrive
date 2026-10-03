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

	"peerdrive/internal/transport"
)

// TestLocalWSSessionFetch Local WebSocket session: browser directly connects to local node,
// reusing the exact same frame protocol as DataChannel to pull files.
// Discovery context: architecture decision — local/LAN uses WS (no hole-punching/signaling overhead), remote uses
// WebRTC DataChannel; both transports must have consistent semantics (same reqId state machine). This test
// verifies the full WS path chain of req/meta/data/done.
func TestLocalWSSessionFetch(t *testing.T) {
	storage := t.TempDir()
	content := make([]byte, 200*1024)
	for i := range content {
		content[i] = byte(i * 3)
	}
	hash := writeTestFile(t, storage, content)

	svc := newService(t, randID("it-ws"), storage, false, nil)

	// Server side: upgrade WS to WSSession and bind (equivalent to router /ws/peer handling)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()

	// Client side: send req frame, receive meta/data/done
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
			t.Fatalf("server error: %s", resp.Msg)
		case "done":
			if !bytes.Equal(got, content) {
				t.Fatalf("WS pull content mismatch: got %d bytes, want %d", len(got), len(content))
			}
			if sum := sha256Hex(got); sum != hash {
				t.Fatalf("sha256 mismatch: %s", sum)
			}
			return
		}
	}
	t.Fatal("WS pull timeout (no done received)")
}

// TestLocalWSSession_FetchFromPeerReuse After local session registration,
// FetchFromPeer("local", ...) goes through the same state machine (server-initiated pull direction).
// Discovery context: BindLocal registers with "local" in conns, FetchFromPeer can reuse without branching.
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
		svc.BindLocal(transport.NewWSSession("local", conn))
	}))
	defer srv.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+srv.URL[4:]+"/ws", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Wait for local session registration (bindConn completes synchronously inside BindLocal, but wait one cycle to ensure readLoop is active)
	time.Sleep(200 * time.Millisecond)

	// Server initiates pull via "local" session — client needs to simulate response frames
	done := make(chan []byte, 1)
	go func() {
		data, err := svc.FetchFromPeer("local", hash, 0, -1)
		if err != nil {
			t.Errorf("FetchFromPeer(local): %v", err)
			return
		}
		done <- data
	}()

	// Client reads req frame and replies (meta not needed, send data+done directly)
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
			// Simulate server chunked response
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
			t.Fatalf("bidirectional pull content mismatch: got %d bytes", len(data))
		}
	case <-time.After(30 * time.Second):
		t.Fatal("FetchFromPeer(local) timeout")
	}
}
