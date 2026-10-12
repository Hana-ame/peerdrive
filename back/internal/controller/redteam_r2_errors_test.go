package controller

// Package controller — red-team R2 regression: the 5xx information-disclosure
// fix.
//
// Discovery background (required by AGENTS.md): the round-2 full-stack audit
// found 75 controller call sites answering http.StatusInternalServerError with
// the driver's own error text:
//
//	c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
//
// On this codebase err.Error() is dominated by SQLite messages —
// "UNIQUE constraint failed: collections.collection_name", "database is
// locked", "no such table: file_index", "attempt to write a readonly
// database" — so an unauthenticated caller could reconstruct the schema,
// name the constraints, and tell "does not exist" apart from "locked".
//
// The fix is writeInternalError (errors.go): a fixed string for the client,
// the real error in the structured log. These tests pin it, including the
// property that must never regress: the driver text never reaches the wire.

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// probeEngine wires a route whose only job is to call writeInternalError with
// the given error, so the test asserts on the real HTTP response rather than on
// an in-memory struct. Going through ServeHTTP also means c.FullPath() resolves
// the way it does in production.
func probeEngine(target error) *gin.Engine {
	gin.SetMode(gin.TestMode)
	e := gin.New()
	e.GET("/probe", func(c *gin.Context) {
		writeInternalError(c, target)
	})
	return e
}

// TestWriteInternalError_NeverLeaksDriverText — the core invariant.
//
// 发现背景：75 处 5xx 直回 err.Error()，把 SQLite 的表名、约束名、锁状态交给
// 任意未认证调用方。这个用例把「驱动原文不上网线」钉死。
func TestWriteInternalError_NeverLeaksDriverText(t *testing.T) {
	t.Parallel()
	// Realistic driver / OS error strings, taken from what this codebase actually
	// returns. None of these may appear in the response body.
	driven := []error{
		errors.New("UNIQUE constraint failed: collections.collection_name"),
		errors.New("database is locked"),
		errors.New("no such table: file_index"),
		errors.New("attempt to write a readonly database"),
		errors.New("open /srv/peerdrive/storage/ab/abcdef: permission denied"),
		errors.New("read tcp 10.0.0.4:51022->203.0.113.7:443: i/o timeout"),
	}
	for _, err := range driven {
		t.Run(err.Error(), func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/probe", nil)
			probeEngine(err).ServeHTTP(w, req)

			require.Equal(t, http.StatusInternalServerError, w.Code)
			require.Equal(t, `{"error":"internal error"}`, w.Body.String(),
				"the body must be the fixed string and nothing else")
			for _, sensitive := range []string{"collections.collection_name", "database is locked",
				"file_index", "readonly", "/srv/peerdrive", "i/o timeout", "UNIQUE"} {
				assert.NotContains(t, w.Body.String(), sensitive,
					"driver text must never reach the client: %q", sensitive)
			}
		})
	}
}

// TestWriteInternalError_NilErrorStill500 — a nil error is still an internal
// error: never a 200, never an empty body.
func TestWriteInternalError_NilErrorStill500(t *testing.T) {
	t.Parallel()
	w := httptest.NewRecorder()
	probeEngine(nil).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, `{"error":"internal error"}`, w.Body.String())
}

// TestWriteInternalError_ResponseIsIdenticalAcrossFailures — a caller cannot
// distinguish which internal failure happened from the response alone. That is
// the point: the oracle must not exist on the wire.
func TestWriteInternalError_ResponseIsIdenticalAcrossFailures(t *testing.T) {
	t.Parallel()
	failures := []error{nil, errors.New("a"), errors.New("no such table: file_index"),
		errors.New("UNIQUE constraint failed: collections.collection_name")}
	var bodies []string
	for _, err := range failures {
		w := httptest.NewRecorder()
		probeEngine(err).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))
		require.Equal(t, http.StatusInternalServerError, w.Code)
		bodies = append(bodies, w.Body.String())
	}
	for _, b := range bodies {
		assert.Equal(t, bodies[0], b, "every 5xx must be byte-identical")
	}
}
