package controller

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"peerdrive/internal/model"
	"peerdrive/internal/nodestate"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

var anonSvc *service.AnonService

// InitAnonController injects the AnonService instance for anonymous collection handlers.
func InitAnonController(svc *service.AnonService) {
	anonSvc = svc
}

// CreateAnonCollection godoc
// @Summary      Create anonymous collection
// @Description  Create an immutable content-addressed collection with file entries. Entries validated for path traversal and valid SHA256 hashes.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{friendly_name=string,entries=[]object{path=string,hash=string}}  true  "Collection entries"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request or invalid path/hash"
// @Failure      500  {object}  map[string]string  "Internal error"
// @Router       /anon/collections [post]
func CreateAnonCollection(c *gin.Context) {
	var req struct {
		FriendlyName string                      `json:"friendly_name"`
		Entries      []model.AnonCollectionEntry `json:"entries"`
		Tags         []string                    `json:"tags"`
		// Visibility/AccessList correspond to the frontend's "public access / restricted to specified / private only" options.
		// Old frontends without these two fields → visibility empty string → service layer defaults to public, behavior unchanged.
		Visibility   string   `json:"visibility"`
		AccessList   []string `json:"access_list"`
		AccessPolicy string   `json:"access_policy"`
		Passcode     string   `json:"passcode"`
		PSK          string   `json:"psk"`
		PSKHash      string   `json:"psk_hash"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	// Owner is the current node operator: only nodes logged into regserver have an account; anonymous nodes get an empty string
	owner := nodestate.GetOperator()
	pskVal := req.PSK
	if pskVal == "" && req.PSKHash != "" {
		pskVal = req.PSKHash
	}

	hash, err := anonSvc.CreateCollectionWithPolicyAndPSK(req.FriendlyName, req.Entries, req.Tags, req.Visibility, req.AccessList, req.AccessPolicy, req.Passcode, pskVal, owner)
	if err != nil {
		msg := err.Error()
		switch {
		case strings.Contains(msg, "invalid visibility"), strings.Contains(msg, "access_list required"),
			strings.Contains(msg, "invalid access policy"), strings.Contains(msg, "passcode required"),
			strings.Contains(msg, "psk required"):
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		case strings.Contains(msg, "invalid path"), strings.Contains(msg, "invalid hash"), strings.Contains(msg, "invalid providers"):
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		writeInternalError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash, "visibility": req.Visibility, "access_policy": req.AccessPolicy, "owner": owner})
}

// SetAnonCollectionVisibility godoc
// @Summary      Update visibility of an anonymous collection
// @Description  Switch a collection between public / restricted (access_list) / private.
//
//	The collection is content-addressed: changing visibility writes a new JSON file,
//	so the response carries the NEW hash — the old hash still resolves to the old setting.
//
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        hash path string true "Collection SHA256 hash"
// @Param        body body object{visibility=string,access_list=[]string} true "Visibility settings"
// @Success      200 {object} map[string]string "hash, visibility"
// @Failure      400 {object} map[string]string "Invalid visibility"
// @Failure      404 {object} map[string]string "Not found or not owned by this operator"
// @Router       /anon/collections/{hash}/visibility [put]
func SetAnonCollectionVisibility(c *gin.Context) {
	hash := c.Param("hash")
	var req struct {
		Visibility string   `json:"visibility"`
		AccessList []string `json:"access_list"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	newHash, err := anonSvc.UpdateCollectionVisibility(hash, req.Visibility, req.AccessList, nodestate.GetOperator())
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid visibility") || strings.Contains(msg, "access_list required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": newHash, "previous_hash": hash, "visibility": req.Visibility})
}

// ListAnonCollections godoc
// @Summary      List anonymous collections
// @Description  Returns all anonymous collections known to this node (from file_meta).
// @Tags         anon
// @Produce      json
// @Success      200  {array}  model.AnonCollectionSummary  "List of collections"
// @Router       /anon/collections [get]
func ListAnonCollections(c *gin.Context) {
	colls, err := anonSvc.ListCollections()
	if err != nil {
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, colls)
}

// GetAnonCollection godoc
// @Summary      Get anonymous collection by hash
// @Description  Retrieve an anonymous collection's metadata and entries.
// @Tags         anon
// @Produce      json
// @Param        hash  path  string  true  "Collection SHA256 hash"
// @Success      200  {object}  model.AnonCollection  "Collection"
// @Failure      404  {object}  map[string]string     "Collection not found"
// @Router       /anon/collections/{hash} [get]
func GetAnonCollection(c *gin.Context) {
	hash := c.Param("hash")
	// Visibility gate: private/restricted collections are equivalent to "not found" (404) for unauthorized requesters.
	// Gotcha: this previously used GetCollectionByHash for a raw read — once you have the hash of a content-addressed
	// JSON, anyone can fetch it, making the three permission tiers meaningless (downloads already check visibility via
	// DownloadAnonFile, but the metadata endpoint was missing it = bypass). The current node operator is used as the requester identity.
	operator := nodestate.GetOperator()
	coll, err := anonSvc.GetCollectionVisibleTo(hash, operator)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}
	passcode := c.Query("passcode")
	if passcode == "" {
		passcode = c.GetHeader("X-Passcode")
	}
	psk := c.Query("psk")
	if psk == "" {
		psk = c.GetHeader("X-PSK")
	}
	// Issue #268: If protected and passcode doesn't match, hide entries and mark is_protected: true
	if coll.EffectiveAccessPolicy() == model.AccessPolicyProtected && operator == "" {
		if subtle.ConstantTimeCompare([]byte(passcode), []byte(coll.Passcode)) != 1 {
			masked := *coll
			masked.Entries = []model.AnonCollectionEntry{}
			masked.IsProtected = true
			masked.Passcode = ""
			masked.PSKHash = ""
			c.JSON(http.StatusOK, masked)
			return
		}
	}
	// Issue #338: If PSK protected and PSK doesn't match, hide entries and mark is_protected: true
	if (coll.PSKHash != "" || coll.EffectiveAccessPolicy() == model.AccessPolicyPSK || coll.EffectiveVisibility() == model.VisibilityPSK) && (operator == "" || operator != coll.Owner) {
		if !coll.VerifyPSK(psk) {
			masked := *coll
			masked.Entries = []model.AnonCollectionEntry{}
			masked.IsProtected = true
			masked.Passcode = ""
			masked.PSKHash = ""
			c.JSON(http.StatusOK, masked)
			return
		}
	}
	resp := *coll
	resp.Passcode = ""
	resp.PSKHash = ""
	c.JSON(http.StatusOK, resp)
}

// DownloadAnonFile godoc
// @Summary      Download file from anonymous collection
// @Description  Download a specific file entry from an anonymous collection by hash and file path.
// @Tags         anon
// @Produce      octet-stream
// @Param        hash      path  string  true  "Collection SHA256 hash"
// @Param        filepath  path  string  true  "File path within collection"
// @Success      200  {file}  binary  "File content"
// @Failure      404  {object}  map[string]string  "Collection or file not found"
// @Router       /anon/collections/{hash}/{filepath} [get]
func DownloadAnonFile(c *gin.Context) {
	hash := c.Param("hash")
	filePath := strings.TrimPrefix(c.Param("filepath"), "/")
	// Dispatch /anon/collections/:hash/history through wildcard route to prevent Gin tree collision with /*filepath
	if filePath == "history" {
		GetAnonCollectionHistory(c)
		return
	}

	operator := nodestate.GetOperator()
	coll, err := anonSvc.GetCollectionVisibleTo(hash, operator)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "collection not found"})
		return
	}

	if coll.EffectiveAccessPolicy() == model.AccessPolicyProtected && operator == "" {
		passcode := c.Query("passcode")
		if passcode == "" {
			passcode = c.GetHeader("X-Passcode")
		}
		if subtle.ConstantTimeCompare([]byte(passcode), []byte(coll.Passcode)) != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "passcode required or invalid for protected collection"})
			return
		}
	}

	psk := c.Query("psk")
	if psk == "" {
		psk = c.GetHeader("X-PSK")
	}
	// Issue #338: PSK verification for file downloads
	if (coll.PSKHash != "" || coll.EffectiveAccessPolicy() == model.AccessPolicyPSK || coll.EffectiveVisibility() == model.VisibilityPSK) && (operator == "" || operator != coll.Owner) {
		if !coll.VerifyPSK(psk) {
			c.JSON(http.StatusForbidden, gin.H{"error": "psk required or invalid for psk-protected collection"})
			return
		}
	}

	var targetEntry *model.AnonCollectionEntry
	for i := range coll.Entries {
		if coll.Entries[i].Path == filePath {
			targetEntry = &coll.Entries[i]
			break
		}
	}
	if targetEntry == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "file not found in collection"})
		return
	}

	// Try downloading by provider order: sha256 first, url as fallback
	primaryHash := targetEntry.GetPrimaryHash()
	if primaryHash != "" {
		if universalDownloader != nil {
			ctx := c.Request.Context()
			data, protocol, err := universalDownloader.Download(ctx, primaryHash)
			if err == nil {
				c.Header("X-Protocol", protocol)
				downloadFilename := filepath.Base(filePath)
				mimeType := targetEntry.GetPrimaryMime()
				if mimeType == "" {
					mimeType = "application/octet-stream"
				}
				if c.Query("inline") == "1" {
					c.Header("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, downloadFilename))
				} else {
					c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, downloadFilename))
				}
				c.Data(http.StatusOK, mimeType, data)
				return
			}
		}
	}
	// fallback: URL providers
	for _, p := range targetEntry.Providers {
		if p.Type == "url" && p.Value != "" {
			c.Redirect(http.StatusFound, p.Value)
			return
		}
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "no usable provider found for this file"})
}

// ForkAnonCollection godoc
// @Summary      Fork anonymous collection
// @Description  Create a variant of an anonymous collection by adding and/or removing file entries.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{source_hash=string,friendly_name=string,add_entries=[]object{path=string,hash=string},remove_paths=[]string}  true  "Fork parameters"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request"
// @Failure      404  {object}  map[string]string  "Source collection not found"
// @Router       /anon/collections/fork [post]
func ForkAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash   string                      `json:"source_hash"`
		FriendlyName string                      `json:"friendly_name"`
		AddEntries   []model.AnonCollectionEntry `json:"add_entries"`
		RemovePaths  []string                    `json:"remove_paths"`
		AccessPolicy string                      `json:"access_policy"`
		Passcode     string                      `json:"passcode"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Source collection read by visibility (same as GetAnonCollection): otherwise anyone with any hash
	// could fork a private/restricted collection into a public copy — the three permission tiers bypassed by forking.
	operator := nodestate.GetOperator()
	src, err := anonSvc.GetCollectionVisibleTo(req.SourceHash, operator)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "source collection not found"})
		return
	}

	// Issue #268: If source collection is protected and operator is empty, require valid passcode
	if src.EffectiveAccessPolicy() == model.AccessPolicyProtected && operator == "" {
		forkPasscode := req.Passcode
		if forkPasscode == "" {
			forkPasscode = c.Query("passcode")
			if forkPasscode == "" {
				forkPasscode = c.GetHeader("X-Passcode")
			}
		}
		if subtle.ConstantTimeCompare([]byte(forkPasscode), []byte(src.Passcode)) != 1 {
			c.JSON(http.StatusForbidden, gin.H{"error": "passcode required or invalid for protected source collection"})
			return
		}
	}

	removeSet := map[string]bool{}
	for _, p := range req.RemovePaths {
		removeSet[p] = true
	}

	var newEntries []model.AnonCollectionEntry
	for _, e := range src.Entries {
		if !removeSet[e.Path] {
			newEntries = append(newEntries, e)
		}
	}

	existing := map[string]int{}
	for i, e := range newEntries {
		existing[e.Path] = i
	}
	for _, e := range req.AddEntries {
		if idx, ok := existing[e.Path]; ok {
			newEntries[idx] = e
		} else {
			newEntries = append(newEntries, e)
		}
	}

	friendlyName := req.FriendlyName
	if friendlyName == "" {
		friendlyName = src.FriendlyName
	}
	// Fork creates a new local copy, but the permission tier must be inherited from the source:
	// if the source is restricted/private and the copy defaults to public, restricted content is
	// re-exposed (same files with a different hash bypass permissions).
	// Owner carries over from the source (when the source has no Owner, record the current node operator to ensure private copies remain readable).
	forkOwner := src.Owner
	if forkOwner == "" {
		forkOwner = operator
	}
	policy := req.AccessPolicy
	if policy == "" {
		policy = src.EffectiveAccessPolicy()
	}
	passcode := req.Passcode
	if passcode == "" {
		passcode = src.Passcode
	}
	hash, err := anonSvc.CreateCollectionWithPolicy(
		friendlyName, newEntries, src.Tags, src.Visibility, src.AccessList, policy, passcode, forkOwner)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid visibility") || strings.Contains(msg, "access_list required") ||
			strings.Contains(msg, "invalid access policy") || strings.Contains(msg, "passcode required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": hash})
}

// CommitAnonCollection godoc
// @Summary      Commit anonymous collection (versioned)
// @Description  Commit modifications to an existing anonymous collection. Accepts a list of entries to add/update (non-empty hash) or remove (empty hash). Increments version and generates a new content hash.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        body  body  object{source_hash=string,entries=[]object{path=string,hash=string},commit_message=string}  true  "Commit parameters"
// @Success      201  {object}  map[string]string  "hash"
// @Failure      400  {object}  map[string]string  "Invalid request or invalid path/hash"
// @Failure      404  {object}  map[string]string  "Source collection not found"
// @Router       /anon/collections/commit [post]
func CommitAnonCollection(c *gin.Context) {
	var req struct {
		SourceHash    string                      `json:"source_hash"`
		Entries       []model.AnonCollectionEntry `json:"entries"`
		CommitMessage string                      `json:"commit_message"`
		Passcode      string                      `json:"passcode"`
		PSK           string                      `json:"psk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}

	passcode := req.Passcode
	if passcode == "" {
		passcode = c.Query("passcode")
		if passcode == "" {
			passcode = c.GetHeader("X-Passcode")
		}
	}
	psk := req.PSK
	if psk == "" {
		psk = c.Query("psk")
		if psk == "" {
			psk = c.GetHeader("X-PSK")
		}
	}

	hash, err := anonSvc.CommitCollectionWithPasscodeAndPSK(req.SourceHash, req.Entries, req.CommitMessage, passcode, psk, nodestate.GetOperator())
	if err != nil {
		if strings.Contains(err.Error(), "passcode required or invalid") || strings.Contains(err.Error(), "psk required or invalid") {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "source collection not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
			return
		}
		if strings.Contains(err.Error(), "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		writeInternalError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"hash": hash, "prev_sha": req.SourceHash})
}

// AddAnonCollectionEntry godoc
// @Summary      Add or update entry in anonymous collection
// @Description  Adds or updates a single file entry in an existing collection, generating a new version with prev_sha pointing to parent version.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        hash  path  string  true  "Source Collection SHA256 hash"
// @Success      201   {object}  map[string]string  "hash, prev_sha, path"
// @Router       /anon/collections/{hash}/entries [post]
func AddAnonCollectionEntry(c *gin.Context) {
	hash := c.Param("hash")
	var req struct {
		Entry model.AnonCollectionEntry `json:"entry"`
		Path  string                    `json:"path"`
		Hash  string                    `json:"hash"`
		PSK   string                    `json:"psk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	entry := req.Entry
	if entry.Path == "" && req.Path != "" {
		entry.Path = req.Path
		if req.Hash != "" {
			entry.Hash = req.Hash
			entry.Normalize()
		}
	}
	psk := req.PSK
	if psk == "" {
		psk = c.Query("psk")
		if psk == "" {
			psk = c.GetHeader("X-PSK")
		}
	}
	newHash, err := anonSvc.AddCollectionEntry(hash, entry, nodestate.GetOperator(), psk)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "psk required or invalid") {
			c.JSON(http.StatusForbidden, gin.H{"error": msg})
			return
		}
		if strings.Contains(msg, "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": msg})
			return
		}
		if strings.Contains(msg, "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"hash": newHash, "prev_sha": hash, "path": entry.Path})
}

// RemoveAnonCollectionEntry godoc
// @Summary      Remove entry from anonymous collection
// @Description  Removes a file entry by path, generating a new version with prev_sha pointing to parent version.
// @Tags         anon
// @Produce      json
// @Param        hash  path  string  true  "Source Collection SHA256 hash"
// @Param        path  query string  false "File path within collection"
// @Success      200   {object}  map[string]string  "hash, prev_sha, removed"
// @Router       /anon/collections/{hash}/entries [delete]
func RemoveAnonCollectionEntry(c *gin.Context) {
	hash := c.Param("hash")
	path := c.Query("path")
	var req struct {
		Path string `json:"path"`
		PSK  string `json:"psk"`
	}
	if c.Request.ContentLength > 0 {
		_ = c.ShouldBindJSON(&req)
	}
	if path == "" {
		path = req.Path
	}
	if path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "path required"})
		return
	}
	psk := req.PSK
	if psk == "" {
		psk = c.Query("psk")
		if psk == "" {
			psk = c.GetHeader("X-PSK")
		}
	}
	newHash, err := anonSvc.RemoveCollectionEntry(hash, path, nodestate.GetOperator(), psk)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "psk required or invalid") {
			c.JSON(http.StatusForbidden, gin.H{"error": msg})
			return
		}
		if strings.Contains(msg, "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": msg})
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": newHash, "prev_sha": hash, "removed": path})
}

// UpdateAnonCollectionEntry godoc
// @Summary      Update entry in anonymous collection
// @Description  Updates a file entry in an existing collection, generating a new version with prev_sha pointing to parent version.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        hash  path  string  true  "Source Collection SHA256 hash"
// @Success      200   {object}  map[string]string  "hash, prev_sha, path"
// @Router       /anon/collections/{hash}/entries [put]
func UpdateAnonCollectionEntry(c *gin.Context) {
	hash := c.Param("hash")
	var req struct {
		Entry model.AnonCollectionEntry `json:"entry"`
		Path  string                    `json:"path"`
		Hash  string                    `json:"hash"`
		PSK   string                    `json:"psk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: " + err.Error()})
		return
	}
	entry := req.Entry
	if entry.Path == "" && req.Path != "" {
		entry.Path = req.Path
		if req.Hash != "" {
			entry.Hash = req.Hash
			entry.Normalize()
		}
	}
	psk := req.PSK
	if psk == "" {
		psk = c.Query("psk")
		if psk == "" {
			psk = c.GetHeader("X-PSK")
		}
	}
	newHash, err := anonSvc.UpdateCollectionEntry(hash, entry, nodestate.GetOperator(), psk)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "psk required or invalid") {
			c.JSON(http.StatusForbidden, gin.H{"error": msg})
			return
		}
		if strings.Contains(msg, "not found") {
			c.JSON(http.StatusNotFound, gin.H{"error": msg})
			return
		}
		if strings.Contains(msg, "invalid") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		writeInternalError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": newHash, "prev_sha": hash, "path": entry.Path})
}

// GetAnonCollectionHistory godoc
// @Summary      Get version history of anonymous collection
// @Description  Returns backward version history chain along prev_sha pointers.
// @Tags         anon
// @Produce      json
// @Param        hash       path   string true  "Collection SHA256 hash"
// @Param        max_depth  query  int    false "Maximum versions to traverse (default 50)"
// @Success      200        {array} model.AnonCollectionSummary "Version history"
// @Router       /anon/collections/{hash}/history [get]
func GetAnonCollectionHistory(c *gin.Context) {
	hash := c.Param("hash")
	maxDepth := 50
	if depthStr := c.Query("max_depth"); depthStr != "" {
		if d, err := strconv.Atoi(depthStr); err == nil && d > 0 {
			maxDepth = d
		}
	}
	psk := c.Query("psk")
	if psk == "" {
		psk = c.GetHeader("X-PSK")
	}
	history, err := anonSvc.GetCollectionHistory(hash, maxDepth, nodestate.GetOperator(), psk)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, history)
}

// SetAnonCollectionPermissions godoc
// @Summary      Update permissions (visibility / access_policy / PSK) of an anonymous collection
// @Description  Creates a new collection version with updated permissions and prev_sha pointing to parent version.
// @Tags         anon
// @Accept       json
// @Produce      json
// @Param        hash  path  string true "Collection SHA256 hash"
// @Success      200   {object} map[string]string "hash, prev_sha, visibility, access_policy"
// @Router       /anon/collections/{hash}/permissions [put]
func SetAnonCollectionPermissions(c *gin.Context) {
	hash := c.Param("hash")
	var req struct {
		Visibility   string   `json:"visibility"`
		AccessList   []string `json:"access_list"`
		AccessPolicy string   `json:"access_policy"`
		PSK          string   `json:"psk"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	newHash, err := anonSvc.UpdateCollectionPermissions(hash, req.Visibility, req.AccessList, req.AccessPolicy, req.PSK, nodestate.GetOperator())
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid") || strings.Contains(msg, "required") {
			c.JSON(http.StatusBadRequest, gin.H{"error": msg})
			return
		}
		c.JSON(http.StatusNotFound, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"hash": newHash, "prev_sha": hash, "visibility": req.Visibility, "access_policy": req.AccessPolicy})
}
