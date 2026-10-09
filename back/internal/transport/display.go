package transport

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"peerdrive/internal/log"
)

// display.go — Remote screen display control and public screen sync (Issue #243).
//
// Background:
// Remote control capability extends beyond administrative node management to screen casting
// and synchronization:
// 1. A session can open a public / controlled screen page (/display).
// 2. Control sources (Drive UI, remote node, CLI) can remotely command what appears on a specific screen
//    or channel (images, videos, audio, text announcements).
// 3. Security invariant (user rule):
//    "需要直接控制某个session屏幕上出现什么。该session也需要打开一个受控界面才可以"
//    If the target session is NOT actively registered as a display receiver, casting to it is rejected.
// 4. Remote peer commands require authentication (RemoteControlToken / Authorizer).

// DisplayScreen represents a registered display session.
type DisplayScreen struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"sessionId"`
	Name         string    `json:"name"`
	Channel      string    `json:"channel"`
	RegisteredAt time.Time `json:"registeredAt"`
	LastSeen     time.Time `json:"lastSeen"`
	sess         Session
}

// DisplayState represents the current playback / screen state for a channel.
type DisplayState struct {
	Channel      string    `json:"channel"`
	MediaType    string    `json:"mediaType"` // "image", "video", "audio", "text"
	Hash         string    `json:"hash,omitempty"`
	URL          string    `json:"url,omitempty"`
	Title        string    `json:"title,omitempty"`
	MimeType     string    `json:"mimeType,omitempty"`
	Autoplay     bool      `json:"autoplay"`
	Loop         bool      `json:"loop"`
	Playing      bool      `json:"playing"`
	Position     float64   `json:"position"`
	Volume       float64   `json:"volume"`
	UpdatedAt    time.Time `json:"updatedAt"`
	ControlledBy string    `json:"controlledBy,omitempty"`
}

// DisplayFrame wire protocol message for display verbs.
type DisplayFrame struct {
	Type          string   `json:"type"`                     // always "display"
	Action        string   `json:"action"`                   // "register", "unregister", "show", "control", "clear", "status", "list"
	SessionID     string   `json:"sessionId,omitempty"`      // target screen ID or self session ID
	Channel       string   `json:"channel,omitempty"`        // channel name (default "default")
	Name          string   `json:"name,omitempty"`           // friendly screen name
	Token         string   `json:"token,omitempty"`          // auth credential for remote peers
	ReqID         string   `json:"reqId,omitempty"`
	MediaType     string   `json:"mediaType,omitempty"`      // "image", "video", "audio", "text"
	Hash          string   `json:"hash,omitempty"`           // peerdrive file sha256
	URL           string   `json:"url,omitempty"`            // direct stream/file URL
	Title         string   `json:"title,omitempty"`          // media title
	MimeType      string   `json:"mimeType,omitempty"`
	Autoplay      bool     `json:"autoplay,omitempty"`
	Loop          bool     `json:"loop,omitempty"`
	ControlAction string   `json:"controlAction,omitempty"`  // "play", "pause", "seek", "volume", "stop"
	Position      float64  `json:"position,omitempty"`
	Volume        *float64 `json:"volume,omitempty"`
}

// DisplayManager manages registered display screens and active channel states.
type DisplayManager struct {
	mu       sync.RWMutex
	screens  map[string]*DisplayScreen // keyed by screen ID
	channels map[string]*DisplayState  // keyed by channel name
}

// NewDisplayManager creates a new DisplayManager instance.
func NewDisplayManager() *DisplayManager {
	return &DisplayManager{
		screens:  make(map[string]*DisplayScreen),
		channels: make(map[string]*DisplayState),
	}
}

// Register registers a session as a controlled display receiver.
func (m *DisplayManager) Register(sess Session, id, channel, name string) *DisplayScreen {
	m.mu.Lock()
	defer m.mu.Unlock()

	if id == "" {
		id = sess.ID()
	}
	if channel == "" {
		channel = "default"
	}
	if name == "" {
		name = "Screen-" + id
	}

	screen := &DisplayScreen{
		ID:           id,
		SessionID:    sess.ID(),
		Name:         name,
		Channel:      channel,
		RegisteredAt: time.Now(),
		LastSeen:     time.Now(),
		sess:         sess,
	}
	m.screens[id] = screen
	log.LogInfo("display: registered screen %s (channel=%s, name=%s)", id, channel, name)
	return screen
}

// Unregister unregisters any screen associated with the session.
func (m *DisplayManager) Unregister(sess Session) {
	if sess == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	for id, s := range m.screens {
		if s.sess == sess || s.SessionID == sess.ID() {
			delete(m.screens, id)
			log.LogInfo("display: unregistered screen %s", id)
		}
	}
}

// UnregisterByID unregisters a screen by its screen ID.
func (m *DisplayManager) UnregisterByID(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.screens, id)
}

// ListScreens returns active display screens, optionally filtered by channel.
func (m *DisplayManager) ListScreens(channel string) []*DisplayScreen {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var res []*DisplayScreen
	for _, s := range m.screens {
		if channel == "" || s.Channel == channel {
			res = append(res, &DisplayScreen{
				ID:           s.ID,
				SessionID:    s.SessionID,
				Name:         s.Name,
				Channel:      s.Channel,
				RegisteredAt: s.RegisteredAt,
				LastSeen:     s.LastSeen,
			})
		}
	}
	return res
}

// GetState returns the current display state for a channel.
func (m *DisplayManager) GetState(channel string) *DisplayState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if channel == "" {
		channel = "default"
	}
	st, ok := m.channels[channel]
	if !ok {
		return &DisplayState{
			Channel: channel,
			Volume:  1.0,
		}
	}
	copyState := *st
	return &copyState
}

// Cast commands content to be displayed on target screen(s).
// Enforces: target session must be registered in display mode.
func (m *DisplayManager) Cast(from Session, targetID, channel string, cmd DisplayFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	fromID := "local"
	if from != nil {
		fromID = from.ID()
	}

	if targetID != "" {
		screen, ok := m.screens[targetID]
		if !ok {
			return fmt.Errorf("target session %q is not in display mode; controlled screen must be opened first", targetID)
		}

		ch := screen.Channel
		if cmd.Channel != "" {
			ch = cmd.Channel
		}
		st := &DisplayState{
			Channel:      ch,
			MediaType:    cmd.MediaType,
			Hash:         cmd.Hash,
			URL:          cmd.URL,
			Title:        cmd.Title,
			MimeType:     cmd.MimeType,
			Autoplay:     cmd.Autoplay,
			Loop:         cmd.Loop,
			Playing:      cmd.Autoplay,
			Volume:       1.0,
			UpdatedAt:    time.Now(),
			ControlledBy: fromID,
		}
		if cmd.Volume != nil {
			st.Volume = *cmd.Volume
		}
		m.channels[ch] = st

		outFrame := cmd
		outFrame.Type = "display"
		outFrame.Action = "show"
		return screen.sess.SendJSON(outFrame)
	}

	if channel == "" {
		channel = "default"
	}

	var targets []*DisplayScreen
	for _, s := range m.screens {
		if s.Channel == channel {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no active display screens in channel %q", channel)
	}

	st := &DisplayState{
		Channel:      channel,
		MediaType:    cmd.MediaType,
		Hash:         cmd.Hash,
		URL:          cmd.URL,
		Title:        cmd.Title,
		MimeType:     cmd.MimeType,
		Autoplay:     cmd.Autoplay,
		Loop:         cmd.Loop,
		Playing:      cmd.Autoplay,
		Volume:       1.0,
		UpdatedAt:    time.Now(),
		ControlledBy: fromID,
	}
	if cmd.Volume != nil {
		st.Volume = *cmd.Volume
	}
	m.channels[channel] = st

	outFrame := cmd
	outFrame.Type = "display"
	outFrame.Action = "show"

	var lastErr error
	for _, s := range targets {
		if err := s.sess.SendJSON(outFrame); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// Control dispatches playback controls (play/pause/seek/volume/stop).
func (m *DisplayManager) Control(from Session, targetID, channel string, cmd DisplayFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if targetID != "" {
		screen, ok := m.screens[targetID]
		if !ok {
			return fmt.Errorf("target session %q is not in display mode", targetID)
		}
		ch := screen.Channel
		if st, ok := m.channels[ch]; ok {
			m.applyControlToState(st, cmd)
		}
		outFrame := cmd
		outFrame.Type = "display"
		outFrame.Action = "control"
		return screen.sess.SendJSON(outFrame)
	}

	if channel == "" {
		channel = "default"
	}
	var targets []*DisplayScreen
	for _, s := range m.screens {
		if s.Channel == channel {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return fmt.Errorf("no active display screens in channel %q", channel)
	}

	if st, ok := m.channels[channel]; ok {
		m.applyControlToState(st, cmd)
	}

	outFrame := cmd
	outFrame.Type = "display"
	outFrame.Action = "control"

	var lastErr error
	for _, s := range targets {
		if err := s.sess.SendJSON(outFrame); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

func (m *DisplayManager) applyControlToState(st *DisplayState, cmd DisplayFrame) {
	switch cmd.ControlAction {
	case "play":
		st.Playing = true
	case "pause", "stop":
		st.Playing = false
	case "seek":
		st.Position = cmd.Position
	case "volume":
		if cmd.Volume != nil {
			st.Volume = *cmd.Volume
		}
	}
	st.UpdatedAt = time.Now()
}

// Clear clears the display on target screen(s) or channel.
func (m *DisplayManager) Clear(from Session, targetID, channel string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if targetID != "" {
		screen, ok := m.screens[targetID]
		if !ok {
			return fmt.Errorf("target session %q is not in display mode", targetID)
		}
		ch := screen.Channel
		delete(m.channels, ch)
		outFrame := DisplayFrame{Type: "display", Action: "clear"}
		return screen.sess.SendJSON(outFrame)
	}

	if channel == "" {
		channel = "default"
	}
	delete(m.channels, channel)

	var targets []*DisplayScreen
	for _, s := range m.screens {
		if s.Channel == channel {
			targets = append(targets, s)
		}
	}
	if len(targets) == 0 {
		return nil
	}

	outFrame := DisplayFrame{Type: "display", Action: "clear"}
	var lastErr error
	for _, s := range targets {
		if err := s.sess.SendJSON(outFrame); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// DisplayResponse returned by wire frames or endpoints.
type DisplayResponse struct {
	Type    string          `json:"type"`
	Code    string          `json:"code,omitempty"`
	Msg     string          `json:"msg,omitempty"`
	ReqID   string          `json:"reqId,omitempty"`
	Screens []*DisplayScreen `json:"screens,omitempty"`
	State   *DisplayState   `json:"state,omitempty"`
}

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
				Type:          "display",
				Action:        "show",
				Channel:       currState.Channel,
				MediaType:     currState.MediaType,
				Hash:          currState.Hash,
				URL:           currState.URL,
				Title:         currState.Title,
				MimeType:      currState.MimeType,
				Autoplay:      currState.Autoplay,
				Loop:          currState.Loop,
				Position:      currState.Position,
				Volume:        &currState.Volume,
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
