package transport

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
)

// stream_test.go — P2P live stream chunk distribution test suite (Issue #244).

// TestStream_CreateAndSubscribe verifies stream creation and subscription playlists.
// 发现背景：Issue #244（基于 Peerdrive 文件 SHA 的公共直播源与流分发：订阅流频道并获取切片清单）。
func TestStream_CreateAndSubscribe(t *testing.T) {
	mgr := NewStreamManager()
	pub := &fakeSession{id: "pub-1"}
	viewer := &fakeSession{id: "viewer-1"}

	// Create stream
	manifest, err := mgr.CreateStream(pub, "stream-live", "Chill Music Radio", "audio/mp3")
	require.NoError(t, err)
	assert.Equal(t, "stream-live", manifest.StreamID)
	assert.True(t, manifest.Active)
	assert.Equal(t, "pub-1", manifest.Publisher)

	// Subscribe
	subManifest, err := mgr.Subscribe(viewer, "stream-live")
	require.NoError(t, err)
	assert.Equal(t, "stream-live", subManifest.StreamID)
	assert.Empty(t, subManifest.Chunks)

	// List streams
	streams := mgr.ListStreams()
	require.Len(t, streams, 1)
	assert.Equal(t, "stream-live", streams[0].StreamID)
}

// TestStream_PushChunkAndBroadcast verifies chunk sequence assignment and real-time broadcasting.
// 发现背景：Issue #244（服务 Peerdrive 文件的方式，提供文件 SHA 切片广播给所有订阅者互相抓取）。
func TestStream_PushChunkAndBroadcast(t *testing.T) {
	mgr := NewStreamManager()
	pub := &fakeSession{id: "pub-1"}
	viewer1 := &fakeSession{id: "viewer-1"}
	viewer2 := &fakeSession{id: "viewer-2"}

	_, err := mgr.CreateStream(pub, "video-feed", "Cam 1", "video/mp4")
	require.NoError(t, err)

	_, err = mgr.Subscribe(viewer1, "video-feed")
	require.NoError(t, err)
	_, err = mgr.Subscribe(viewer2, "video-feed")
	require.NoError(t, err)

	// Push first chunk
	c1, err := mgr.PushChunk(pub, "video-feed", StreamChunk{
		Hash:     "sha-chunk-001",
		Size:     10240,
		Duration: 4.0,
		Title:    "Part 1",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), c1.Seq)

	// Both viewers receive the chunk frame
	require.NotEmpty(t, viewer1.sentJSON)
	require.NotEmpty(t, viewer2.sentJSON)

	var frame1 StreamFrame
	err = json.Unmarshal(viewer1.sentJSON[0], &frame1)
	require.NoError(t, err)
	assert.Equal(t, "stream", frame1.Type)
	assert.Equal(t, "chunk", frame1.Action)
	assert.Equal(t, "video-feed", frame1.StreamID)
	assert.Equal(t, "sha-chunk-001", frame1.Chunk.Hash)
	assert.Equal(t, int64(1), frame1.Chunk.Seq)

	// Push second chunk
	c2, err := mgr.PushChunk(pub, "video-feed", StreamChunk{
		Hash:     "sha-chunk-002",
		Size:     12400,
		Duration: 4.0,
		Title:    "Part 2",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(2), c2.Seq)

	// Verify manifest sliding playlist
	mf, err := mgr.GetManifest("video-feed")
	require.NoError(t, err)
	assert.Equal(t, int64(2), mf.CurrentSeq)
	assert.Len(t, mf.Chunks, 2)
}

// TestStream_CloseStream verifies stream closure and notifying viewers.
// 发现背景：Issue #244（直播源结束下线通知）。
func TestStream_CloseStream(t *testing.T) {
	mgr := NewStreamManager()
	pub := &fakeSession{id: "pub-1"}
	viewer := &fakeSession{id: "viewer-1"}

	_, err := mgr.CreateStream(pub, "stream-temp", "Temp", "text/plain")
	require.NoError(t, err)

	_, err = mgr.Subscribe(viewer, "stream-temp")
	require.NoError(t, err)

	err = mgr.CloseStream(pub, "stream-temp")
	require.NoError(t, err)

	// Viewer received close frame
	require.NotEmpty(t, viewer.sentJSON)
	var f StreamFrame
	err = json.Unmarshal(viewer.sentJSON[len(viewer.sentJSON)-1], &f)
	require.NoError(t, err)
	assert.Equal(t, "stream", f.Type)
	assert.Equal(t, "close", f.Action)

	// New chunk pushes to closed stream are rejected
	_, err = mgr.PushChunk(pub, "stream-temp", StreamChunk{Hash: "h1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not active")
}

// TestStream_RemoteAuthVerification verifies remote peer authentication for publishing streams.
// 发现背景：Issue #244（远程对端创建或推送切片需通过 remote control 授权校验）。
func TestStream_RemoteAuthVerification(t *testing.T) {
	svc := newTestPeerJSService(t)
	svc.cfg.RemoteControlEnable = true
	svc.cfg.RemoteControlToken = "stream-secret-token"

	remotePeer := &fakeSession{id: "remote-pub"}
	svc.bindConn(remotePeer)

	// 1. Remote peer attempts pub without token -> REJECTED
	svc.dispatchFrame(remotePeer, svc.pending[remotePeer], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"stream","action":"pub","streamId":"live-1","title":"Stream 1","reqId":"r-unauth"}`),
	})

	errResp, ok := waitSent(remotePeer, "err", 2*time.Second)
	require.True(t, ok, "should receive err frame on unauthenticated request")
	assert.Equal(t, "UNAUTHORIZED", errResp["code"])

	// 2. Remote peer attempts pub with valid token -> SUCCESS
	svc.dispatchFrame(remotePeer, svc.pending[remotePeer], peerjs.Frame{
		IsText: true,
		Data:   []byte(`{"type":"stream","action":"pub","streamId":"live-1","title":"Stream 1","token":"stream-secret-token","reqId":"r-auth"}`),
	})

	okResp, ok := waitSent(remotePeer, "stream", 2*time.Second)
	require.True(t, ok, "should receive stream response frame")
	assert.Equal(t, "pub-resp", okResp["action"])
	assert.Equal(t, "live-1", okResp["streamId"])
}

// TestStream_CapabilitiesIntegration verifies that CapStream is registered and recognized.
// 发现背景：Issue #244（帧协议 capability 协商增加 stream 功能位）。
func TestStream_CapabilitiesIntegration(t *testing.T) {
	assert.True(t, KnownCapabilities[CapStream])
	assert.Contains(t, DefaultNodeCapabilities, CapStream)
	assert.Equal(t, CapStream, VerbRequiredCap("stream"))

	mask := CapsToBitset([]string{CapStream})
	assert.Equal(t, BitStream, mask)
	caps := CapsFromBitset(mask)
	assert.Equal(t, []string{CapStream}, caps)
}
