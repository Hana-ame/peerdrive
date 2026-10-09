package echproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGetVideoMeta pins the page-facing metadata contract: the same two API
// calls as ResolveDownloadURL, but returning every resolution (not just
// "Source") with an https:-prefixed CDN URL, plus the title/author/cover/
// duration the page renders.
//
// Discovery background: ResolveDownloadURL discards everything except the
// single best download URL, which is enough for a CLI downloader but leaves a
// page with no title, no cover and only one resolution to offer. This test is
// the executable statement of the page's data contract.
func TestGetVideoMeta(t *testing.T) {
	m := newMockIwaraServer(t)
	m.author = "uploader"
	m.authorID = "u123"
	m.cover = "https://img.iwara.tv/thumb/mock.jpg"
	m.duration = 123.5
	m.views = 4567

	cfg := NewModuleConfig()
	cfg.IWARACookie = m.expectedCookie
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	meta, err := client.GetVideoMeta(context.Background(), m.videoID)
	if err != nil {
		t.Fatalf("GetVideoMeta: %v", err)
	}
	if meta.ID != m.videoID {
		t.Errorf("id = %q, want %q", meta.ID, m.videoID)
	}
	if meta.Title != "Mock Video" {
		t.Errorf("title = %q, want %q", meta.Title, "Mock Video")
	}
	if meta.Author != m.author || meta.AuthorID != m.authorID {
		t.Errorf("author = %q/%q, want %q/%q", meta.Author, meta.AuthorID, m.author, m.authorID)
	}
	if meta.Cover != m.cover {
		t.Errorf("cover = %q, want %q", meta.Cover, m.cover)
	}
	if meta.Duration != 123 {
		t.Errorf("duration = %d, want 123", meta.Duration)
	}
	if meta.Views != 4567 {
		t.Errorf("views = %d, want 4567", meta.Views)
	}
	if meta.File == nil || meta.File.ID != m.fileID {
		t.Errorf("file = %+v, want file id %q", meta.File, m.fileID)
	}
	if len(meta.Resolutions) != 2 {
		t.Fatalf("resolutions = %d entries, want 2 (the page needs every option)", len(meta.Resolutions))
	}
	// Every resolution carries a full URL, not the protocol-relative src the
	// API returns raw.
	for _, r := range meta.Resolutions {
		if !strings.HasPrefix(r.DownloadURL, "https://") {
			t.Errorf("resolution %q downloadUrl = %q, want https:// prefix", r.Name, r.DownloadURL)
		}
		if r.Name == "" {
			t.Error("a resolution with an empty name would render as an empty option")
		}
	}
	if !m.sawCookie.Load() {
		t.Error("cookie was never observed on the metadata requests")
	}
	if !m.sawXVersion.Load() {
		t.Error("X-Version header was never observed on the resolution request")
	}
}

// TestGetVideoMetaAcceptsURL form is what the frontend pastes into the input
// box: https://www.iwara.tv/videos/{id} must resolve to the same payload as
// the bare ID, or the page would show different data depending on how the user
// typed it.
func TestGetVideoMetaAcceptsURL(t *testing.T) {
	m := newMockIwaraServer(t)
	cfg := NewModuleConfig()
	cfg.IWARACookie = m.expectedCookie
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	byID, err := client.GetVideoMeta(context.Background(), m.videoID)
	if err != nil {
		t.Fatalf("GetVideoMeta(bare id): %v", err)
	}
	byURL, err := client.GetVideoMeta(context.Background(), "https://www.iwara.tv/videos/"+m.videoID)
	if err != nil {
		t.Fatalf("GetVideoMeta(url): %v", err)
	}
	if byID.Title != byURL.Title || len(byID.Resolutions) != len(byURL.Resolutions) {
		t.Errorf("bare id and URL forms disagree: %q/%d vs %q/%d",
			byID.Title, len(byID.Resolutions), byURL.Title, len(byURL.Resolutions))
	}
}

// TestGetVideoMetaMissingMetadataFields covers a payload that has no author /
// cover / duration at all (private or legacy videos). The decoder must leave
// them empty rather than error, so the page can render placeholders.
func TestGetVideoMetaMissingMetadataFields(t *testing.T) {
	m := newMockIwaraServer(t) // no author/cover/duration injected
	cfg := NewModuleConfig()
	cfg.IWARACookie = m.expectedCookie
	client := NewIwaraClient(&cfg)
	client.client = m.Server.Client()
	client.baseURL = m.Server.URL

	meta, err := client.GetVideoMeta(context.Background(), m.videoID)
	if err != nil {
		t.Fatalf("GetVideoMeta: %v", err)
	}
	if meta.Author != "" || meta.Cover != "" || meta.Duration != 0 || meta.Views != 0 {
		t.Errorf("expected zero-valued metadata, got %+v", meta)
	}
	if meta.Title != "Mock Video" {
		t.Errorf("title should still come through: %q", meta.Title)
	}
}

// TestGetVideoMetaValidation covers the input guard: an empty ID must fail
// before any network call (the page passes whatever the user typed).
func TestGetVideoMetaValidation(t *testing.T) {
	client := NewIwaraClient(nil)
	if _, err := client.GetVideoMeta(context.Background(), "   "); err == nil {
		t.Fatal("GetVideoMeta(whitespace) should return an error")
	}
	if _, err := client.GetVideoMeta(context.Background(), "https://example.com/not-iyara"); err == nil {
		t.Fatal("GetVideoMeta(non-iwara URL) should return an error")
	}
}

// TestGetVideoMetaAPIError pins the status mapping: a 404 from the iwara API
// (video removed / requires login) must surface as an error, not a half-empty
// payload the page would render as a real video.
func TestGetVideoMetaAPIError(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	client := NewIwaraClient(nil)
	client.client = srv.Client()
	client.baseURL = srv.URL

	if _, err := client.GetVideoMeta(context.Background(), "gone123"); err == nil {
		t.Fatal("GetVideoMeta should return an error on HTTP 404")
	}
}

// TestSchemePrefix pins the CDN URL normalization: the resolution API returns
// protocol-relative "//host/path" values and the page hands the URL to a
// browser as an <a href>, which needs a full scheme.
func TestSchemePrefix(t *testing.T) {
	cases := map[string]string{
		"//v-f007.v.ihstatic.com/a.mp4": "https://v-f007.v.ihstatic.com/a.mp4",
		"https://cdn.example.com/a.mp4": "https://cdn.example.com/a.mp4",
		"":                              "",
	}
	for in, want := range cases {
		if got := schemePrefix(in); got != want {
			t.Errorf("schemePrefix(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestApplyMetaCandidatesTypes checks the type tolerance: JSON numbers arrive
// as float64, but a legacy payload may send the duration as a string ("123").
// The decoder must accept both so an API change cannot blank the page.
func TestApplyMetaCandidatesTypes(t *testing.T) {
	meta := &VideoMeta{}
	applyMetaCandidates(meta, map[string]any{
		"title":    "T",
		"duration": float64(90),
		"views":    float64(12),
	})
	if meta.Duration != 90 || meta.Views != 12 {
		t.Errorf("float64 metadata: duration=%d views=%d, want 90/12", meta.Duration, meta.Views)
	}

	meta = &VideoMeta{}
	applyMetaCandidates(meta, map[string]any{
		"time":     "2:30", // a human-readable duration: must not panic or mis-parse
		"uploaded": "1700000000",
	})
	if meta.Uploaded != 1700000000 {
		t.Errorf("string timestamp: uploaded=%d, want 1700000000", meta.Uploaded)
	}

	// nil payload (e.g. a non-object response) must be a no-op, not a panic.
	applyMetaCandidates(meta, nil)
}

// TestApplyMetaCandidatesCandidateKeys verifies the multi-spelling lookup
// actually falls through to the next candidate instead of only honouring the
// first key (the field names here are inferred, not verified — see meta.go).
func TestApplyMetaCandidatesCandidateKeys(t *testing.T) {
	meta := &VideoMeta{}
	applyMetaCandidates(meta, map[string]any{
		"uploader":     "alt-author",
		"thumbnailUrl": "https://img/alt.jpg",
		"length":       float64(5),
		"viewCount":    float64(9),
	})
	if meta.Author != "alt-author" {
		t.Errorf("author candidate fallback = %q, want alt-author", meta.Author)
	}
	if meta.Cover != "https://img/alt.jpg" {
		t.Errorf("cover candidate fallback = %q", meta.Cover)
	}
	if meta.Duration != 5 || meta.Views != 9 {
		t.Errorf("duration/views fallback = %d/%d, want 5/9", meta.Duration, meta.Views)
	}
}
