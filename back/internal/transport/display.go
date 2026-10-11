package transport

import (
	"encoding/json"
	"fmt"

	"peerdrive/internal/log"
)

// display.go — Remote screen display control wire protocol handling (Issue #243).
// Screen session registration and channel playback management logic is implemented
// in display_manager.go.

// serveDisplay handles inbound display frames.
func (s *PeerJSService) serveDisplay(c Session, st *connState, raw []byte) {
	var df DisplayFrame
	if err := json.Unmarshal(raw, &df); err != nil || df.Action == "" {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "invalid display frame", ReqID: df.ReqID})
		return
	}

	// Remote peers must pass remote control authorization (Issue #234).
	if !isSelfSession(c) {
		if !s.isRemoteControlAllowed(c, df.Token, "POST", "/display") {
			log.LogWarn("remote-display: unauthorized attempt from peer %s", c.ID())
			_ = c.SendJSON(dcResp{
				Type:  "err",
				Code:  "UNAUTHORIZED",
				Msg:   "remote control unauthorized: valid auth token required",
				ReqID: df.ReqID,
			})
			return
		}
	}

	if s.displayMgr == nil {
		_ = c.SendJSON(dcResp{Type: "err", Msg: "display manager not initialized", ReqID: df.ReqID})
		return
	}

	switch df.Action {
	case "register":
		screen := s.displayMgr.Register(c, df.SessionID, df.Channel, df.Name)
		_ = c.SendJSON(DisplayResponse{
			Type:    "display-resp",
			Code:    "REGISTERED",
			ReqID:   df.ReqID,
			Screens: []*DisplayScreen{screen},
		})
		// If current channel has active content, send it to sync the new screen
		currState := s.displayMgr.GetState(screen.Channel)
		if currState != nil && currState.MediaType != "" {
			_ = c.SendJSON(DisplayFrame{
				Type:      "display",
				Action:    "show",
				Channel:   currState.Channel,
				MediaType: currState.MediaType,
				Hash:      currState.Hash,
				URL:       currState.URL,
				Title:     currState.Title,
				MimeType:  currState.MimeType,
				Autoplay:  currState.Autoplay,
				Loop:      currState.Loop,
				Position:  currState.Position,
				Volume:    &currState.Volume,
			})
		}

	case "unregister":
		s.displayMgr.Unregister(c)
		_ = c.SendJSON(DisplayResponse{
			Type:  "display-resp",
			Code:  "UNREGISTERED",
			ReqID: df.ReqID,
		})

	case "show":
		if err := s.displayMgr.Cast(c, df.SessionID, df.Channel, df); err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: df.ReqID})
			return
		}
		_ = c.SendJSON(DisplayResponse{
			Type:  "display-resp",
			Code:  "OK",
			ReqID: df.ReqID,
		})

	case "control":
		if err := s.displayMgr.Control(c, df.SessionID, df.Channel, df); err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: df.ReqID})
			return
		}
		_ = c.SendJSON(DisplayResponse{
			Type:  "display-resp",
			Code:  "OK",
			ReqID: df.ReqID,
		})

	case "clear":
		if err := s.displayMgr.Clear(c, df.SessionID, df.Channel); err != nil {
			_ = c.SendJSON(dcResp{Type: "err", Msg: err.Error(), ReqID: df.ReqID})
			return
		}
		_ = c.SendJSON(DisplayResponse{
			Type:  "display-resp",
			Code:  "OK",
			ReqID: df.ReqID,
		})

	case "status":
		state := s.displayMgr.GetState(df.Channel)
		_ = c.SendJSON(DisplayResponse{
			Type:  "display-resp",
			Code:  "STATUS",
			ReqID: df.ReqID,
			State: state,
		})

	case "list":
		screens := s.displayMgr.ListScreens(df.Channel)
		_ = c.SendJSON(DisplayResponse{
			Type:    "display-resp",
			Code:    "SCREENS",
			ReqID:   df.ReqID,
			Screens: screens,
		})

	default:
		_ = c.SendJSON(dcResp{
			Type:  "err",
			Msg:   fmt.Sprintf("unknown display action %q", df.Action),
			ReqID: df.ReqID,
		})
	}
}

// DisplayManager returns the PeerJSService's DisplayManager instance.
func (s *PeerJSService) DisplayManager() *DisplayManager {
	return s.displayMgr
}
