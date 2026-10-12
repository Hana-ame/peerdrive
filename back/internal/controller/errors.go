package controller

// Shared error-response helpers for the HTTP layer.
//
// Why this file exists (red-team R2, information disclosure): 62 controller
// call sites used to answer http.StatusInternalServerError with gin.H{"error":
// err.Error()}. err.Error() on this codebase is dominated by SQLite messages
// ("UNIQUE constraint failed: collections.collection_name", "database is locked",
// "no such table: file_index", "attempt to write a readonly database"), which
// hand an unauthenticated caller the schema, the constraint names and the
// current lock state of the node. A caller who probes enough endpoints can
// reconstruct the data model and distinguish "does not exist" from "locked".
//
// The rule this file enforces: 5xx never carries the driver's text. The client
// gets a fixed string; the real error goes to the structured log, which already
// has a request id attached by the AccessLog middleware.
//
// 4xx validation errors are deliberately NOT routed through here: those messages
// come from the request body the caller just submitted and are user-facing by
// design. Only the 5xx surface leaks internals.

import (
	"net/http"

	"github.com/gin-gonic/gin"

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
