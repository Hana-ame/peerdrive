package controller

// Shared error-response helpers for the HTTP layer.
//
// Why this file exists (red-team R2 & Issue #395, information disclosure):
// Controller call sites used to answer 5xx and 4xx/502 with raw err.Error().
// err.Error() on this codebase can carry SQLite messages ("UNIQUE constraint
// failed: collections.collection_name", "database is locked", "no such table:
// file_index") or OS paths ("open /home/.../peerdrive.db"), which hand an
// unauthenticated caller the schema, constraint names, and filesystem layout.
//
// Rules enforced here:
//   1. 5xx (writeInternalError) NEVER carries the driver's text — always replies
//      {"error": "internal error"} and logs the real error.
//   2. 4xx / 502 (writeClientError / sanitizeErr) scrubs any OS path, SQLite
//      driver string, or raw TCP socket error via apperr.Sanitize while
//      preserving intentional domain validation messages.

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/apperr"
	"peerdrive/internal/log"
)

// writeInternalError replies with a fixed generic 500 and logs the real error.
//
// It takes c so the logged line carries the route pattern; c.FullPath() is the
// gin pattern ("/collections/:name"), not the attacker-supplied path, so it is
// safe to log even when the path itself is untrusted.
func writeInternalError(c *gin.Context, err error) {
	if err == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	log.LogError("ctrl: %s %s failed: %v", c.Request.Method, c.FullPath(), err)
	c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
}

// sanitizeErr returns a client-safe error string (Issue #395), scrubbing SQL/OS/path
// internals while keeping safe validation messages.
func sanitizeErr(err error, fallback string) string {
	return apperr.Sanitize(err, fallback)
}

// writeClientError logs the underlying error at warn level and responds with a
// sanitized error message for 4xx/502 responses (Issue #395).
func writeClientError(c *gin.Context, status int, err error, fallback string) {
	if err != nil {
		log.LogWarn("ctrl: %s %s status=%d: %v", c.Request.Method, c.FullPath(), status, err)
	}
	c.JSON(status, gin.H{"error": sanitizeErr(err, fallback)})
}

// writeAppError responds using a typed AppError (Issue #435), preserving the JSON "error" key.
func writeAppError(c *gin.Context, appErr *apperr.AppError) {
	if appErr == nil {
		writeInternalError(c, nil)
		return
	}
	status := appErr.HTTPStatus
	if status == 0 {
		status = http.StatusInternalServerError
	}
	c.JSON(status, appErr)
}

