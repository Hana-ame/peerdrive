package transport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// display_test.go — Display manager and remote display control tests (Issue #243).

// TestDisplay_RegisterAndList verifies display screen registration and filtering by channel.
// 发现背景：Issue #243（遥控屏幕同步与公共展示大屏：受控界面/多端画面联动）。
func TestDisplay_RegisterAndList(t *testing.T) {
	mgr := NewDisplayManager()
	s1 := &fakeSession{id: "session-tv"}
	s2 := &fakeSession{id: "session-pad"}

	scr1 := mgr.Register(s1, "tv-screen", "living-room", "Living Room TV")
	require.NotNil(t, scr1)
	assert.Equal(t, "tv-screen", scr1.ID)
	assert.Equal(t, "living-room", scr1.Channel)

	scr2 := mgr.Register(s2, "pad-screen", "bedroom", "Bedroom Pad")
	require.NotNil(t, scr2)

	// List all screens
	all := mgr.ListScreens("")
	assert.Len(t, all, 2)

	// Filter by channel
	lrScreens := mgr.ListScreens("living-room")
	require.Len(t, lrScreens, 1)
	assert.Equal(t, "tv-screen", lrScreens[0].ID)

	brScreens := mgr.ListScreens("bedroom")
	require.Len(t, brScreens, 1)
	assert.Equal(t, "pad-screen", brScreens[0].ID)
}

// TestDisplay_Unregister verifies display screen unregistration.
// 发现背景：Issue #243（展示大屏关闭连接时自动注销受控状态）。
func TestDisplay_Unregister(t *testing.T) {
	mgr := NewDisplayManager()
	s1 := &fakeSession{id: "sess-1"}

	mgr.Register(s1, "screen-1", "default", "Screen 1")
	assert.Len(t, mgr.ListScreens(""), 1)

	mgr.Unregister(s1)
	assert.Empty(t, mgr.ListScreens(""))
}

// TestDisplay_CastTargetScreenEnforcement verifies that casting requires the target session
// to have explicitly opened the controlled screen.
// 发现背景：Issue #243（安全约束：直接控制某个 session 屏幕时，该 session 也需要打开一个受控界面才可以）。
func TestDisplay_CastTargetScreenEnforcement(t *testing.T) {
	mgr := NewDisplayManager()
	s1 := &fakeSession{id: "controlled-screen"}
	mgr.Register(s1, "controlled-screen", "main", "Wall Screen")

	// 1. Cast to registered screen: succeeds
	cmd := DisplayFrame{
		MediaType: "image",
		Hash:      "abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234abcd1234",
		Title:     "sunset.jpg",
	}
	err := mgr.Cast(nil, "controlled-screen", "", cmd)
	require.NoError(t, err)

	require.NotEmpty(t, s1.sentJSON)
	var sent DisplayFrame
	err = json.Unmarshal(s1.sentJSON[0], &sent)
	require.NoError(t, err)
	assert.Equal(t, "display", sent.Type)
	assert.Equal(t, "show", sent.Action)
	assert.Equal(t, "image", sent.MediaType)
	assert.Equal(t, "sunset.jpg", sent.Title)

	// 2. Cast to non-registered session: MUST FAIL with clear error
	err = mgr.Cast(nil, "random-unopened-session", "", cmd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not in display mode")
}

// TestDisplay_CastChannelBroadcast verifies channel-wide broadcast when no specific session is targeted.
// 发现背景：Issue #243（公共大屏频道投屏：同一频道多个屏幕同步显示画面）。
func TestDisplay_CastChannelBroadcast(t *testing.T) {
	mgr := NewDisplayManager()
	s1 := &fakeSession{id: "tv-1"}
	s2 := &fakeSession{id: "tv-2"}

	mgr.Register(s1, "tv-1", "lobby", "Lobby Left")
	mgr.Register(s2, "tv-2", "lobby", "Lobby Right")

	cmd := DisplayFrame{
		MediaType: "video",
		Hash:      "1111222233334444555566667777888899990000aaaabbbbccccddddeeeeffff",
		Title:     "promo.mp4",
		Autoplay:  true,
		Loop:      true,
	}

	err := mgr.Cast(nil, "", "lobby", cmd)
	require.NoError(t, err)

	assert.NotEmpty(t, s1.sentJSON)
	assert.NotEmpty(t, s2.sentJSON)

	st := mgr.GetState("lobby")
	require.NotNil(t, st)
	assert.Equal(t, "lobby", st.Channel)
	assert.Equal(t, "video", st.MediaType)
	assert.True(t, st.Autoplay)
	assert.True(t, st.Loop)
	assert.True(t, st.Playing)
}

// TestDisplay_PlaybackControlAndClear verifies play/pause/seek controls and clearing the screen.
// 发现背景：Issue #243（遥控播控操作与画面清空）。
func TestDisplay_PlaybackControlAndClear(t *testing.T) {
	mgr := NewDisplayManager()
	s1 := &fakeSession{id: "cinema-screen"}
	mgr.Register(s1, "cinema-screen", "cinema", "Main Cinema")

	// Start playback
	err := mgr.Cast(nil, "cinema-screen", "", DisplayFrame{
		MediaType: "video",
		Hash:      "video-sha",
		Autoplay:  true,
	})
	require.NoError(t, err)

	// Pause
	err = mgr.Control(nil, "cinema-screen", "", DisplayFrame{
		ControlAction: "pause",
	})
	require.NoError(t, err)
	st := mgr.GetState("cinema")
	assert.False(t, st.Playing)

	// Seek
	err = mgr.Control(nil, "cinema-screen", "", DisplayFrame{
		ControlAction: "seek",
		Position:      124.5,
	})
	require.NoError(t, err)
	st = mgr.GetState("cinema")
	assert.Equal(t, 124.5, st.Position)

	// Clear
	err = mgr.Clear(nil, "cinema-screen", "")
	require.NoError(t, err)
	st = mgr.GetState("cinema")
	assert.Empty(t, st.MediaType)
}

// TestDisplay_RemoteAuthVerification verifies authentication for remote peer display control.
// 发现背景：Issue #243（遥控功能验证 auth：未授权对端发送 display 帧被拦截）。
func TestDisplay_RemoteAuthVerification(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "secret-display-token"

	remotePeer := &fakeSession{id: "remote-peer-1"}
	svc.bindConn(remotePeer)
	svc.SetPeerCapabilitiesForTest("remote-peer-1", []string{CapReq, CapShare, CapDisplay})

	// Register a local display screen
	localDisplay := &fakeSession{id: "local-screen"}
	svc.BindLocal(localDisplay)
	svc.displayMgr.Register(localDisplay, "screen-1", "default", "Screen 1")

	// 1. Remote peer attempts display without token -> REJECTED
	svc.dispatchFrame(remotePeer, svc.pending[remotePeer], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"display","action":"show","sessionId":"screen-1","mediaType":"image","hash":"h1","reqId":"r-unauth"}`),
	})

	errResp, ok := waitSent(remotePeer, "err", 2*time.Second)
	require.True(t, ok, "should receive err frame on unauthenticated request")
	assert.Equal(t, "UNAUTHORIZED", errResp["code"])

	// 2. Remote peer attempts display with correct token -> SUCCESS
	svc.dispatchFrame(remotePeer, svc.pending[remotePeer], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"display","action":"show","sessionId":"screen-1","mediaType":"image","hash":"h1","token":"secret-display-token","reqId":"r-auth"}`),
	})

	okResp, ok := waitSent(remotePeer, "display-resp", 2*time.Second)
	require.True(t, ok, "should receive display-resp frame on authenticated request")
	assert.Equal(t, "OK", okResp["code"])

	// Local screen should have received the show frame
	showFrame, ok := waitSent(localDisplay, "display", 2*time.Second)
	require.True(t, ok, "local display should receive display frame")
	assert.Equal(t, "show", showFrame["action"])
	assert.Equal(t, "image", showFrame["mediaType"])
}

// TestDisplay_CapabilitiesIntegration verifies that CapDisplay is registered and recognized.
// 发现背景：Issue #243（帧协议 capability 协商增加 display 功能位）。
func TestDisplay_CapabilitiesIntegration(t *testing.T) {
	assert.True(t, KnownCapabilities[CapDisplay])
	assert.Contains(t, DefaultNodeCapabilities, CapDisplay)
	assert.Equal(t, CapDisplay, VerbRequiredCap("display"))

	mask := CapsToBitset([]string{CapDisplay})
	assert.Equal(t, BitDisplay, mask)
	caps := CapsFromBitset(mask)
	assert.Equal(t, []string{CapDisplay}, caps)
}
