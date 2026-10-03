// node_share.go: management endpoints for this node's sharing scope (doc/NETDISK.md M2.6 / §12.6).
//
// Semantics: answer and modify "what does my node offer externally, and to whom":
//   - GET  /peerjs/share         current sharing scope + optional file list (with shared/level flags)
//   - PUT  /peerjs/share         partial update (enable/dirs/files/collections/friends)
//   - POST /peerjs/share/files   toggle some files {hashes:[], shared:bool, level:string}
//
// Why this set of endpoints: sharing scope used to only be configurable via PEERDRIVE_SHARE_*
// environment variables at startup — changing it required a node restart. But "which files am I
// willing to share, and with whom" is an ad-hoc decision — you upload a file and want to share it
// immediately, or decide you don't want to share a certain directory anymore. Requiring a restart
// forces operators to either long-term share an overbroad directory, or not enable sharing at all
// (see service/nodeshare.go file header).
//
// Three levels (model.Level*): public = listed and downloadable / unlisted = not listed but
// downloadable / private = only self and friends. When the same content is matched by multiple
// sources, the most permissive one wins.
//
// Why auth is required (AuthRequired): GET lists local file names and sizes, and write endpoints
// directly decide what's publicly exposed. When no registration server is configured, AuthRequired
// passes through internally (single-machine mode); semantics are consistent with /peerjs/nodes/join.
//
// Distinction from /peerjs/nodes/:peer/shares (don't confuse the names):
//   - /peerjs/nodes/:peer/shares = **ask a peer** for its share manifest (share frames)
//   - /peerjs/share              = manage **your own** sharing scope (local state)

package controller

import (
	"net/http"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// nodeShareSvc is injected by main (same pattern as InitNodeDirectory).
var nodeShareSvc *service.NodeShare

// InitNodeShareController injects the sharing scope service (nil = this group of endpoints returns 503).
func InitNodeShareController(s *service.NodeShare) {
	log.LogDebug("ctrl-node-share: InitNodeShareController")
	nodeShareSvc = s
}

// GetNodeShare handles GET /peerjs/share:
// Returns current sharing scope + optional file list (each row with shared/by_dir/level),
// so the admin panel can render checkboxes and level selectors.
func GetNodeShare(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	scope := nodeShareSvc.Scope()
	files := nodeShareSvc.CandidateFiles()
	if files == nil {
		files = []service.ShareFileItem{} // Keep JSON as []
	}
	summary := model.NodeShares{}
	if nodeShareSvc.Enabled() {
		summary = nodeShareSvc.Summary()
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":      scope.Enable,
		"dirs":        scope.Dirs,
		"files":       files,
		"collections": scope.Collections,
		// Hashes that were checked but aren't currently in file_index must also be returned to
		// the frontend: otherwise the user can't see "I checked it" (residual checkmark after
		// file deletion), and would think the system lost their selection.
		"selected": scope.Files,
		// friends: the allowlist of peer IDs for private level. Frontend edits this directly.
		"friends": scope.Friends,
		"levels":  []string{model.LevelPublic, model.LevelUnlisted, model.LevelPrivate},
		"summary": summary,
	})
}

// PutNodeShare handles PUT /peerjs/share (partial update; unsubmitted fields remain unchanged).
func PutNodeShare(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	var patch service.ScopePatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	scope, err := nodeShareSvc.Update(patch)
	if err != nil {
		// Validation failures (volume root directory / invalid hash / invalid level) are user input
		// issues → 400, not 500
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":      scope.Enable,
		"dirs":        scope.Dirs,
		"files":       scope.Files,
		"collections": scope.Collections,
		"friends":     scope.Friends,
	})
}

// PostNodeShareFiles handles POST /peerjs/share/files
// {hashes:[...], shared:bool, level:"public"|"unlisted"|"private"}.
//
// A separate endpoint rather than letting the frontend PUT the full state each time: a checkbox
// changes one row at a time, and a full PUT requires the frontend to read the entire scope back
// and reassemble it — two concurrent clicks would overwrite each other.
//
// When level is omitted, the existing level is kept (or public if none exists) — a UI that only
// toggles "shared/not shared" shouldn't be forced to know the current level.
func PostNodeShareFiles(c *gin.Context) {
	if nodeShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "share scope not enabled (peerjs disabled)"})
		return
	}
	var body struct {
		Hashes []string `json:"hashes"`
		Shared bool     `json:"shared"`
		Level  string   `json:"level"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body: " + err.Error()})
		return
	}
	scope, err := nodeShareSvc.SetFilesShared(body.Hashes, body.Shared, body.Level)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"enable":   scope.Enable,
		"files":    scope.Files,
		"selected": scope.Files,
	})
}
