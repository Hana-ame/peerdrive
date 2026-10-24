package router

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"peerdrive/internal/controller"
)

// dispatchCreateCollection dispatches POST /collections:
// body with username field → user system (CreateCollection), otherwise anonymous collection.
func dispatchCreateCollection(c *gin.Context) {
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		controller.CreateAnonCollection(c)
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))

	var probe struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && probe.Username != "" {
		controller.CreateCollection(c)
		return
	}
	controller.CreateAnonCollection(c)
}

// dispatchGetCollection dispatches GET /collections/:id:
// 64-char hex → anonymous collection (GetAnonCollection), otherwise list collections by username.
// Background: anon system (hash addressing) and user system (username addressing) share the
// /collections path, but gin doesn't allow the same :param level with different names,
// so use :id uniformly and dispatch by shape.
func dispatchGetCollection(c *gin.Context) {
	id := c.Param("id")
	if len(id) == 64 {
		cp := withParams(c, "hash", id)
		controller.GetAnonCollection(cp)
		return
	}
	cp := withParams(c, "username", id)
	controller.ListCollections(cp)
}

// dispatchGetTree dispatches GET /collections/:id/*filepath:
//   - id is 64-char hex → anonymous collection file download (DownloadAnonFile)
//   - filepath empty → user collection list (ListCollections)
//   - filepath one segment → user collection details (GetCollection)
//   - filepath two segments with last segment "log" → version log (GetVersionLog)
//
// Gotcha: gin doesn't allow :param and *wildcard coexistence at the same path level
// ("/:username/:collection_name" and "/:username/*filepath" panic on registration),
// so all user-system deep GETs are merged into this single wildcard route with internal dispatch.
func dispatchGetTree(c *gin.Context) {
	id := c.Param("id")
	parts := splitPath(c.Param("filepath"))

	if len(id) == 64 {
		cp := withParams(c, "hash", id)
		controller.DownloadAnonFile(cp)
		return
	}
	switch {
	case len(parts) == 0:
		cp := withParams(c, "username", id)
		controller.ListCollections(cp)
	case len(parts) == 1:
		cp := withParams(c, "username", id, "collection_name", parts[0])
		controller.GetCollection(cp)
	case len(parts) == 2 && parts[1] == "log":
		cp := withParams(c, "username", id, "collection_name", parts[0])
		controller.GetVersionLog(cp)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	}
}

// withParams appends URL parameters to the original context and returns the original c.
// Background: gin's c.Param only reads parameter names registered at route definition;
// the dispatcher merges routes with different semantics (:id → :hash/:username/:collection_name),
// so append Params to fill in, letting underlying controllers work without changes.
// Gotcha: don't use c.Copy() — gin v1.8+ Context.Copy doesn't copy ResponseWriter,
// downstream c.JSON would nil-pointer panic (exposed by 2026-08-19 test.sh 6b GET
// /collections/tester returning 500). Dispatcher calls are single-request serial,
// appending to the original context is safe; after the handler returns, the request
// ends, no need to restore Params.
func withParams(c *gin.Context, kv ...string) *gin.Context {
	for i := 0; i+1 < len(kv); i += 2 {
		c.Params = append(c.Params, gin.Param{Key: kv[i], Value: kv[i+1]})
	}
	return c
}

func splitPath(p string) []string {
	p = strings.Trim(p, "/")
	if p == "" {
		return nil
	}
	return strings.Split(p, "/")
}
