// peer_pull.go: Cross-node pull-save endpoints (doc/NETDISK.md M3 / ROADMAP phase 6).
//
// User actions → endpoint mapping:
//   - Peer node detail page "Save to my drive" (single file) → POST /p2p/pull
//   - Collection card "Save entire collection"               → POST /p2p/pull/collection
//   - Transfer page view/cancel                               → GET /p2p/pull, POST /p2p/pull/cancel
//
// All are static paths (no :id parameters): gin's routing tree tends to conflict when
// static segments and parameter segments are at the same level, and these actions can
// semantically be expressed via the body anyway, so there's no need to introduce path parameters.
package controller

import (
	"encoding/json"
	"net/http"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// peerPuller injected by main.
var peerPuller *service.PeerPuller

// InitPeerPuller injects the cross-node pull service.
func InitPeerPuller(p *service.PeerPuller) {
	log.LogDebug("ctrl-peer-pull: InitPeerPuller")
	peerPuller = p
}

// ListPullJobs handles GET /p2p/pull.
func ListPullJobs(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	jobs := peerPuller.List()
	c.JSON(http.StatusOK, gin.H{"jobs": jobs, "count": len(jobs)})
}

// StartPull handles POST /p2p/pull {peer, hash, name?, path?}.
func StartPull(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		Peer string `json:"peer"`
		Hash string `json:"hash"`
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Peer == "" || req.Hash == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer and hash are required"})
		return
	}
	job, err := peerPuller.Start(req.Peer, req.Hash, req.Name, req.Path, "")
	if err != nil {
		log.LogWarn("ctrl-peer-pull: start pull from %s failed: %v", req.Peer, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"job": job})
}

// StartPullCollection handles POST /p2p/pull/collection {peer, collection}.
//
// Semantics: request a share manifest once from the peer (share frame) → find the collection →
// create pull tasks for all its entries. The manifest is **pulled on demand** rather than pre-cached:
// nodes can change their sharing scope at any time, so when the user clicks "Save entire collection"
// the peer's current state must be authoritative (not the same as the count summary on the marketplace
// card, which is just guidance info).
func StartPullCollection(c *gin.Context) {
	if peerPuller == nil || peerShareSvc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		Peer       string `json:"peer"`
		Collection string `json:"collection"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Peer == "" || req.Collection == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "peer and collection are required"})
		return
	}
	snap, err := peerShareSvc.RequestShares(req.Peer)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	var found bool
	for _, coll := range snap.Collections {
		if coll.Hash != req.Collection {
			continue
		}
		found = true
		entries := make([]service.PullEntry, 0, len(coll.Entries))
		for _, e := range coll.Entries {
			entries = append(entries, service.PullEntry{Path: e.Path, Hash: e.Hash})
		}
		jobs := peerPuller.StartCollection(req.Peer, coll.Hash, entries)
		c.JSON(http.StatusOK, gin.H{
			"collection": coll.Hash,
			"name":       coll.Name,
			"jobs":       jobs,
			"count":      len(jobs),
		})
		return
	}
	if !found {
		// Not in the manifest ≠ doesn't exist: unlisted collections **by definition** aren't in the manifest,
		// but their manifest is a content-addressed JSON that can be fetched directly by hash. Without this
		// fallback, "pull an entire collection via link" would never work (not found in manifest → 404).
		//
		// Access level is not affected: the fetch goes through the same req channel, and the peer's ShareGate
		// still evaluates — a private collection's manifest can't be fetched, and we return 404 here too (the
		// server won't tell you "it exists but won't give it to you" for something not in the manifest).
		jobs, name, ok := startPullByManifest(req.Peer, req.Collection)
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "collection not shared by peer"})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"collection": req.Collection,
			"name":       name,
			"jobs":       jobs,
			"count":      len(jobs),
		})
	}
}

// startPullByManifest when the collection isn't found in the manifest, fetches the manifest by hash and creates tasks.
//
// Returns (jobs, name, ok); ok=false means this path is also blocked (hash doesn't exist / offline / private
// blocked / fetched content isn't a manifest); the caller treats all such cases as 404.
func startPullByManifest(peer, hash string) ([]*service.PullJob, string, bool) {
	raw, err := peerPuller.FetchManifest(peer, hash, 0)
	if err != nil {
		log.LogDebug("ctrl-peer-pull: fetch collection manifest %s failed: %v", hash, err)
		return nil, "", false
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(raw, &coll); err != nil || len(coll.Entries) == 0 {
		// Fetched it but it's not a collection (user passed a file's hash as a collection hash)
		log.LogDebug("ctrl-peer-pull: %s is not a collection manifest", hash)
		return nil, "", false
	}
	entries := make([]service.PullEntry, 0, len(coll.Entries))
	for _, e := range coll.Entries {
		h := e.GetPrimaryHash()
		if h == "" {
			continue
		}
		entries = append(entries, service.PullEntry{Path: e.Path, Hash: h})
	}
	if len(entries) == 0 {
		return nil, "", false
	}
	return peerPuller.StartCollection(peer, hash, entries), coll.FriendlyName, true
}

// CancelPull handles POST /p2p/pull/cancel {id}.
func CancelPull(c *gin.Context) {
	if peerPuller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "peer pull not enabled"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if err := peerPuller.Cancel(req.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "cancelled", "id": req.ID})
}
