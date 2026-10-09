package transport

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// stream.go — P2P live stream chunk broadcast and subscription based on Peerdrive file SHAs (Issue #244).
//
// Background & Mechanism:
// "服务 peerdrive 文件的方式，提供文件 sha，然后所有人互相抓"
// 1. Live stream publisher continuously releases media segments / chunks, each addressed by its SHA256 hash.
// 2. The stream manifest maintains the live segment sequence and sliding window.
// 3. Subscribers receive chunk announcements via WebRTC/WS, and pull the content-addressed files from
//    the publisher or any peer that has already retrieved that chunk.
// 4. Fully decentralized distribution alleviates upstream bandwidth pressure on the origin broadcaster.

const maxStreamChunkWindow = 50

// StreamChunk represents a single content-addressed live stream segment.
type StreamChunk struct {
	Seq       int64   `json:"seq"`
	Hash      string  `json:"hash"` // 64-hex SHA-256 of the media segment
	Size      int64   `json:"size,omitempty"`
	Duration  float64 `json:"duration,omitempty"` // segment duration in seconds
	MimeType  string  `json:"mimeType,omitempty"` // e.g. "video/mp4", "image/jpeg"
	Timestamp int64   `json:"timestamp"`
	Title     string  `json:"title,omitempty"`
}

// StreamManifest describes an active live stream channel and its recent segment playlist.
type StreamManifest struct {
	StreamID   string        `json:"streamId"`
	Title      string        `json:"title"`
	Publisher  string        `json:"publisher"`
	MimeType   string        `json:"mimeType"`
	Active     bool          `json:"active"`
	CurrentSeq int64         `json:"currentSeq"`
	CreatedAt  time.Time     `json:"createdAt"`
	UpdatedAt  time.Time     `json:"updatedAt"`
	Chunks     []StreamChunk `json:"chunks"`
}

// StreamFrame wire protocol frame for live streaming.
type StreamFrame struct {
	Type     string            `json:"type"`                // always "stream"
	Action   string            `json:"action"`              // "pub", "chunk", "sub", "unsub", "list", "manifest", "close"
	StreamID string            `json:"streamId,omitempty"`
	Title    string            `json:"title,omitempty"`
	MimeType string            `json:"mimeType,omitempty"`
	Chunk    *StreamChunk      `json:"chunk,omitempty"`
	Manifest *StreamManifest   `json:"manifest,omitempty"`
	Streams  []*StreamManifest `json:"streams,omitempty"`
	Token    string            `json:"token,omitempty"`
	ReqID    string            `json:"reqId,omitempty"`
}

type streamRecord struct {
	manifest    *StreamManifest
	subscribers map[Session]bool
	publisher   Session
}

// StreamManager manages live stream channels, subscriptions, and chunk broadcasting.
type StreamManager struct {
	mu      sync.RWMutex
	streams map[string]*streamRecord
}

// NewStreamManager creates a new StreamManager instance.
func NewStreamManager() *StreamManager {
	return &StreamManager{
		streams: make(map[string]*streamRecord),
	}
}

// CreateStream registers a new live stream channel.
func (m *StreamManager) CreateStream(pub Session, streamID, title, mimeType string) (*StreamManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if streamID == "" {
		return nil, fmt.Errorf("streamId cannot be empty")
	}
	if title == "" {
		title = "Live Stream " + streamID
	}
	if mimeType == "" {
		mimeType = "video/mp4"
	}

	pubID := "local"
	if pub != nil {
		pubID = pub.ID()
	}

	manifest := &StreamManifest{
		StreamID:   streamID,
		Title:      title,
		Publisher:  pubID,
		MimeType:   mimeType,
		Active:     true,
		CurrentSeq: 0,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Chunks:     make([]StreamChunk, 0),
	}

	m.streams[streamID] = &streamRecord{
		manifest:    manifest,
		subscribers: make(map[Session]bool),
		publisher:   pub,
	}

	log.LogInfo("stream: created stream %s (title=%s, publisher=%s)", streamID, title, pubID)
	return manifest, nil
}

// PushChunk publishes a new media segment SHA to the stream and broadcasts it to all subscribers.
func (m *StreamManager) PushChunk(pub Session, streamID string, chunk StreamChunk) (*StreamChunk, error) {
	m.mu.Lock()
	rec, ok := m.streams[streamID]
	if !ok || !rec.manifest.Active {
		m.mu.Unlock()
		return nil, fmt.Errorf("stream %q is not active", streamID)
	}

	if chunk.Hash == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("chunk hash cannot be empty")
	}

	rec.manifest.CurrentSeq++
	chunk.Seq = rec.manifest.CurrentSeq
	if chunk.Timestamp == 0 {
		chunk.Timestamp = time.Now().UnixMilli()
	}
	if chunk.MimeType == "" {
		chunk.MimeType = rec.manifest.MimeType
	}

	rec.manifest.Chunks = append(rec.manifest.Chunks, chunk)
	if len(rec.manifest.Chunks) > maxStreamChunkWindow {
		rec.manifest.Chunks = rec.manifest.Chunks[len(rec.manifest.Chunks)-maxStreamChunkWindow:]
	}
	rec.manifest.UpdatedAt = time.Now()

	subs := make([]Session, 0, len(rec.subscribers))
	for s := range rec.subscribers {
		subs = append(subs, s)
	}
	m.mu.Unlock()

	// Broadcast chunk to all subscribers
	frame := StreamFrame{
		Type:     "stream",
		Action:   "chunk",
		StreamID: streamID,
		Chunk:    &chunk,
	}
	for _, s := range subs {
		_ = s.SendJSON(frame)
	}

	log.LogDebug("stream: chunk #%d (sha=%s) broadcast to %d subscribers on %s", chunk.Seq, chunk.Hash, len(subs), streamID)
	return &chunk, nil
}

// Subscribe registers a viewer session to receive real-time chunk notifications for a stream.
func (m *StreamManager) Subscribe(viewer Session, streamID string) (*StreamManifest, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.streams[streamID]
	if !ok {
		return nil, fmt.Errorf("stream %q not found", streamID)
	}

	if viewer != nil {
		rec.subscribers[viewer] = true
	}

	copyManifest := *rec.manifest
	copyManifest.Chunks = append([]StreamChunk(nil), rec.manifest.Chunks...)
	log.LogInfo("stream: session %s subscribed to stream %s", viewer.ID(), streamID)
	return &copyManifest, nil
}

// Unsubscribe removes a viewer session from a stream channel.
func (m *StreamManager) Unsubscribe(viewer Session, streamID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if rec, ok := m.streams[streamID]; ok {
		delete(rec.subscribers, viewer)
	}
}

// CloseStream closes a live stream channel and informs all subscribers.
func (m *StreamManager) CloseStream(pub Session, streamID string) error {
	m.mu.Lock()
	rec, ok := m.streams[streamID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("stream %q not found", streamID)
	}

	rec.manifest.Active = false
	rec.manifest.UpdatedAt = time.Now()
	subs := make([]Session, 0, len(rec.subscribers))
	for s := range rec.subscribers {
		subs = append(subs, s)
	}
	m.mu.Unlock()

	frame := StreamFrame{
		Type:     "stream",
		Action:   "close",
		StreamID: streamID,
	}
	for _, s := range subs {
		_ = s.SendJSON(frame)
	}

	log.LogInfo("stream: closed stream %s", streamID)
	return nil
}

// ListStreams returns all currently active streams.
func (m *StreamManager) ListStreams() []*StreamManifest {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*StreamManifest
	for _, r := range m.streams {
		cp := *r.manifest
		cp.Chunks = append([]StreamChunk(nil), r.manifest.Chunks...)
		res = append(res, &cp)
	}
	return res
}

// GetManifest returns the current manifest and chunk playlist for a stream.
func (m *StreamManager) GetManifest(streamID string) (*StreamManifest, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	rec, ok := m.streams[streamID]
	if !ok {
		return nil, fmt.Errorf("stream %q not found", streamID)
	}
	cp := *rec.manifest
	cp.Chunks = append([]StreamChunk(nil), rec.manifest.Chunks...)
	return &cp, nil
}

// UnregisterSession cleans up any subscriptions or streams associated with the closing session.
func (m *StreamManager) UnregisterSession(sess Session) {
	if sess == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, rec := range m.streams {
		delete(rec.subscribers, sess)
	}
}

// serveStream handles inbound stream wire frames.
func (s *PeerJSService) serveStream(c Session, st *connState, raw []byte) {
	var sf StreamFrame
	if err := json.Unmarshal(raw, &sf); err != nil || sf.Action == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "invalid stream frame", ReqID: sf.ReqID})
		return
	}

	if s.streamMgr == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "stream manager not initialized", ReqID: sf.ReqID})
		return
	}

	// Remote publishing or stream closing requires remote control authorization
	if (sf.Action == "pub" || sf.Action == "chunk" || sf.Action == "close") && !isSelfSession(c) {
		if !s.isRemoteControlAllowed(c, sf.Token, "POST", "/stream") {
			log.LogWarn("remote-stream: unauthorized attempt from peer %s (action=%s)", c.ID(), sf.Action)
			_ = c.SendJSON(dcResp{
				Type:  "err",
				Code:  "UNAUTHORIZED",
				Msg:   "remote control unauthorized: valid auth token required",
				ReqID: sf.ReqID,
			})
			return
		}
	}

	switch sf.Action {
	case "pub":
		manifest, err := s.streamMgr.CreateStream(c, sf.StreamID, sf.Title, sf.MimeType)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: sf.ReqID})
			return
		}
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "pub-resp",
			StreamID: manifest.StreamID,
			Manifest: manifest,
			ReqID:    sf.ReqID,
		})

	case "chunk":
		if sf.Chunk == nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: "chunk payload required", ReqID: sf.ReqID})
			return
		}
		chunk, err := s.streamMgr.PushChunk(c, sf.StreamID, *sf.Chunk)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: sf.ReqID})
			return
		}
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "chunk-ack",
			StreamID: sf.StreamID,
			Chunk:    chunk,
			ReqID:    sf.ReqID,
		})

	case "sub":
		manifest, err := s.streamMgr.Subscribe(c, sf.StreamID)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: sf.ReqID})
			return
		}
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "manifest",
			StreamID: sf.StreamID,
			Manifest: manifest,
			ReqID:    sf.ReqID,
		})

	case "unsub":
		s.streamMgr.Unsubscribe(c, sf.StreamID)
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "unsub-ack",
			StreamID: sf.StreamID,
			ReqID:    sf.ReqID,
		})

	case "list":
		streams := s.streamMgr.ListStreams()
		_ = c.SendJSON(StreamFrame{
			Type:    "stream",
			Action:  "list-resp",
			Streams: streams,
			ReqID:   sf.ReqID,
		})

	case "manifest":
		manifest, err := s.streamMgr.GetManifest(sf.StreamID)
		if err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: sf.ReqID})
			return
		}
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "manifest",
			StreamID: sf.StreamID,
			Manifest: manifest,
			ReqID:    sf.ReqID,
		})

	case "close":
		if err := s.streamMgr.CloseStream(c, sf.StreamID); err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: sf.ReqID})
			return
		}
		_ = c.SendJSON(StreamFrame{
			Type:     "stream",
			Action:   "close-ack",
			StreamID: sf.StreamID,
			ReqID:    sf.ReqID,
		})

	default:
		_ = c.SendJSON(dcResp{Type: "err", Msg: fmt.Sprintf("unknown stream action %q", sf.Action), ReqID: sf.ReqID})
	}
}

// StreamManager returns the PeerJSService's StreamManager instance.
func (s *PeerJSService) StreamManager() *StreamManager {
	return s.streamMgr
}
