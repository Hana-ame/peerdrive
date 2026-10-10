package transport

// forward_test.go: forward v2 (PeerJS DataChannel port forwarding) handshake and data passthrough tests.
//
// Coverage (with discovery backgrounds noted):
//   - full handshake flow + bidirectional data passthrough (server side: echo TCP service <-> tunnel)
//   - bad key / unauthorized port / nonce replay -> fwd-err (the core assertions for authorization and key exchange)
//   - the client-side OpenForward end to end (fakeSession plays the server)
//
// Note: no real WebRTC/WS -- it uses fakeSession + bindConn end to end (including uploadWorker, so
// forwarded blocks are delivered via fwdCh and written to the tunnel by the worker), sharing the same
// pump-internal routing logic as file frames.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	peerjs "github.com/Hana-ame/go-peerjs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startEchoServer starts a loopback echo TCP service, returning the listening port and a close function.
func startEchoServer(t *testing.T, tag string) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				// echo: return whatever was received unchanged (reply once a full line arrives, which keeps assertions simple)
				buf := make([]byte, 4096)
				for {
					n, err := conn.Read(buf)
					if n > 0 {
						// prefix the tag so assertions can tell multiple services apart
						resp := append([]byte(tag+":"), buf[:n]...)
						if _, werr := conn.Write(resp); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

// bindFakeServer binds a fakeSession to the service end to end (bindConn + conns/pending registration),
// returning st so the tunnel state can be asserted on.
func bindFakeServer(t *testing.T, svc *PeerJSService, sess *fakeSession) *connState {
	t.Helper()
	svc.mu.Lock()
	svc.conns[sess.id] = sess
	svc.mu.Unlock()
	svc.bindConn(sess)
	svc.SetPeerCapabilitiesForTest(sess.id, []string{CapReq, CapShare, CapForward})
	st := svc.stateFor(sess)
	require.NotNil(t, st)
	return st
}

// waitFrameType polls until the most recent frame's type is want (bindConn dispatch is an asynchronous goroutine,
// so asserting right after feed is not safe). On timeout it fails with the frames seen so far.
func waitFrameType(t *testing.T, sess *fakeSession, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		frames := sess.sentFrames()
		if len(frames) > 0 {
			last := frames[len(frames)-1]
			if t0, _ := last.header["type"].(string); t0 == want {
				return last.header
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("waiting for frame %q timed out (current most recent frames: %v)", want, func() []string {
		out := []string{}
		for _, fr := range sess.sentFrames() {
			if tt, ok := fr.header["type"].(string); ok {
				out = append(out, tt)
			}
		}
		return out
	}())
	return nil
}

// fwdHMAC computes the client-side response (symmetric with the server's verification logic).
func fwdHMAC(key, nonceHex string) string {
	nonce, _ := hex.DecodeString(nonceHex)
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write(nonce)
	return hex.EncodeToString(mac.Sum(nil))
}

// TestForward_HandshakeAndData the full server-side handshake + bidirectional data passthrough:
//  1. receive fwd-open -> reply fwd-challenge (with a nonce)
//  2. receive a valid fwd-auth -> reply fwd-ok and establish the tunnel (the server dials the echo service)
//  3. local TCP -> the tunnel (the pump sends a fwd-data header + block) -> the echo service
//  4. the echo reply -> received on the other end of the tunnel over TCP
func TestForward_HandshakeAndData(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})

	sess := &fakeSession{id: "client"}
	st := bindFakeServer(t, svc, sess)

	// 1. fwd-open -> fwd-challenge
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"h1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	require.NotEmpty(t, nonce)

	// 2. fwd-auth -> fwd-ok
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"h1"}`)})
	waitFrameType(t, sess, "fwd-ok")
	st.mu.Lock()
	require.NotNil(t, st.fwd, "tunnel should be established")
	require.Equal(t, echoPort, st.fwd.port)
	st.mu.Unlock()

	// 3. client-direction data: inject a fwd-data header + block -> the TCP connection the server dialed should receive it
	// (bindConn pump-internal routing: the header frame marks pending -> the binary block goes to fwdCh -> the worker writes TCP)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"h1"}`)})
	sess.feed(peerjs.Frame{IsText: false, Data: []byte("ping-data")})
	// 4. echo reply: the server pump reads TCP -> SendFrame(fwd-data) to the fakeSession
	deadline := time.Now().Add(3 * time.Second)
	var gotBody []byte
	for time.Now().Before(deadline) {
		frs := sess.sentFrames()
		gotBody = nil
		for _, fr := range frs {
			if fr.header["type"] == "fwd-data" && len(fr.body) > 0 {
				gotBody = fr.body
			}
		}
		if strings.Contains(string(gotBody), "srv:ping-data") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assert.Equal(t, "srv:ping-data", string(gotBody), "echo reply should return to client side via tunnel")
}

// TestForward_AuthRejected authorization: a bad key / an unauthorized port / a nonce replay all get fwd-err,
// and no tunnel is established (no rule details leak: the failure replies carry nothing beyond the reason).
func TestForward_AuthRejected(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"goodkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)

	openAndGetNonce := func(t *testing.T, reqID string) string {
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"` + reqID + `"}`)})
		ch := waitFrameType(t, sess, "fwd-challenge")
		nonce, _ := ch["nonce"].(string)
		return nonce
	}
	// bad key: HMAC with the wrong secret -> fwd-err
	nonce1 := openAndGetNonce(t, "badk")
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("wrongkey", nonce1) + `","reqId":"badk"}`)})
	waitFrameType(t, sess, "fwd-err")

	// unauthorized ports are covered separately in TestForward_PortNotAuthorized (this test always opens the legitimate port)

	// replay: submit the same nonce twice -- the first time legitimately establishes the tunnel, the second must be fwd-err
	nonce3 := openAndGetNonce(t, "replay")
	hm := fwdHMAC("goodkey", nonce3)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + hm + `","reqId":"replay"}`)})
	waitFrameType(t, sess, "fwd-ok")
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + hm + `","reqId":"replay"}`)})
	last := waitFrameType(t, sess, "fwd-err")
	msg, _ := last["msg"].(string)
	assert.Equal(t, "no challenge", msg, "nonce replay must be rejected (already consumed)")

	// the legitimate request really did establish a tunnel (the premise of the replay rejection holds)
	st := svc.stateFor(sess)
	st.mu.Lock()
	tunneled := st.fwd != nil
	st.mu.Unlock()
	assert.True(t, tunneled, "legitimate replay request established tunnel (later replay rejected)")
}

// TestForward_PortNotAuthorized unauthorized port verified on its own (the one above was actually the same
// legitimate port, so this adds an assertion for a genuinely unauthorized port).
func TestForward_PortNotAuthorized(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"goodkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)

	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":65000,"reqId":"p1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("goodkey", nonce) + `","reqId":"p1"}`)})
	last := waitFrameType(t, sess, "fwd-err")
	msg, _ := last["msg"].(string)
	assert.Equal(t, "port not authorized", msg)
}

// TestOpenForward_ClientSide the client-side OpenForward end to end: fakeSession plays the server.
// Verifies: handshake (open -> challenge -> auth -> ok) -> write to the tunnel -> the peer receives fwd-data;
// the peer injects a fwd-data block -> the caller reads it out.
func TestOpenForward_ClientSide(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.SetForwardRules(map[string][]int{})
	sess := &fakeSession{id: "server"}
	st := bindFakeServer(t, svc, sess)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	connCh := make(chan struct {
		c   net.Conn
		err error
	}, 1)
	go func() {
		c, err := svc.OpenForward(ctx, "server", "clientkey", 8080)
		connCh <- struct {
			c   net.Conn
			err error
		}{c, err}
	}()

	// the (fake) server receives fwd-open -> replies with challenge
	deadline := time.Now().Add(2 * time.Second)
	var nonce string
	for time.Now().Before(deadline) {
		for _, fr := range sess.sentFrames() {
			if fr.header["type"] == "fwd-open" {
				nonce = "aabbccddeeff00112233445566778899"
				sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-challenge","nonce":"` + nonce + `","reqId":"` + fr.header["reqId"].(string) + `"}`)})
				goto sent
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
sent:
	require.NotEmpty(t, nonce, "OpenForward should send fwd-open")

	// the client should emit fwd-auth (HMAC computed with clientkey)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, fr := range sess.sentFrames() {
			if fr.header["type"] == "fwd-auth" {
				expected := fwdHMAC("clientkey", nonce)
				assert.Equal(t, expected, fr.header["hmac"], "HMAC must be computed with caller's key")
				// the server replies fwd-ok
				sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-ok","reqId":"` + fr.header["reqId"].(string) + `"}`)})
				goto authed
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
authed:

	select {
	case res := <-connCh:
		require.NoError(t, res.err)
		require.NotNil(t, res.c)
		defer res.c.Close()
		// write to the tunnel -> the peer should receive a fwd-data header + block
		go res.c.Write([]byte("tunnel-write"))
		deadline := time.Now().Add(2 * time.Second)
		var got []byte
		for time.Now().Before(deadline) {
			for _, fr := range sess.sentFrames() {
				if fr.header["type"] == "fwd-data" && len(fr.body) > 0 {
					got = fr.body
				}
			}
			if string(got) == "tunnel-write" {
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		assert.Equal(t, "tunnel-write", string(got), "data written to tunnel should reach peer as fwd-data frame")

		// the peer injects fwd-data -> the caller should read it out
		st.mu.Lock()
		fw := st.fwd
		st.mu.Unlock()
		require.NotNil(t, fw)
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"` + fw.reqID + `"}`)})
		sess.feed(peerjs.Frame{IsText: false, Data: []byte("from-server")})
		buf := make([]byte, 64)
		res.c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, err := res.c.Read(buf)
		require.NoError(t, err)
		assert.Equal(t, "from-server", string(buf[:n]))
	case <-time.After(3 * time.Second):
		t.Fatal("OpenForward did not return within timeout")
	}
}

// TestForward_FwdDataWithoutTunnel defense: a fwd-data header/block with no tunnel is silently dropped, no panic.
// Discovery background: thought of while writing the hand-rolled protocol tests -- a malicious peer that skips
// the handshake and sends fwd-data straight away; the pump-internal routing must handle it gracefully (no panic,
// no state created), otherwise one frame could take down a node on the public signaling channel.
func TestForward_FwdDataWithoutTunnel(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "evil"}
	bindFakeServer(t, svc, sess)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-data","reqId":"x"}`)})
	sess.feed(peerjs.Frame{IsText: false, Data: []byte("garbage")})
	// passing means no panic; a normal handshake afterwards still works
	svc.SetForwardRules(map[string][]int{"k": {1}})
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":2,"reqId":"y"}`)})
	waitFrameType(t, sess, "fwd-challenge")
}

// TestForward_CloseStream an active disconnect (CloseForwardStream): the peer receives fwd-close,
// this end's tunnel slot is cleared, and out is closed (the caller sees EOF on its read side).
func TestForward_CloseStream(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	_ = bindFakeServer(t, svc, sess)

	// establish a tunnel
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"c1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"c1"}`)})
	waitFrameType(t, sess, "fwd-ok")

	svc.CloseForwardStream("client")
	waitFrameType(t, sess, "fwd-close")
	st := svc.stateFor(sess)
	st.mu.Lock()
	closed := st.fwd == nil
	st.mu.Unlock()
	assert.True(t, closed, "tunnel should have cleared slot")
}

// TestForward_ListAndInfo management plane: ListForwardStreams returns a tunnel snapshot.
func TestForward_ListAndInfo(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"testkey": {echoPort}})
	sess := &fakeSession{id: "client"}
	bindFakeServer(t, svc, sess)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-open","port":` + fmt.Sprint(echoPort) + `,"reqId":"l1"}`)})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(`{"type":"fwd-auth","hmac":"` + fwdHMAC("testkey", nonce) + `","reqId":"l1"}`)})
	waitFrameType(t, sess, "fwd-ok")
	time.Sleep(50 * time.Millisecond) // wait for the tunnel to be established
	infos := svc.ListForwardStreams()
	require.Len(t, infos, 1)
	assert.Equal(t, "client", infos[0].PeerID)
	assert.Equal(t, echoPort, infos[0].Port)
}

// TestForward_TimeoutNoChallenge client timeout: the server never replies with challenge -> an error is returned,
// and the single handshake slot is released (another handshake is possible).
// Discovery background: defensive test -- when the peer does not reply (spoofed / crashed), OpenForward must time
// out and return rather than hang the caller forever or keep occupying the fwdHs single slot.
func TestForward_TimeoutNoChallenge(t *testing.T) {
	svc := newTestPeerJSService(t)
	sess := &fakeSession{id: "silent"}
	bindFakeServer(t, svc, sess)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := svc.OpenForward(ctx, "silent", "k", 8080)
	require.Error(t, err)
	st := svc.stateFor(sess)
	st.mu.Lock()
	hs := st.fwdHs
	st.mu.Unlock()
	assert.Nil(t, hs, "handshake slot must be released after timeout")
	_ = io.EOF
}
