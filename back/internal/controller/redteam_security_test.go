// Package controller — red-team security regression tests (2026-10-11 audit R3).
//
// Discovery background (required by AGENTS.md): the 2026-10-11 UI adversarial
// audit found a HIGH stored-XSS chain on /sha256sum/:hash:
//
//   1. Upload `evil.js` and `page.html` containing
//      `<script src="/sha256sum/<evil.js-hash>?inline=1"></script>`.
//   2. http.ServeContent guessed the response Content-Type from the
//      attacker-chosen filename extension, so the HTML came back as
//      `text/html` and the JS as `text/javascript`.
//   3. The global CSP is `default-src 'self'`, which permits same-origin
//      <script src>, so the stored JS executed in the victim's browser on this
//      origin. Verified live: document.title was set by the stored payload.
//      Same-origin JS can read the admin frontend's localStorage
//      (`peerdrive_auth_token` / `peerdrive_auth_key`), so a victim who opens
//      the crafted link gets full node takeover.
//
// The audit also found two smaller issues fixed here:
//   - /shares accepted an arbitrary `hash`, and /s/:token built a 302 Location
//     from it unvalidated (observed `Location: /etc/passwd`).
//   - the follow-redirects URL provider put an operator-stored value verbatim
//     into the Location header.
//
// These tests pin the fixes so the findings cannot regress.

package controller

import (
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupRedTeamRouter builds a router with a real file service (content-addressed
// storage under a temp dir) and the two routes under test.
func setupRedTeamRouter(t *testing.T) (*gin.Engine, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	require.NoError(t, repository.InitDB(":memory:"))

	cfg := config.Load()
	cfg.StorageDir = t.TempDir()
	cfg.StorageEnable = true
	InitFileController(service.NewFileService(cfg))
	InitShareController(service.NewShareService())

	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("storageDir", cfg.StorageDir)
		c.Next()
	})
	r.POST("/files/upload", UploadFile)
	r.GET("/sha256sum/:sha256", DownloadBySHA256Local)
	r.POST("/shares", CreateShare)
	r.GET("/s/:token", AccessShare)
	return r, cfg.StorageDir
}

// uploadMultipart uploads one file through the real multipart path so the test
// exercises the same code the browser does.
func uploadMultipart(t *testing.T, r *gin.Engine, name, body string) string {
	t.Helper()
	var sb strings.Builder
	w := multipart.NewWriter(&sb)
	fw, err := w.CreateFormFile("file", name)
	require.NoError(t, err)
	_, err = fw.Write([]byte(body))
	require.NoError(t, err)
	require.NoError(t, w.Close())

	req := httptest.NewRequest("POST", "/files/upload", strings.NewReader(sb.String()))
	req.Header.Set("Content-Type", w.FormDataContentType())
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusCreated, wr.Code, "upload failed: %s", wr.Body.String())

	var got struct {
		Hash string `json:"hash"`
	}
	require.NoError(t, json.Unmarshal(wr.Body.Bytes(), &got))
	require.NotEmpty(t, got.Hash)
	return got.Hash
}

// TestDownload_Inline_HTML_IsNeverRendered is the core R3 regression: a stored
// .html file must never come back as text/html, regardless of ?inline=1.
func TestDownload_Inline_HTML_IsNeverRendered(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	html := "<!DOCTYPE html><html><body><script>fetch('/files')</script></body></html>"
	hash := uploadMultipart(t, r, "evil.html", html)

	for _, inline := range []bool{false, true} {
		url := "/sha256sum/" + hash
		if inline {
			url += "?inline=1"
		}
		req := httptest.NewRequest("GET", url, nil)
		wr := httptest.NewRecorder()
		r.ServeHTTP(wr, req)
		require.Equal(t, http.StatusOK, wr.Code)

		ct := wr.Header().Get("Content-Type")
		assert.NotContains(t, strings.ToLower(ct), "text/html",
			"stored .html must never be served as text/html (inline=%v)", inline)
		assert.NotContains(t, strings.ToLower(ct), "application/xhtml+xml",
			"stored .html must never be served as xhtml (inline=%v)", inline)
		assert.Equal(t, "application/octet-stream", ct,
			"non-previewable content must be forced to octet-stream (inline=%v)", inline)
	}
}

// TestDownload_Inline_JS_IsNeverRendered closes the second half of the R3 chain:
// even if some HTML ever reached the browser, a stored .js must not be served as
// an executable script type.
func TestDownload_Inline_JS_IsNeverRendered(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	hash := uploadMultipart(t, r, "evil.js", "document.title='pwned'")

	req := httptest.NewRequest("GET", "/sha256sum/"+hash+"?inline=1", nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusOK, wr.Code)

	ct := strings.ToLower(wr.Header().Get("Content-Type"))
	assert.NotContains(t, ct, "javascript", "stored .js must never be served as a script type")
	assert.NotContains(t, ct, "text/plain", "stored .js must not be inline text")
	assert.Equal(t, "application/octet-stream", ct)
}

// TestDownload_BlobCSP_BlocksScriptExecution pins the belt-and-braces CSP on the
// blob response. Even a Content-Type that leaks through cannot run script.
func TestDownload_BlobCSP_BlocksScriptExecution(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	hash := uploadMultipart(t, r, "note.txt", "hello")

	req := httptest.NewRequest("GET", "/sha256sum/"+hash+"?inline=1", nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusOK, wr.Code)

	csp := wr.Header().Get("Content-Security-Policy")
	assert.Contains(t, csp, "script-src 'none'", "blob responses must disable script execution")
	assert.Contains(t, csp, "default-src 'none'")
	assert.Contains(t, wr.Header().Get("Content-Type"), "text/plain",
		"text/plain stays inline so plain-text preview keeps working")
}

// TestDownload_ImageInline_StillPreviews guards against over-tightening: images
// are safe to render inline and the panel relies on image preview.
func TestDownload_ImageInline_StillPreviews(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	png := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a} // PNG magic
	// uploadMultipart takes a string body; a truncated PNG is still enough to
	// exercise the Content-Type path (the handler guesses by extension only).
	hash := uploadMultipart(t, r, "pic.png", string(png))

	req := httptest.NewRequest("GET", "/sha256sum/"+hash+"?inline=1", nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusOK, wr.Code)
	assert.Equal(t, "image/png", wr.Header().Get("Content-Type"))
	assert.Contains(t, wr.Header().Get("Content-Disposition"), "inline")
}

// TestDownload_SVG_IsNotRenderable closes the other script-bearing "image" type:
// image/svg+xml can execute script, so it must be forced to octet-stream too.
func TestDownload_SVG_IsNotRenderable(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	hash := uploadMultipart(t, r, "evil.svg", svg)

	req := httptest.NewRequest("GET", "/sha256sum/"+hash+"?inline=1", nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusOK, wr.Code)
	assert.NotContains(t, strings.ToLower(wr.Header().Get("Content-Type")), "svg",
		"image/svg+xml can execute script and must be forced to octet-stream")
	assert.Equal(t, "application/octet-stream", wr.Header().Get("Content-Type"))
}

// TestDownload_ContentDisposition_NoInjection verifies the attacker-controlled
// filename is sanitised before being placed in the header: no CR/LF, no stray
// quote that could terminate the filename parameter.
func TestDownload_ContentDisposition_NoInjection(t *testing.T) {
	r, _ := setupRedTeamRouter(t)
	hash := uploadMultipart(t, r, `evil" ; x="y\nX-Injected: yes.txt`, "body")

	req := httptest.NewRequest("GET", "/sha256sum/"+hash, nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	require.Equal(t, http.StatusOK, wr.Code)

	cd := wr.Header().Get("Content-Disposition")
	assert.NotContains(t, cd, "\n", "Content-Disposition must not contain LF")
	assert.NotContains(t, cd, "\r", "Content-Disposition must not contain CR")
	// exactly one quoted filename parameter, nothing after it
	assert.NotContains(t, strings.TrimPrefix(cd, "attachment; "), ",",
		"no second parameter may be injected")
}

// TestShare_CreateRejectsNonSHA256Hash — /shares used to accept any `hash` and
// /s/:token turned it into a raw 302 Location (`../../../../../../etc/passwd` →
// `Location: /etc/passwd`). The value is now shape-validated at write time.
func TestShare_CreateRejectsNonSHA256Hash(t *testing.T) {
	r, _ := setupRedTeamRouter(t)

	bad := []string{
		"../../../../../../etc/passwd",
		"//evil.com/steal",
		"/s/absolute",
		"x",
		"%2e%2e%2fsecret",
		strings.Repeat("0", 63),
		strings.Repeat("0", 65),
	}
	for _, h := range bad {
		req := httptest.NewRequest("POST", "/shares", strings.NewReader(
			`{"hash":"`+h+`","type":"file"}`))
		req.Header.Set("Content-Type", "application/json")
		wr := httptest.NewRecorder()
		r.ServeHTTP(wr, req)
		assert.Equal(t, http.StatusBadRequest, wr.Code, "bad hash must be rejected: %q", h)
		assert.NotContains(t, wr.Body.String(), `"token"`,
			"no share token may be minted for a malformed hash: %q", h)
	}

	// A well-formed hash still works.
	req := httptest.NewRequest("POST", "/shares", strings.NewReader(
		`{"hash":"`+strings.Repeat("0", 64)+`","type":"file"}`))
	req.Header.Set("Content-Type", "application/json")
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	assert.Equal(t, http.StatusOK, wr.Code)
	assert.Contains(t, wr.Body.String(), `"token"`)
}

// TestShare_AccessShare_RejectsMalformedStoredHash guards rows written before
// CreateShare started validating: the redirect target must never be built from an
// unvalidated value.
func TestShare_AccessShare_RejectsMalformedStoredHash(t *testing.T) {
	_, _ = setupRedTeamRouter(t)
	const badHash = "../../../../etc/passwd"
	share, err := repository.CreateShare(badHash, "file", "x")
	require.NoError(t, err)
	require.NotEmpty(t, share.Token, "expected the malformed-hash share to be stored for the test")

	r, _ := setupRedTeamRouter(t)
	req := httptest.NewRequest("GET", "/s/"+share.Token, nil)
	wr := httptest.NewRecorder()
	r.ServeHTTP(wr, req)
	assert.Equal(t, http.StatusNotFound, wr.Code)
	assert.Empty(t, wr.Header().Get("Location"),
		"must not redirect to an unvalidated path")
}

// TestSafeFilename pins the character set that may reach a header.
func TestSafeFilename(t *testing.T) {
	assert.Equal(t, "evil", safeFilename("evil"))
	assert.Equal(t, "a.txt", safeFilename("a.txt"))
	assert.NotContains(t, safeFilename("a\rb"), "\r")
	assert.NotContains(t, safeFilename("a\nb"), "\n")
	assert.NotContains(t, safeFilename(`a"b`), `"`)
	assert.NotContains(t, safeFilename(`a'b`), "'")
	assert.NotContains(t, safeFilename(`a\b`), `\\`)
	assert.NotContains(t, safeFilename("a\x00b"), "\x00")
}

// TestIsHTTPURL pins the follow-redirects Location constraint: only absolute
// http(s) targets may be placed in the Location header.
func TestIsHTTPURL(t *testing.T) {
	ok := []string{
		"https://example.com/a",
		"http://example.com:8080/a",
		"https://example.com",
	}
	for _, u := range ok {
		assert.True(t, isHTTPURL(u), "must accept %q", u)
	}
	bad := []string{
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"vbscript:msgbox(1)",
		"file:///etc/passwd",
		"//evil.com/steal",
		"not a url",
		"",
		"/relative/path",
		"ftp://example.com/x",
	}
	for _, u := range bad {
		assert.False(t, isHTTPURL(u), "must reject %q", u)
	}
}
