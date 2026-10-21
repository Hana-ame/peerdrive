package controller

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/echproxy"
)

// mockIwaraAPI stands in for api.iwara.tv: /video/{id} returns the video
// payload (including a fileUrl that points back at the mock), and the
// file-resolution endpoint returns two resolution entries with the X-Version
// header validated. The tests here assert the HTTP contract the iwara page
// consumes, not the client's signing — that is covered by echproxy's own tests.
func mockIwaraAPI(t *testing.T, id string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	var fileURL string // set once the server's base URL is known
	mux.HandleFunc("/video/"+id, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":       id,
			"title":    "Test Video",
			"author":   "uploader",
			"coverUrl": "https://img.example.com/cover.jpg",
			"duration": float64(65),
			"file":     map[string]string{"id": "f1", "name": "v.mp4"},
			"fileUrl":  fileURL,
		})
	})
	mux.HandleFunc("/resolve", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Version") == "" {
			http.Error(w, "missing X-Version", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]map[string]any{
			{"id": "sd", "name": "480p", "src": map[string]string{"download": "//cdn.example.com/480.mp4"}},
			{"id": "hi", "name": "Source", "src": map[string]string{"download": "//cdn.example.com/src.mp4"}},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	// The payload must carry a full URL (scheme included): the client parses
	// fileUrl and reads its "expires" query parameter.
	fileURL = srv.URL + "/resolve?expires=1700000000"
	return srv
}

// ginTest invokes a handler directly. Params are normally filled in by the
// router, so the test supplies them — calling the handler without routing means
// c.Param("id") would otherwise always be empty.
func ginTest(t *testing.T, r gin.HandlerFunc, method, target, idParam string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	if idParam != "" {
		c.Params = gin.Params{{Key: "id", Value: idParam}}
	}
	r(c)
	return w
}

// TestGetIwaraVideoOK is the contract the iwara page depends on: one GET
// returns the metadata object plus every resolution's https download URL.
//
// Discovery background: the frontend renders cover/author/duration from this
// exact shape, so the field names and the http:// → https:// prefix are part of
// the contract, not an implementation detail.
func TestGetIwaraVideoOK(t *testing.T) {
	srv := mockIwaraAPI(t, "test123")
	// The mock is plain HTTP; the client's transport verifies TLS only for the
	// ech-proxy entry host, so pointing baseURL at a plain http URL works.
	client := echproxy.NewIwaraClient(nil)
	// The mock is plain HTTP; the client's real transport only skips TLS
	// verification for the ech-proxy entry host, so srv.Client() (system roots,
	// no host rewriting) works for a 127.0.0.1 httptest URL.
	client.SetHTTPClient(srv.Client())
	client.SetBaseURL(srv.URL)
	orig := iwaraClient
	iwaraClient = client
	t.Cleanup(func() { iwaraClient = orig })

	w := ginTest(t, GetIwaraVideo, http.MethodGet, "/iwara/video/test123", "test123")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var meta echproxy.VideoMeta
	if err := json.Unmarshal(w.Body.Bytes(), &meta); err != nil {
		t.Fatalf("body is not the metadata JSON: %v", err)
	}
	if meta.Title != "Test Video" || meta.Author != "uploader" || meta.Duration != 65 {
		t.Errorf("meta = %+v, want title/author/duration from the payload", meta)
	}
	if meta.Cover != "https://img.example.com/cover.jpg" {
		t.Errorf("cover = %q", meta.Cover)
	}
	if len(meta.Resolutions) != 2 {
		t.Fatalf("resolutions = %d, want 2", len(meta.Resolutions))
	}
	for _, r := range meta.Resolutions {
		if !strings.HasPrefix(r.DownloadURL, "https://") {
			t.Errorf("resolution %q downloadUrl = %q, want https:// prefix", r.Name, r.DownloadURL)
		}
	}
}

// TestGetIwaraVideoAcceptsPastedURL: the page passes the raw pasted URL, so the
// route must normalize https://www.iwara.tv/videos/{id} → {id}.
func TestGetIwaraVideoAcceptsPastedURL(t *testing.T) {
	srv := mockIwaraAPI(t, "abc123")
	client := echproxy.NewIwaraClient(nil)
	// The mock is plain HTTP; the client's real transport only skips TLS
	// verification for the ech-proxy entry host, so srv.Client() (system roots,
	// no host rewriting) works for a 127.0.0.1 httptest URL.
	client.SetHTTPClient(srv.Client())
	client.SetBaseURL(srv.URL)
	orig := iwaraClient
	iwaraClient = client
	t.Cleanup(func() { iwaraClient = orig })

	// The pasted URL is passed as the :id param value verbatim; ParseVideoID
	// inside the client turns it back into the bare video ID.
	w := ginTest(t, GetIwaraVideo, http.MethodGet, "/iwara/video/abc123",
		"https://www.iwara.tv/videos/abc123")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	body, _ := io.ReadAll(w.Body)
	if !strings.Contains(string(body), "Test Video") {
		t.Errorf("body = %s, want the resolved title", string(body))
	}
}

// TestGetIwaraVideoModuleDisabled: with no client injected the handler answers
// 503 (the route is normally not registered at all, but a future change that
// registers it unconditionally must not panic on a nil client).
func TestGetIwaraVideoModuleDisabled(t *testing.T) {
	orig := iwaraClient
	iwaraClient = nil
	t.Cleanup(func() { iwaraClient = orig })

	w := ginTest(t, GetIwaraVideo, http.MethodGet, "/iwara/video/test123", "test123")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

// TestGetIwaraVideoAPIError: a 404 from iwara (removed video / login required)
// maps to 404 with an error message the page can display.
func TestGetIwaraVideoAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	client := echproxy.NewIwaraClient(nil)
	// The mock is plain HTTP; the client's real transport only skips TLS
	// verification for the ech-proxy entry host, so srv.Client() (system roots,
	// no host rewriting) works for a 127.0.0.1 httptest URL.
	client.SetHTTPClient(srv.Client())
	client.SetBaseURL(srv.URL)
	orig := iwaraClient
	iwaraClient = client
	t.Cleanup(func() { iwaraClient = orig })

	w := ginTest(t, GetIwaraVideo, http.MethodGet, "/iwara/video/gone999", "gone999")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "error") {
		t.Errorf("body = %s, want an error field for the page's error box", body)
	}
}
