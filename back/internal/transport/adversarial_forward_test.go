package transport

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// adversarial_forward_test.go — Adversarial security tests for Port Forwarding tunnels.
// 发现背景：Issue #257（为遥控投屏、流切片广播、端口转发与落盘链路增加攻击对抗测试套件）。

// TestAdversarial_Forward_ReplayAttack tests that once a challenge nonce is consumed,
// replaying the exact same valid HMAC signature is immediately rejected.
// 发现背景：Issue #257（攻击对抗：针对端口转发 Nonce 质询重放攻击防御）。
func TestAdversarial_Forward_ReplayAttack(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"legit-key": {echoPort}})

	sess := &fakeSession{id: "replay-client"}
	_ = bindFakeServer(t, svc, sess)

	// Step 1: Normal challenge request
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-open","port":%d,"reqId":"req-replay-1"}`, echoPort))})
	ch := waitFrameType(t, sess, "fwd-challenge")
	nonce, _ := ch["nonce"].(string)
	require.NotEmpty(t, nonce)

	validHMAC := fwdHMAC("legit-key", nonce)

	// Step 2: First authentication consumes the nonce -> Success
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-auth","hmac":"%s","reqId":"req-replay-1"}`, validHMAC))})
	waitFrameType(t, sess, "fwd-ok")

	// Step 3: Replay attack — Attacker resends the same consumed HMAC signature and reqId
	sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-auth","hmac":"%s","reqId":"req-replay-1"}`, validHMAC))})
	replayErr := waitFrameType(t, sess, "fwd-err")
	assert.Equal(t, "no challenge", replayErr["msg"])
}

// TestAdversarial_Forward_PortScanningBypass tests that an attacker with a valid key for
// port A cannot open or scan unauthorized ports (port 22, 3306, 6379, 0, or out of range).
// 发现背景：Issue #257（攻击对抗：端口扫描与非白名单端口越权穿透防御）。
func TestAdversarial_Forward_PortScanningBypass(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	// Whitelist only echoPort
	svc.SetForwardRules(map[string][]int{"client-key": {echoPort}})

	sess := &fakeSession{id: "scanner-client"}
	_ = bindFakeServer(t, svc, sess)

	unauthorizedPorts := []int{
		22,    // SSH
		3306,  // MySQL
		6379,  // Redis
		8080,  // Different HTTP port
		0,     // Invalid port
		70000, // Out of range port
	}

	for _, badPort := range unauthorizedPorts {
		t.Run(fmt.Sprintf("port-%d", badPort), func(t *testing.T) {
			sess := &fakeSession{id: fmt.Sprintf("scanner-%d", badPort)}
			_ = bindFakeServer(t, svc, sess)

			reqID := fmt.Sprintf("scan-%d", badPort)
			sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-open","port":%d,"reqId":"%s"}`, badPort, reqID))})

			if badPort <= 0 || badPort > 65535 {
				// Invalid port immediately rejected at fwd-open
				errFrame := waitFrameType(t, sess, "fwd-err")
				assert.Equal(t, "invalid port", errFrame["msg"])
			} else {
				// Gets challenge, but auth fails due to port not being whitelisted for key
				ch := waitFrameType(t, sess, "fwd-challenge")
				nonce, _ := ch["nonce"].(string)
				require.NotEmpty(t, nonce)

				sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-auth","hmac":"%s","reqId":"%s"}`, fwdHMAC("client-key", nonce), reqID))})
				errFrame := waitFrameType(t, sess, "fwd-err")
				assert.Equal(t, "port not authorized", errFrame["msg"])
			}
		})
	}
}

// TestAdversarial_Forward_CorruptedHMACAndSignatureTampering tests that flipped bits,
// wrong keys, truncated signatures, or corrupted hex are deterministically rejected.
// 发现背景：Issue #257（攻击对抗：伪造 HMAC 签名与签名位翻转篡改防御）。
func TestAdversarial_Forward_CorruptedHMACAndSignatureTampering(t *testing.T) {
	svc := newTestPeerJSService(t)
	echoPort, stopEcho := startEchoServer(t, "srv")
	defer stopEcho()
	svc.SetForwardRules(map[string][]int{"secret-key-1": {echoPort}})

	sess := &fakeSession{id: "tamper-client"}
	_ = bindFakeServer(t, svc, sess)

	corruptedPayloads := []struct {
		name string
		hmac string
	}{
		{name: "wrong key", hmac: fwdHMAC("wrong-key-guess", "0123456789abcdef0123456789abcdef")},
		{name: "flipped bit", hmac: "0000000000000000000000000000000000000000000000000000000000000000"},
		{name: "truncated hmac", hmac: "abcdef"},
		{name: "empty hmac", hmac: ""},
		{name: "invalid hex chars", hmac: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}

	for i, tc := range corruptedPayloads {
		reqID := fmt.Sprintf("corrupt-%d", i)
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-open","port":%d,"reqId":"%s"}`, echoPort, reqID))})
		ch := waitFrameType(t, sess, "fwd-challenge")
		require.NotEmpty(t, ch["nonce"])

		sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-auth","hmac":"%s","reqId":"%s"}`, tc.hmac, reqID))})
		errFrame := waitFrameType(t, sess, "fwd-err")
		assert.Equal(t, "unauthorized", errFrame["msg"])
	}
}

// TestAdversarial_Forward_ChallengePoolFlooding tests that rapid floods of fwd-open
// do not cause unbounded memory growth and trigger the challenge flood limit.
// 发现背景：Issue #257（攻击对抗：质询表内存洪水与 DoS 防御）。
func TestAdversarial_Forward_ChallengePoolFlooding(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.SetForwardRules(map[string][]int{"key": {8080}})

	sess := &fakeSession{id: "flooder-client"}
	_ = bindFakeServer(t, svc, sess)

	// Max unconsumed challenges is fwdNonceMax (64)
	flooded := false
	for i := 0; i < 70; i++ {
		reqID := fmt.Sprintf("flood-%d", i)
		sess.feed(peerjs.Frame{IsText: true, Data: []byte(fmt.Sprintf(`{"type":"fwd-open","port":8080,"reqId":"%s"}`, reqID))})
	}

	// Within sent frames, we should observe "too many challenges"
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, fr := range sess.sentFrames() {
			if fr.header["type"] == "fwd-err" && fr.header["msg"] == "too many challenges" {
				flooded = true
				break
			}
		}
		if flooded {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	assert.True(t, flooded, "challenge flooding should trigger 'too many challenges' error")
}
