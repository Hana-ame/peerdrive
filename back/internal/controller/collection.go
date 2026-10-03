// Collection controller — collection CRUD, entry add/remove, version commit/log/rollback.
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
//   POST   /collections/:username/:coll/entries       — add path→hash entry
//   DELETE /collections/:username/:coll/entries/:path — remove entry
//   POST   /collections/:username/:coll/commit        — snapshot current entries as version + generate CID
//   GET    /collections/:username/:coll/log           — version history (newest first)
//   POST   /collections/:username/:coll/rollback/:vid — rollback to version + update CID
//   GET    /:username/:coll/*filepath                 — download file from collection entry

package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"peerdrive/internal/model"
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
// @Param q query string true "Search query"
// @Success 200 {object} map[string]interface{} "data array of collections"
// @Router /collections/search [get]
func SearchCollections(c *gin.Context) {
	q := c.Query("q")
	if q == "" {
		c.JSON(http.StatusOK, gin.H{"data": []model.Collection{}})
		return
	}
	cols, err := collSvc.Search(q)
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
	if entries == nil {
		entries = []model.CollectionEntry{}
	}
	c.JSON(http.StatusOK, gin.H{"collection": col, "entries": entries})
}

// AddEntry godoc
// @Summary Add an entry to a collection
// @Description Map a path (e.g. "dir/file.txt") to a SHA256 hash within a collection. Creates the collection if it doesn't exist.
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{path=string,hash=string} true "Entry path and file hash"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid request"
// @Router /collections/{username}/{collection_name}/entries [post]
func AddEntry(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Path      string           `json:"path"`
		Hash      string           `json:"hash"`
		Providers []model.Provider `json:"providers,omitempty"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	collID, err := collSvc.GetOrCreate(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if len(req.Providers) > 0 {
		err = collSvc.AddProviderEntry(collID, req.Path, req.Providers)
	} else if req.Hash != "" {
		err = collSvc.AddEntry(collID, req.Path, req.Hash)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "must provide hash or providers"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry added"})
}

// RemoveEntry godoc
// @Summary Remove an entry from a collection
// @Description Delete a path→hash mapping from a collection
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param path path string true "Entry path (URL-encoded)"
// @Success 200 {object} map[string]string "message"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/entries/{path} [delete]
func RemoveEntry(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	path := strings.TrimPrefix(c.Param("path"), "/")
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.RemoveEntry(col.ID, path); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "entry removed"})
}

// DownloadCollectionFile godoc
// @Summary Download a file from a collection
// @Description Look up the path in the collection's entries and redirect to /sha256sum/{hash} for download
// @Tags collections
// @Produce octet-stream
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param filepath path string true "File path within the collection"
// @Success 200 {file} binary "File content"
// @Failure 404 {object} map[string]string "Collection or file not found"
// @Router /{username}/{collection_name}/{filepath} [get]
func DownloadCollectionFile(c *gin.Context) {
	username := c.Param("username")
	collectionName := c.Param("collection_name")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	entry, err := collSvc.GetEntry(col.ID, filePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if entry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}
	providers := entry.BuildProviders()
	// Try providers in order: sha256 first
	for _, p := range providers {
		if p.Type == "sha256" && p.Value != "" {
			DownloadBySHA256Internal(c, p.Value)
			return
		}
	}
	// fallback: URL providers
	for _, p := range providers {
		if p.Type == "url" && p.Value != "" {
			if !col.FollowRedirects {
				c.JSON(http.StatusOK, gin.H{"url": p.Value, "follow_redirects": false})
				return
			}
			c.Redirect(http.StatusFound, p.Value)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "no usable provider found"})
}

// CommitCollection godoc
// @Summary Commit collection entries as a new version
// @Description Snapshot all current entries into a version with a commit message. Creates a linked version chain.
//
//	Also generates an anonymous collection JSON (SHA256), updates collections.current_hash.
//
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{commit_message=string} true "Commit message"
// @Success 200 {object} map[string]interface{} "message, version_number, snapshot_hash"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/commit [post]
func CommitCollection(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		CommitMessage string `json:"commit_message"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	// 1. Get current workspace entries
	entries, err := collSvc.ListEntries(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// 2. Convert to anonymous collection entries (with providers)
	anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
	for _, e := range entries {
		anonEntries = append(anonEntries, model.AnonCollectionEntry{
			Path:      e.Path,
			Providers: e.BuildProviders(),
		})
	}

	// 3. Build anonymous collection and save
	anonColl := model.NewAnonCollection("", anonEntries, nil)
	storageDir := c.MustGet("storageDir").(string)
	hash, err := collSvc.SaveAnon(anonColl, storageDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create snapshot: " + err.Error()})
		return
	}

	// 4. Update current_hash
	if err := collSvc.UpdateCurrentHash(col.ID, hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update current hash: " + err.Error()})
		return
	}

	// 5. Original version snapshot logic (preserves history)
	versions, err := collSvc.VersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var parentID *int
	if len(versions) > 0 {
		parentID = &versions[0].ID
	}
	verID, verNum, err := collSvc.CreateVersion(col.ID, req.CommitMessage, parentID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := collSvc.SnapshotEntries(verID, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "committed", "version_number": verNum, "snapshot_hash": hash})
}

// GetVersionLog godoc
// @Summary Get collection version history
// @Description Returns all committed versions for a collection, newest first
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Success 200 {object} map[string]interface{} "data array of versions"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/log [get]
func GetVersionLog(c *gin.Context) {
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
	versions, err := collSvc.VersionLog(col.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if versions == nil {
		versions = []model.CollectionVersion{}
	}
	c.JSON(http.StatusOK, gin.H{"data": versions})
}

// RollbackCollection godoc
// @Summary Rollback collection to a previous version
// @Description Restore collection entries from a specific version's snapshot. Replaces all current entries.
// @Tags collections
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param version_id path int true "Version ID to restore"
// @Success 200 {object} map[string]string "message"
// @Failure 400 {object} map[string]string "Invalid version_id"
// @Failure 404 {object} map[string]string "Collection not found"
// @Router /collections/{username}/{collection_name}/rollback/{version_id} [post]
func RollbackCollection(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	versionID := c.Param("version_id")
	var vid int
	if _, err := fmt.Sscanf(versionID, "%d", &vid); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid version_id"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.RestoreVersion(vid, col.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Regenerate CID
	entries, err := collSvc.ListEntries(col.ID)
	if err == nil {
		anonEntries := make([]model.AnonCollectionEntry, 0, len(entries))
		for _, e := range entries {
			anonEntries = append(anonEntries, model.AnonCollectionEntry{Path: e.Path, Hash: e.FileHash})
		}
		anonColl := model.NewAnonCollection("", anonEntries, nil)
		storageDir := c.MustGet("storageDir").(string)
		hash, err := collSvc.SaveAnon(anonColl, storageDir)
		if err == nil {
			collSvc.UpdateCurrentHash(col.ID, hash)
		}
	}

	c.JSON(http.StatusOK, gin.H{"message": "rolled back"})
}

// SetCollectionVisibility godoc
// @Summary Set collection visibility
// @Description Change the visibility of a collection (public, unlisted, private)
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{visibility=string} true "Visibility: public|unlisted|private"
// @Success 200 {object} map[string]string "ok"
// @Failure 400,404 {object} map[string]string "error"
// @Router /collections/{username}/{collection_name}/visibility [post]
func SetCollectionVisibility(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Visibility string `json:"visibility"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if req.Visibility != "public" && req.Visibility != "unlisted" && req.Visibility != "private" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "visibility must be public, unlisted, or private"})
		return
	}
	col, err := collSvc.Get(username, collectionName)
	if err != nil || col == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	if err := collSvc.SetVisibility(username, collectionName, req.Visibility); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
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

// UpdateCollectionTags godoc
// @Summary Update collection tags
// @Description Replace the tag list for a collection
// @Tags collections
// @Accept json
// @Produce json
// @Param username path string true "Username"
// @Param collection_name path string true "Collection name"
// @Param body body object{tags=[]string} true "Tag list"
// @Success 200 {object} map[string]string "ok"
// @Failure 400,404 {object} map[string]string "error"
// @Router /collections/{username}/{collection_name}/tags [post]
func UpdateCollectionTags(c *gin.Context) {
	username := collectionUsername(c)
	collectionName := c.Param("collection_name")
	var req struct {
		Tags []string `json:"tags"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if err := collSvc.UpdateTags(username, collectionName, req.Tags); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "ok"})
}
