package transport

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// adversarial_stream_test.go — Adversarial and security test suite for P2P Live Stream Broadcast.
// 发现背景：Issue #257（为遥控投屏、流切片广播、端口转发与落盘链路增加攻击对抗测试套件）。

// TestAdversarial_Stream_HashPoisoningAndTraversal tests that empty hashes and path traversal
// injection payloads in stream chunks are strictly blocked.
// 发现背景：Issue #257（攻击对抗：P2P 流切片哈希投毒与路径穿越载荷防御）。
func TestAdversarial_Stream_HashPoisoningAndTraversal(t *testing.T) {
	mgr := NewStreamManager()
	pub := &fakeSession{id: "legit-publisher"}

	_, err := mgr.CreateStream(pub, "live-cam", "Live Stream", "video/mp4")
	require.NoError(t, err)

	poisonedHashes := []string{
		"",
		"../../etc/shadow",
		"..\\..\\windows\\system32",
		"sha256/../../traversal",
		"badhash\x00extra",
	}

	for _, badHash := range poisonedHashes {
		t.Run("poisoned hash: "+badHash, func(t *testing.T) {
			_, err := mgr.PushChunk(pub, "live-cam", StreamChunk{
				Hash: badHash,
			})
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "empty") || strings.Contains(err.Error(), "path traversal"))
		})
	}
}

// TestAdversarial_Stream_StreamHijacking tests that an active stream cannot be overwritten
// or hijacked by another publisher.
// 发现背景：Issue #257（攻击对抗：活跃流频道所有权保护与覆盖劫持防御）。
func TestAdversarial_Stream_StreamHijacking(t *testing.T) {
	mgr := NewStreamManager()
	legitPub := &fakeSession{id: "legit-publisher"}
	attackerPub := &fakeSession{id: "attacker-publisher"}

	// 1. Legit publisher creates stream
	_, err := mgr.CreateStream(legitPub, "exclusive-channel", "Original Stream", "video/mp4")
	require.NoError(t, err)

	// 2. Attacker attempts to overwrite the active stream
	_, err = mgr.CreateStream(attackerPub, "exclusive-channel", "Hijacked Stream", "video/mp4")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already active and owned by publisher")

	// 3. Legit publisher updating stream should be allowed
	_, err = mgr.CreateStream(legitPub, "exclusive-channel", "Updated Stream", "video/mp4")
	require.NoError(t, err)
}

// TestAdversarial_Stream_UnauthorizedChunkPush tests that unauthorized sessions cannot
// push chunks into a stream owned by someone else.
// 发现背景：Issue #257（攻击对抗：第三方仿冒推流与未授权切片注入拦截）。
func TestAdversarial_Stream_UnauthorizedChunkPush(t *testing.T) {
	mgr := NewStreamManager()
	legitPub := &fakeSession{id: "legit-publisher"}
	attackerPub := &fakeSession{id: "attacker-publisher"}

	_, err := mgr.CreateStream(legitPub, "live-feed", "Original", "video/mp4")
	require.NoError(t, err)

	// Attacker tries to inject chunk into legitPub's stream
	_, err = mgr.PushChunk(attackerPub, "live-feed", StreamChunk{
		Hash: "valid-chunk-sha256-abc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized to push chunks")
}

// TestAdversarial_Stream_UnauthorizedClose tests that unauthorized sessions cannot
// terminate a stream owned by someone else.
// 发现背景：Issue #257（攻击对抗：恶意关闭与注销他人活跃流频道防御）。
func TestAdversarial_Stream_UnauthorizedClose(t *testing.T) {
	mgr := NewStreamManager()
	legitPub := &fakeSession{id: "legit-publisher"}
	attackerPub := &fakeSession{id: "attacker-publisher"}

	_, err := mgr.CreateStream(legitPub, "live-feed", "Original", "video/mp4")
	require.NoError(t, err)

	// Attacker tries to close the stream
	err = mgr.CloseStream(attackerPub, "live-feed")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not authorized to close stream")

	// Legit publisher can close it
	err = mgr.CloseStream(legitPub, "live-feed")
	require.NoError(t, err)
}

// TestAdversarial_Stream_RemotePubWithoutToken tests that remote peers attempting
// wire stream pub/chunk/close actions without valid authorization tokens are rejected.
// 发现背景：Issue #257（攻击对抗：远端未授权 Peer 发起流发布/广播拦截）。
func TestAdversarial_Stream_RemotePubWithoutToken(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "stream-secret-token"

	attacker := &fakeSession{id: "remote-unauthorized-streamer"}
	svc.bindConn(attacker)

	// 1. Remote pub without token -> UNAUTHORIZED
	svc.dispatchFrame(attacker, svc.pending[attacker], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"stream","action":"pub","streamId":"evil-stream","title":"Evil Stream"}`),
	})

	errResp, ok := waitSent(attacker, "err", 2*time.Second)
	require.True(t, ok, "should reject unauthenticated stream pub request")
	assert.Equal(t, "UNAUTHORIZED", errResp["code"])

	// 2. Remote chunk push without token -> UNAUTHORIZED
	svc.dispatchFrame(attacker, svc.pending[attacker], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"stream","action":"chunk","streamId":"evil-stream","chunk":{"hash":"some-sha"}}`),
	})

	errResp2, ok := waitSent(attacker, "err", 2*time.Second)
	require.True(t, ok, "should reject unauthenticated stream chunk request")
	assert.Equal(t, "UNAUTHORIZED", errResp2["code"])
}
