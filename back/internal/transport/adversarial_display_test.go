package transport

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// adversarial_display_test.go — Adversarial and security fuzzing test suite for Display & Remote Control.
// 发现背景：Issue #257（为遥控投屏、流切片广播、端口转发与落盘链路增加攻击对抗测试套件）。

// TestAdversarial_Display_UnauthorizedPeerRejected tests that remote peers attempting display
// commands with no token, forged tokens, or invalid credentials are deterministically rejected.
// 发现背景：Issue #257（攻击对抗：远端未授权 Peer 伪造控制指令与 Token 穿透拦截）。
func TestAdversarial_Display_UnauthorizedPeerRejected(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "real-secure-display-token"

	attacker := &fakeSession{id: "attacker-peer-99"}
	svc.bindConn(attacker)

	localScreen := &fakeSession{id: "local-screen-target"}
	svc.BindLocal(localScreen)
	svc.displayMgr.Register(localScreen, "target-screen", "default", "Target Screen")

	attackPayloads := []struct {
		name    string
		token   string
		action  string
		payload string
	}{
		{
			name:    "empty token",
			token:   "",
			action:  "show",
			payload: `{"type":"display","action":"show","sessionId":"target-screen","mediaType":"image","hash":"safe-hash-1"}`,
		},
		{
			name:    "forged token",
			token:   "forged-fake-token",
			action:  "show",
			payload: `{"type":"display","action":"show","sessionId":"target-screen","mediaType":"image","hash":"safe-hash-1","token":"forged-fake-token"}`,
		},
		{
			name:    "unauthorized control action",
			token:   "wrong-token",
			action:  "control",
			payload: `{"type":"display","action":"control","sessionId":"target-screen","controlAction":"stop","token":"wrong-token"}`,
		},
		{
			name:    "unauthorized clear action",
			token:   "",
			action:  "clear",
			payload: `{"type":"display","action":"clear","sessionId":"target-screen"}`,
		},
	}

	for _, tc := range attackPayloads {
		t.Run(tc.name, func(t *testing.T) {
			svc.dispatchFrame(attacker, svc.pending[attacker], peerjs.Frame{
				IsText: true,
				Data:   []byte(tc.payload),
			})

			errResp, ok := waitSent(attacker, "err", 2*time.Second)
			require.True(t, ok, "should reject unauthenticated attack payload")
			assert.Equal(t, "UNAUTHORIZED", errResp["code"])
		})
	}
}

// TestAdversarial_Display_InactiveScreenTargetRejected tests user rule invariant:
// "需要直接控制某个session屏幕上出现什么。该session也需要打开一个受控界面才可以".
// A target session that is NOT in display mode must strictly reject cast commands.
// 发现背景：Issue #257（攻击对抗：针对未进入受控模式的普通 Session 强制投屏与劫持拦截）。
func TestAdversarial_Display_InactiveScreenTargetRejected(t *testing.T) {
	svc := newTestPeerJSService(t)
	localControl := &fakeSession{id: "local-operator"}
	svc.BindLocal(localControl)

	// Inactive peer that has NOT registered in display mode
	unregisteredID := "innocent-target-peer-123"

	err := svc.displayMgr.Cast(localControl, unregisteredID, "default", DisplayFrame{
		MediaType: "image",
		Hash:      "valid-hash-abc",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in display mode")
}

// TestAdversarial_Display_DangerousURLSchemeRejected tests that attempts to inject
// malicious pseudo-protocols (javascript:, file://, data:, vbscript:) are intercepted.
// 发现背景：Issue #257（攻击对抗：展示大屏恶意 URL Scheme 注入与 XSS/SSRF 载荷拦截）。
func TestAdversarial_Display_DangerousURLSchemeRejected(t *testing.T) {
	mgr := NewDisplayManager()
	s := &fakeSession{id: "screen-sess"}
	mgr.Register(s, "screen-1", "default", "Screen 1")

	maliciousURLs := []string{
		"javascript:alert(document.domain)",
		"JAVASCRIPT:alert(1)",
		"file:///etc/passwd",
		"FILE:///C:/Windows/System32/cmd.exe",
		"data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
		"vbscript:msgbox(1)",
		"ftp://evil.com/payload.exe",
		"gopher://127.0.0.1:6379/_flushall",
	}

	for _, evilURL := range maliciousURLs {
		t.Run("reject URL: "+evilURL, func(t *testing.T) {
			err := mgr.Cast(nil, "screen-1", "default", DisplayFrame{
				MediaType: "video",
				URL:       evilURL,
			})
			require.Error(t, err)
			assert.True(t, strings.Contains(err.Error(), "disallowed") || strings.Contains(err.Error(), "unsupported"))
		})
	}

	// Safe URLs should be accepted
	safeURLs := []string{
		"http://example.com/stream.m3u8",
		"https://cdn.example.com/video.mp4",
		"/api/v1/media/preview.jpg",
	}
	for _, safeURL := range safeURLs {
		err := mgr.Cast(nil, "screen-1", "default", DisplayFrame{
			MediaType: "video",
			URL:       safeURL,
		})
		require.NoError(t, err, "safe URL should be accepted: %s", safeURL)
	}
}

// TestAdversarial_Display_InvalidHashPathTraversal tests path traversal prevention in DisplayFrame.Hash.
// 发现背景：Issue #257（攻击对抗：Display 文件哈希路径穿越载荷防御）。
func TestAdversarial_Display_InvalidHashPathTraversal(t *testing.T) {
	mgr := NewDisplayManager()
	s := &fakeSession{id: "screen-sess"}
	mgr.Register(s, "screen-1", "default", "Screen 1")

	traversalHashes := []string{
		"../../etc/passwd",
		"..\\..\\windows\\system32",
		"validhash/../traversal",
		"hash\x00nullbyte",
	}

	for _, badHash := range traversalHashes {
		t.Run("reject hash: "+badHash, func(t *testing.T) {
			err := mgr.Cast(nil, "screen-1", "default", DisplayFrame{
				MediaType: "image",
				Hash:      badHash,
			})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "path traversal characters disallowed")
		})
	}
}

// TestAdversarial_Display_MalformedFramesAndFuzzing tests service resilience against
// truncated JSON, malformed syntax, unknown actions, and giant payloads.
// 发现背景：Issue #257（攻击对抗：畸形帧模糊测试与未知 Action 静默抵御）。
func TestAdversarial_Display_MalformedFramesAndFuzzing(t *testing.T) {
	svc := newTestPeerJSService(t)
	localSess := &fakeSession{id: "local-sess", local: true}
	svc.BindLocal(localSess)

	t.Run("truncated JSON safely dropped without crashing", func(t *testing.T) {
		assert.NotPanics(t, func() {
			svc.dispatchFrame(localSess, svc.pending[localSess], peerjs.Frame{
				IsText: true,
				Data:   []byte(`{"type":"display","action":"show"`),
			})
		})
		// Verify session remains functional after truncated packet
		svc.dispatchFrame(localSess, svc.pending[localSess], peerjs.Frame{
			IsText: true,
			Data:   []byte(`{"type":"display","action":"status","reqId":"health-check"}`),
		})
		resp, ok := waitSent(localSess, "display-resp", 2*time.Second)
		require.True(t, ok, "session should remain functional and answer status")
		assert.Equal(t, "STATUS", resp["code"])
	})

	invalidActionFrames := []struct {
		name string
		data []byte
	}{
		{
			name: "empty action",
			data: []byte(`{"type":"display","action":""}`),
		},
		{
			name: "unknown action",
			data: []byte(`{"type":"display","action":"destroy_everything"}`),
		},
	}

	for _, tc := range invalidActionFrames {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotPanics(t, func() {
				svc.dispatchFrame(localSess, svc.pending[localSess], peerjs.Frame{
					IsText: true,
					Data:   tc.data,
				})
			})

			errResp, ok := waitSent(localSess, "err", 2*time.Second)
			require.True(t, ok, "service should respond with error frame for invalid action")
			assert.NotEmpty(t, errResp["msg"])
		})
	}
}
