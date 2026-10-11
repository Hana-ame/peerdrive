// Collection controller — collection CRUD, listing, search, and public discovery.
// A collection is a named set of path→hash mappings belonging to a user.
// Operates on SQLite tables collections, collection_entries, collection_versions, and
// version_entries through the repository package.
//
// CID pointer mechanism:
//   On Commit, in addition to the version snapshot, the current entries are assembled
//   into an AnonCollection JSON, stored via anon_repo.StoreCollection to obtain the CID,
//   then repo.UpdateCurrentCID is called.
//   On GetCollection, if collections.current_cid is non-empty, the entries are read
//   from the CID's JSON via the Downloader; otherwise fall back to the collection_entries table.
//   After Rollback, the CID must also be regenerated and current_cid updated.
//
// Routes:
//   POST   /collections                              — create collection
//   GET    /collections/:username                     — list user collections
//   GET    /collections/:username/:coll               — get collection + entries (CID preferred)
//   GET    /collections/search                        — search collections by query/tag/visibility
//   GET    /collections/public                        — list all public collections
//
// Sub-module handlers:
//   collection_entries.go: AddEntry, RemoveEntry, DownloadCollectionFile
//   collection_version.go: CommitCollection, GetVersionLog, RollbackCollection, SetCollectionVisibility, UpdateCollectionTags

package controller

import (
	"encoding/json"
	"net/http"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// collSvc is the collection domain service (after M2 layering, controllers no longer call repository directly).
var collSvc *service.CollectionService

// InitCollectionController injects the CollectionService instance (called during router assembly).
func InitCollectionController(svc *service.CollectionService) {
	collSvc = svc
}

// collectionUsername reads the username parameter from collection routes.
// Background: the collections group sub-routes (entries/commit/rollback/visibility/tags) register
// parameter name :id (gin allows only one param segment per prefix), but handlers uniformly read
// :username — grep finds no call sites, it's always empty at runtime, causing GetOrCreate("") to
// create an empty-username dirty row and entries hanging on the wrong collection (exposed by
// 2026-08-19 test.sh 7d/7e: Add entry OK but Get entries empty). The dispatcher (dispatchGet*)
// uses withParams to fill in username; directly mounted handlers need this fallback.
func collectionUsername(c *gin.Context) string {
	username := c.Param("username")
	if username == "" {
		return c.Param("id")
	}
	return username
}

// CreateCollection godoc
// @Summary Create a new collection
// @Description Create a named collection for a user. Duplicate (username, collection_name) returns 409.
// @Tags collections
// @Accept json
// @Produce json
// @Param body body object{username=string,collection_name=string} true "Username and collection name"
// @Success 200 {object} map[string]interface{} "id, username, collection_name"
// @Failure 400 {object} map[string]string "Invalid request"
// @Failure 409 {object} map[string]string "Duplicate"
// @Router /collections [post]
func CreateCollection(c *gin.Context) {
	var req struct {
		Username        string   `json:"username"`
		CollectionName  string   `json:"collection_name"`
		Visibility      string   `json:"visibility"`
		FollowRedirects *bool    `json:"follow_redirects"`
		Tags            []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	var id int
	var err error
	if req.FollowRedirects != nil {
		id, err = collSvc.Create(req.Username, req.CollectionName, req.Visibility, req.FollowRedirects, req.Tags)
	} else if len(req.Tags) > 0 {
		id, err = collSvc.Create(req.Username, req.CollectionName, req.Visibility, nil, req.Tags)
	} else {
		id, err = collSvc.Create(req.Username, req.CollectionName, req.Visibility, nil, nil)
	}
	if err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": id, "username": req.Username, "collection_name": req.CollectionName, "visibility": req.Visibility})
}

// ListCollections godoc
// @Summary List collections for a user
// @Description Returns all collections owned by the given username
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/{username} [get]
func ListCollections(c *gin.Context) {
	username := c.Param("username")
	cols, err := collSvc.List(username)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}

// SearchCollections godoc
// @Summary Search collections
// @Description Search collections by username or collection name
// @Tags collections
// @Produce json
// @Param q query string false "Search query"
// @Param tag query string false "Single tag to filter"
// @Param tags query string false "Comma-separated tags to filter"
// @Param visibility query string false "Visibility filter (public, all, restricted, private)"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/search [get]
func SearchCollections(c *gin.Context) {
	q := c.Query("q")
	tag := c.Query("tag")
	tagsParam := c.Query("tags")
	visibility := c.Query("visibility")

	var tagList []string
	if tag != "" {
		tagList = append(tagList, strings.TrimSpace(tag))
	}
	if tagsParam != "" {
		for _, part := range strings.Split(tagsParam, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				tagList = append(tagList, part)
			}
		}
	}

	if q == "" && len(tagList) == 0 {
		c.JSON(http.StatusOK, gin.H{"data": []model.Collection{}})
		return
	}

	cols, err := collSvc.SearchWithFilters(q, tagList, visibility)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}

// GetCollection godoc
// @Summary Get collection details with entries
// @Description Returns collection metadata and its path→hash entries
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "collection and entries"
// @Failure 404 {object} map[string]string "Not found"
// @Router /collections/{username}/{collection_name} [get]
func GetCollection(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	var entries []model.CollectionEntry
	if col.CurrentHash != nil && *col.CurrentHash != "" {
		storageDir := c.MustGet("storageDir").(string)
		anonColl, err := collSvc.GetAnonByHash(*col.CurrentHash, storageDir)
		if err == nil {
			anonColl.NormalizeEntries()
			entries = make([]model.CollectionEntry, 0, len(anonColl.Entries))
			for _, ae := range anonColl.Entries {
				pj, _ := json.Marshal(ae.Providers)
				entries = append(entries, model.CollectionEntry{
					Path:          ae.Path,
					FileHash:      ae.GetPrimaryHash(),
					ProvidersJSON: string(pj),
				})
			}
		}
	}
	if entries == nil {
		entries, err = collSvc.ListEntries(col.ID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
	}
	// CAS-DB consistency check (Issue #279): when both CAS and DB entries
	// are available, compare entry counts to detect silent divergence.
	if col.CurrentHash != nil && *col.CurrentHash != "" {
		dbEntries, dbErr := repository.ListCollectionEntries(col.ID)
		if dbErr == nil && len(dbEntries) != len(entries) {
			log.LogWarn("collection: CAS/DB entry count mismatch for %s/%s: CAS=%d DB=%d (cid=%s) — CAS preferred, DB may be stale",
				username, collectionName, len(entries), len(dbEntries), *col.CurrentHash)
		}
	}
	if entries == nil {
		entries = []model.CollectionEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"collection": col, "entries": entries})
}

// ListPublicCollections godoc
// @Summary List all public collections
// @Description Returns all collections with visibility = 'public', optionally filtered by search query
// @Tags collections
// @Produce json
// @Param q query string false "Search query"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/public [get]
func ListPublicCollections(c *gin.Context) {
	q := c.Query("q")
	cols, err := collSvc.ListPublic(q)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if cols == nil {
		cols = []model.Collection{}
	}
	c.JSON(http.StatusOK, gin.H{"data": cols})
}
