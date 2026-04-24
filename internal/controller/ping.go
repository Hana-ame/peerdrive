// Package controller provides Gin HTTP handlers for all API endpoints.
// Usage: controllers are initialized via Init* functions called from router.SetupRouter.
//
// Endpoint handlers:
//   Ping                    GET  /ping
//   DownloadBySHA256        GET  /sha256sum/:sha256
//   DownloadBySHA256Internal     
//   GetNodeInfo             GET  /p2p/node
//   GetPeers                GET  /p2p/peers
//   PingPeer                GET  /p2p/ping/:peer_id
//   UploadFile              POST /files/upload
//   RegisterLocalFile       POST /files/register_local
//   RegisterFolder          POST /files/register_folder
//   VerifyFile              GET  /files/verify/:hash
//   DeleteFile              DELETE /files/:hash
//   DiffVersions            POST /files/diff
//   CreateCollection        POST /collections
//   ListCollections         GET  /collections/:username
//   GetCollection           GET  /collections/:username/:collection_name
//   AddEntry                POST /collections/:username/:collection_name/entries
//   RemoveEntry             DELETE /collections/:username/:collection_name/entries/*path
//   DownloadCollectionFile  GET  /:username/:collection_name/*filepath
//   CommitCollection        POST /collections/:username/:collection_name/commit
//   GetVersionLog           GET  /collections/:username/:collection_name/log
//   RollbackCollection      POST /collections/:username/:collection_name/rollback/:version_id
//   MergeFromSource         POST /actions/merge
//   ForkCollection          POST /actions/fork
//   PullCollection          POST /actions/pull
//   GetTaskStatus           GET  /tasks/:id
//   ListTasks               GET  /tasks

package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// Ping godoc
// @Summary Health check
// @Description Returns "pong" to confirm the server is running
// @Tags health
// @Produce plain
// @Success 200 {string} string "pong"
// @Router /ping [get]
func Ping(c *gin.Context) {
	c.String(http.StatusOK, "pong")
}
