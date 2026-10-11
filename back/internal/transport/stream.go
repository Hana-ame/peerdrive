package transport

import (
	"encoding/json"
	"fmt"

	"peerdrive/internal/log"
)

// stream.go — P2P live stream chunk broadcast and subscription wire protocol handling (Issue #244, #334).
// Live stream state, manifests, and chunk playlist management are implemented
// in stream_manager.go.

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
