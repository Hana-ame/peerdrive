package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	peerjs "github.com/Hana-ame/go-peerjs"
	"peerdrive/internal/config"
	"peerdrive/internal/transport"
)

// display_test.go — Display controller HTTP endpoints test suite (Issue #243).

type fakeDisplaySession struct {
	id string
}

func (f *fakeDisplaySession) ID() string                              { return f.id }
func (f *fakeDisplaySession) SendJSON(v any) error                    { return nil }
func (f *fakeDisplaySession) SendFrame(header any, body []byte) error { return nil }
func (f *fakeDisplaySession) OnMessage(fn func(peerjs.Frame))         {}
func (f *fakeDisplaySession) Close() error                            { return nil }
func (f *fakeDisplaySession) Closed() bool                            { return false }

func setupDisplayTest(t *testing.T) (*gin.Engine, *transport.PeerJSService) {
	gin.SetMode(gin.TestMode)
	cfg := config.DefaultConfig()
	cfg.PeerJSEnable = false
	svc := transport.NewPeerJSService(cfg, t.TempDir())
	InitDisplayController(svc)

	r := gin.New()
	grp := r.Group("/display")
	{
		grp.GET("/screens", ListDisplayScreens)
		grp.GET("/status", GetDisplayStatus)
		grp.POST("/cast", CastDisplay)
		grp.POST("/control", ControlDisplay)
		grp.POST("/clear", ClearDisplay)
	}
	return r, svc
}

// TestDisplayController_Endpoints verifies listing screens, querying status, and casting via HTTP.
// 发现背景：Issue #243（展示大屏与播控 HTTP/admin 面端点）。
func TestDisplayController_Endpoints(t *testing.T) {
	r, svc := setupDisplayTest(t)

	// 1. Initial screen list is empty
	req := httptest.NewRequest(http.MethodGet, "/display/screens", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var screens []*transport.DisplayScreen
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &screens))
	assert.Empty(t, screens)

	// 2. Register a screen on the transport layer
	fakeSess := &fakeDisplaySession{id: "mock-tv"}
	svc.DisplayManager().Register(fakeSess, "mock-tv", "living-room", "Main TV")

	// List again
	req = httptest.NewRequest(http.MethodGet, "/display/screens?channel=living-room", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	screens = nil
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &screens))
	require.Len(t, screens, 1)
	assert.Equal(t, "mock-tv", screens[0].ID)
	assert.Equal(t, "living-room", screens[0].Channel)

	// 3. Cast to non-registered session -> 400 Bad Request
	body, _ := json.Marshal(map[string]any{
		"targetSessionId": "ghost-screen",
		"mediaType":       "image",
		"hash":            "h1",
	})
	req = httptest.NewRequest(http.MethodPost, "/display/cast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "not in display mode")

	// 4. Cast to registered channel -> 200 OK
	body, _ = json.Marshal(map[string]any{
		"channel":   "living-room",
		"mediaType": "video",
		"hash":      "h-video",
		"title":     "clip.mp4",
		"autoplay":  true,
	})
	req = httptest.NewRequest(http.MethodPost, "/display/cast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 5. Query status
	req = httptest.NewRequest(http.MethodGet, "/display/status?channel=living-room", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	var st transport.DisplayState
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &st))
	assert.Equal(t, "video", st.MediaType)
	assert.Equal(t, "clip.mp4", st.Title)
	assert.True(t, st.Playing)

	// 6. Control (pause)
	ctrlBody, _ := json.Marshal(map[string]any{
		"channel":       "living-room",
		"controlAction": "pause",
	})
	req = httptest.NewRequest(http.MethodPost, "/display/control", bytes.NewReader(ctrlBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// 7. Clear
	clearBody, _ := json.Marshal(map[string]any{
		"channel": "living-room",
	})
	req = httptest.NewRequest(http.MethodPost, "/display/clear", bytes.NewReader(clearBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
