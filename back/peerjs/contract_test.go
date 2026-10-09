package peerjs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hana-ame/go-peerjs/signalling"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// fakeSignallerServer stands up a minimal PeerJS-protocol signaling server (HTTP /id + WS /peerjs)
// so the transport can be exercised with no network access.
//
// Discovery background: before this harness, no test in the module touched the real transport —
// every routing test drove Peer.route through fakeSignaller (no socket). The Dial/heartbeat/reconnect
// behaviors (retrieveID, WS handshake, readLoop, ID-TAKEN, Done) had zero coverage.
type fakeSignallerServer struct {
	mu       sync.Mutex
	key      string
	idBody   string // body of GET <path>id
	idStatus int    // 0 -> http.StatusOK
	wsAccept bool   // false -> answer 500, do not upgrade
	script   []Message
	clients  []*websocket.Conn
	received []Message // frames received from all clients, in order
	wsPath   string
	wsQuery  map[string]string
	idPath   string
	idQuery  map[string]string
}

// idBody defaults to "nodeA" so the acquire-an-ID transcript is identical to the
// explicit-ID transcript apart from the extra GET line.
func newFakeSignallerServer(key string) *fakeSignallerServer {
	return &fakeSignallerServer{key: key, idBody: "nodeA", idStatus: http.StatusOK, wsAccept: true}
}

// up returns an httptest server bound to the PeerJS route shape (/{path}peerjs, /{path}id).
func (f *fakeSignallerServer) up() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/peerjs"):
			f.handleWS(w, r)
		case strings.HasSuffix(r.URL.Path, "/id"):
			f.handleID(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

func (f *fakeSignallerServer) handleID(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.idPath, f.idQuery = r.URL.Path, cloneQuery(r.URL.Query())
	body, status := f.idBody, f.idStatus
	f.mu.Unlock()
	if status == 0 {
		status = http.StatusOK
	}
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

func (f *fakeSignallerServer) handleWS(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	f.wsPath, f.wsQuery = r.URL.Path, cloneQuery(q)
	script := append([]Message(nil), f.script...)
	f.mu.Unlock()

	if q.Get("key") != f.key || q.Get("id") == "" || q.Get("token") == "" || !f.wsAccept {
		http.Error(w, "invalid key", http.StatusInternalServerError)
		return
	}
	u := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	conn, err := u.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	f.mu.Lock()
	f.clients = append(f.clients, conn)
	f.mu.Unlock()
	for _, m := range script {
		_ = conn.WriteJSON(m)
	}
	for {
		var m Message
		if err := conn.ReadJSON(&m); err != nil {
			return
		}
		m.Src = q.Get("id") // the real server overwrites src too
		f.mu.Lock()
		f.received = append(f.received, m)
		f.mu.Unlock()
	}
}

// closeAll drops every client socket (simulates a network drop, not a client Close).
func (f *fakeSignallerServer) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.clients {
		_ = c.Close()
	}
	f.clients = nil
}

// client returns the most recent client socket (test helper).
func (f *fakeSignallerServer) client(t *testing.T) *websocket.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	require.NotEmpty(t, f.clients, "no client connected")
	return f.clients[len(f.clients)-1]
}

// count returns how many received frames have type t.
func (f *fakeSignallerServer) count(t MessageType) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, m := range f.received {
		if m.Type == t {
			n++
		}
	}
	return n
}

func cloneQuery(q map[string][]string) map[string]string {
	out := make(map[string]string, len(q))
	for k, v := range q {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// payloadShape extracts only the routing-relevant fields from a signaling payload.
// SDP and ICE candidate bodies are deliberately excluded: they are large and, in a
// real exchange, nondeterministic, while the wire *shape* and *ordering* is what
// the refactor must preserve.
func payloadShape(p json.RawMessage) string {
	if len(p) == 0 {
		return "{}"
	}
	var pl struct {
		ConnectionID  string `json:"connectionId"`
		Type          string `json:"type"`
		Label         string `json:"label"`
		Reliable      bool   `json:"reliable"`
		Serialization string `json:"serialization"`
		Msg           string `json:"msg"`
	}
	_ = json.Unmarshal(p, &pl)
	parts := []string{fmt.Sprintf("reliable=%v", pl.Reliable)}
	if pl.ConnectionID != "" {
		parts = append(parts, "connectionId="+pl.ConnectionID)
	}
	if pl.Type != "" {
		parts = append(parts, "kind="+pl.Type)
	}
	if pl.Label != "" {
		parts = append(parts, "label="+pl.Label)
	}
	if pl.Serialization != "" {
		parts = append(parts, "serialization="+pl.Serialization)
	}
	if pl.Msg != "" {
		parts = append(parts, "msg="+pl.Msg)
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// normMsg renders one frame as a deterministic, diffable line.
func normMsg(m Message) string {
	return fmt.Sprintf("msg type=%q src=%q dst=%q payload=%s", m.Type, m.Src, m.Dst, payloadShape(m.Payload))
}

// normQuery renders a query as sorted key=value pairs; the volatile ts counter
// is normalized to N so a transcript is deterministic.
func normQuery(q map[string]string) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		v := q[k]
		if k == "ts" {
			v = "N"
		}
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, "&")
}

// newContractSignaller builds a transport-level signaller pointing at hs.
func newContractSignaller(t *testing.T, hs *httptest.Server, dialID string, opts signalling.Options) signalling.Signaller {
	t.Helper()
	host, port, _ := strings.Cut(strings.TrimPrefix(hs.URL, "http://"), ":")
	opts.Host, opts.Port, opts.Secure = host, port, false
	if opts.Key == "" {
		opts.Key = "k1"
	}
	if opts.Token == "" {
		opts.Token = "tok-fixed"
	}
	if opts.Path == "" {
		opts.Path = "/"
	}
	return signalling.NewPeerJSSignaller(dialID, opts, nil)
}

// runContractScenario drives one full signaling session and returns the wire
// transcript: every request the client made and every frame in either direction,
// in order. dialID=="" exercises the retrieveID path.
func runContractScenario(t *testing.T, dialID string) string {
	t.Helper()
	srv := newFakeSignallerServer("k1")
	srv.script = []Message{
		{Type: MsgOpen},
		{Type: MsgAnswer, Payload: json.RawMessage(`{"connectionId":"c1","type":"data"}`)},
		{Type: MsgCandidate, Payload: json.RawMessage(`{"connectionId":"c1","type":"data"}`)},
		{Type: MsgLeave, Src: "nodeB"},
		{Type: MsgHeartbeat},
		{Type: MsgIDTaken, Payload: json.RawMessage(`{"msg":"ID is taken"}`)},
	}
	hs := srv.up()
	defer hs.Close()

	var (
		gotMu sync.Mutex
		got   []Message
		gotCh = make(chan Message, 16)
	)
	sig := newContractSignaller(t, hs, dialID, signalling.Options{PingInterval: time.Hour})
	sig.OnMessage(func(m Message) error {
		gotCh <- m
		return nil
	})
	require.NoError(t, sig.Dial(context.Background()))

	for i := 0; i < 6; i++ {
		select {
		case m := <-gotCh:
			gotMu.Lock()
			got = append(got, m)
			gotMu.Unlock()
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for frame %d; got so far: %v", i+1, got)
		}
	}

	offer := OfferPayload{Type: ConnData, ConnectionID: "c1", Label: "media", Reliable: true, Serialization: "raw"}
	require.NoError(t, sig.Send(NewMessage(MsgOffer, "nodeB", offer)))
	require.NoError(t, sig.Send(NewMessage(MsgCandidate, "nodeB", CandidatePayload{Type: ConnData, ConnectionID: "c2"})))
	require.NoError(t, sig.Send(NewMessage(MsgHeartbeat, "", nil)))
	require.NoError(t, sig.Close())

	// Send after Close must not reach the wire.
	_ = sig.Send(NewMessage(MsgOffer, "nodeB", offer))

	deadline := time.Now().Add(2 * time.Second)
	var sent []Message
	for len(sent) < 3 {
		srv.mu.Lock()
		sent = append([]Message(nil), srv.received...)
		srv.mu.Unlock()
		if len(sent) >= 3 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	var lines []string
	if dialID == "" {
		lines = append(lines, fmt.Sprintf("GET %s?%s", srv.idPath, normQuery(srv.idQuery)))
	}
	lines = append(lines, fmt.Sprintf("WS  %s?%s", srv.wsPath, normQuery(srv.wsQuery)))
	for _, m := range got {
		lines = append(lines, "RX  "+normMsg(m))
	}
	for _, m := range sent {
		lines = append(lines, "TX  "+normMsg(m))
	}
	return strings.Join(lines, "\n")
}

// CONTRACT: the exact wire sequence of a PeerJS signaling session, captured from
// the pre-split code (peerJSSignaller inside package peerjs). Moving the transport
// to the signalling subpackage must not change a single line of this transcript.
const contractGolden = `WS  /peerjs?id=nodeA&key=k1&token=tok-fixed&version=1.5.4
RX  msg type="OPEN" src="" dst="" payload={}
RX  msg type="ANSWER" src="" dst="" payload={reliable=false connectionId=c1 kind=data}
RX  msg type="CANDIDATE" src="" dst="" payload={reliable=false connectionId=c1 kind=data}
RX  msg type="LEAVE" src="nodeB" dst="" payload={}
RX  msg type="HEARTBEAT" src="" dst="" payload={}
RX  msg type="ID-TAKEN" src="" dst="" payload={reliable=false msg=ID is taken}
TX  msg type="OFFER" src="nodeA" dst="nodeB" payload={reliable=true connectionId=c1 kind=data label=media serialization=raw}
TX  msg type="CANDIDATE" src="nodeA" dst="nodeB" payload={reliable=false connectionId=c2 kind=data}
TX  msg type="HEARTBEAT" src="nodeA" dst="" payload={}`

func TestContract_TransportWireSequence_ExplicitID(t *testing.T) {
	require.Equal(t, contractGolden, runContractScenario(t, "nodeA"))
}

func TestContract_TransportWireSequence_AcquiredID(t *testing.T) {
	want := "GET /id?ts=N&version=1.5.4\n" + contractGolden
	require.Equal(t, want, runContractScenario(t, ""))
}

// TestContract_PathPrefix pins the self-hosted path-prefix handling: both the
// retrieve-id and the socket URL honor Options.Path.
func TestContract_PathPrefix(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "", signalling.Options{Path: "/pfx/", Token: "tok", PingInterval: time.Hour})
	require.NoError(t, sig.Dial(context.Background()))
	defer sig.Close()
	require.Equal(t, "/pfx/id", srv.idPath)
	require.Equal(t, "/pfx/peerjs", srv.wsPath)
}

// TestContract_AllFrameTypesRouted pins that every frame type reaches the router
// in order, including ID-TAKEN / EXPIRE / ERROR, which the Peer layer treats as
// log-only but which the transport must never swallow.
func TestContract_AllFrameTypesRouted(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	srv.script = []Message{
		{Type: MsgOpen},
		{Type: MsgOffer, Payload: json.RawMessage(`{"connectionId":"c1","type":"data"}`)},
		{Type: MsgAnswer, Payload: json.RawMessage(`{"connectionId":"c1","type":"data"}`)},
		{Type: MsgCandidate, Payload: json.RawMessage(`{"connectionId":"c1","type":"data"}`)},
		{Type: MsgExpire, Payload: json.RawMessage(`{"connectionId":"c1"}`)},
		{Type: MsgLeave, Src: "nodeB"},
		{Type: MsgHeartbeat},
		{Type: MsgIDTaken, Payload: json.RawMessage(`{"msg":"ID is taken"}`)},
		{Type: MsgError, Payload: json.RawMessage(`{"msg":"boom"}`)},
	}
	hs := srv.up()
	defer hs.Close()
	var (
		mu  sync.Mutex
		got []Message
		ch  = make(chan Message, 32)
	)
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	sig.OnMessage(func(m Message) error { ch <- m; return nil })
	require.NoError(t, sig.Dial(context.Background()))
	defer sig.Close()
	for i := 0; i < 9; i++ {
		select {
		case m := <-ch:
			mu.Lock()
			got = append(got, m)
			mu.Unlock()
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out at frame %d", i+1)
		}
	}
	require.Len(t, got, 9)
	for i, want := range []MessageType{MsgOpen, MsgOffer, MsgAnswer, MsgCandidate, MsgExpire, MsgLeave, MsgHeartbeat, MsgIDTaken, MsgError} {
		require.Equal(t, want, got[i].Type, "frame %d", i)
	}
}

// TestContract_MalformedFramesSkipped pins readLoop's tolerance: a frame that is
// not valid JSON is skipped, and the loop keeps reading afterwards.
func TestContract_MalformedFramesSkipped(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	ch := make(chan Message, 8)
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	sig.OnMessage(func(m Message) error { ch <- m; return nil })
	require.NoError(t, sig.Dial(context.Background()))
	defer sig.Close()

	conn := srv.client(t)
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "OPEN"}))
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte("{not json")))
	require.NoError(t, conn.WriteJSON(Message{Type: MsgLeave, Src: "nodeB"}))

	select {
	case m := <-ch:
		require.Equal(t, MsgOpen, m.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("OPEN not delivered")
	}
	select {
	case m := <-ch:
		require.Equal(t, MsgLeave, m.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("LEAVE after a malformed frame not delivered")
	}
	// the connection survived the malformed input
	require.Equal(t, "nodeA", sig.ID())
	require.NoError(t, sig.Send(NewMessage(MsgHeartbeat, "", nil)))
}

// TestContract_OverSizedFrameDropsConnection pins the 1MB read limit: a frame
// over the limit tears down the socket (readLoop returns) and Done() fires, so
// the reconnect loop takes over rather than an oversized frame stalling the node.
func TestContract_OverSizedFrameDropsConnection(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.NoError(t, sig.Dial(context.Background()))
	defer sig.Close()

	conn := srv.client(t)
	big := make([]byte, 1<<20+16)
	for i := range big {
		big[i] = 'x'
	}
	// the client's read limit closes the socket before the server finishes the
	// write, so the write itself may fail with connection reset: that is the
	// drop, not a test error.
	_ = conn.WriteMessage(websocket.TextMessage, big)

	select {
	case <-sig.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed after an oversized frame")
	}
}

// TestContract_SignallerDialFailureMessages pins the exact error texts of every
// Dial failure mode. Callers log these; they are part of the public contract.
func TestContract_SignallerDialFailureMessages(t *testing.T) {
	cases := []struct {
		name     string
		idBody   string
		idStatus int
		wsAccept bool
		dialID   string // empty -> exercises the retrieveID path
		want     string
	}{
		{"retrieve http 500", "boom", http.StatusInternalServerError, true, "", "peerjs: retrieve id: status 500"},
		{"retrieve invalid id", "<bad>", http.StatusOK, true, "", `peerjs: retrieve id: server returned invalid id "<bad>"`},
		{"ws refused", "x", http.StatusOK, false, "nodeA", "peerjs: dial"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeSignallerServer("k1")
			srv.idBody, srv.idStatus, srv.wsAccept = tc.idBody, tc.idStatus, tc.wsAccept
			hs := srv.up()
			defer hs.Close()
			sig := newContractSignaller(t, hs, tc.dialID, signalling.Options{Token: "tok", PingInterval: time.Hour})
			err := sig.Dial(context.Background())
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestContract_DialUnreachable pins the unreachable-server error shape.
func TestContract_DialUnreachable(t *testing.T) {
	sig := signalling.NewPeerJSSignaller("nodeA", signalling.Options{
		Host: "127.0.0.1", Port: "1", Secure: false, Path: "/", Key: "k1",
		Token: "tok", PingInterval: time.Hour,
	}, nil)
	err := sig.Dial(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "peerjs: dial 127.0.0.1:1")

	// retrieve-id step against an unreachable host
	sig2 := signalling.NewPeerJSSignaller("", signalling.Options{
		Host: "127.0.0.1", Port: "1", Secure: false, Path: "/", Key: "k1",
		Token: "tok", PingInterval: time.Hour,
	}, nil)
	err = sig2.Dial(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "peerjs: retrieve id")
}

// TestContract_SendNotConnected pins the "peerjs: not connected" text and the
// idempotency of Close before/after Dial.
func TestContract_SendNotConnected(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.EqualError(t, sig.Send(NewMessage(MsgOffer, "nodeB", nil)), "peerjs: not connected")

	require.NoError(t, sig.Close()) // idempotent before Dial
	require.NoError(t, sig.Close())

	require.NoError(t, sig.Dial(context.Background()))
	defer sig.Close()
	require.NoError(t, sig.Close())
	require.EqualError(t, sig.Send(NewMessage(MsgOffer, "nodeB", nil)), "peerjs: not connected")
}

// TestContract_DialContextCancelled pins ctx-abort behavior.
func TestContract_DialContextCancelled(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "", signalling.Options{PingInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, sig.Dial(ctx))
}

// TestContract_HeartbeatAuto: a live socket produces HEARTBEAT frames on the timer,
// and heartbeatLoop stops once the socket is dropped.
func TestContract_HeartbeatAuto(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: 20 * time.Millisecond})
	require.NoError(t, sig.Dial(context.Background()))

	require.Eventually(t, func() bool { return srv.count(MsgHeartbeat) >= 2 }, 2*time.Second, 5*time.Millisecond)

	srv.closeAll()
	select {
	case <-sig.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed after network drop")
	}
	before := srv.count(MsgHeartbeat)
	// heartbeatLoop exits on done; no further heartbeats may be queued
	time.Sleep(100 * time.Millisecond)
	require.LessOrEqual(t, srv.count(MsgHeartbeat), before+1)
}

// TestContract_DoneSemantics pins H7: a network drop closes Done(), but an
// explicit Close() does not — otherwise the reconnect loop would fire forever
// after a deliberate shutdown.
func TestContract_DoneSemantics(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.NoError(t, sig.Dial(context.Background()))

	srv.closeAll() // network drop, not a client Close
	select {
	case <-sig.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("Done() not closed after network drop")
	}

	srv2 := newFakeSignallerServer("k1")
	hs2 := srv2.up()
	defer hs2.Close()
	sig2 := newContractSignaller(t, hs2, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.NoError(t, sig2.Dial(context.Background()))
	require.NoError(t, sig2.Close())
	select {
	case <-sig2.Done():
		t.Fatal("Close() closed Done(); it must not (H7 reconnect-loop guard)")
	case <-time.After(150 * time.Millisecond):
	}
}

// TestContract_ConcurrentSend serializes concurrent writes (gorilla/websocket
// forbids concurrent WriteJSON) through signalframe.Sender.
func TestContract_ConcurrentSend(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.NoError(t, sig.Dial(context.Background()))

	const n = 64
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			payload := map[string]string{"i": strconv.Itoa(i)}
			errs <- sig.Send(NewMessage(MsgCandidate, "nodeB", payload))
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool { return srv.count(MsgCandidate) >= n }, 2*time.Second, 5*time.Millisecond)
}

// TestContract_ConcurrentSendAndClose races Send against Close: every call must
// either succeed or return "peerjs: not connected", never panic.
func TestContract_ConcurrentSendAndClose(t *testing.T) {
	srv := newFakeSignallerServer("k1")
	hs := srv.up()
	defer hs.Close()
	sig := newContractSignaller(t, hs, "nodeA", signalling.Options{PingInterval: time.Hour})
	require.NoError(t, sig.Dial(context.Background()))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := sig.Send(NewMessage(MsgCandidate, "nodeB", map[string]string{"i": strconv.Itoa(i)}))
			if err != nil {
				require.EqualError(t, err, "peerjs: not connected")
			}
		}(i)
	}
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, sig.Close())
	wg.Wait()
}
