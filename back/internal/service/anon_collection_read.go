package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

// GetCollectionByHash reads an anonymous collection from content-addressed storage by SHA256 hash.
func (s *AnonService) GetCollectionByHash(hash string) (*model.AnonCollection, error) {
	defer log.LogDuration("AnonService.GetCollectionByHash")()
	log.LogDebug("anon-svc: GetCollectionByHash hash=%s", hash)

	// Defense: hash comes from URL path/request body/remote P2P sync. Without length
	// validation, hash[:2] will panic with slice out of bounds; non-64-digit hex values
	// (like "..") will also let filepath.Join escape the storage directory.
	// Invalid hashes return not-found directly.
	if !isValidHash(hash) {
		log.LogWarn("anon-svc: GetCollectionByHash invalid hash=%q", hash)
		return nil, fmt.Errorf("collection not found")
	}

	filePath := filepath.Join(s.config.StorageDir, hash[:2], hash)
	data, err := os.ReadFile(filePath)
	if err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s not found: %v", hash, err)
		return nil, fmt.Errorf("collection not found")
	}
	var coll model.AnonCollection
	if err := json.Unmarshal(data, &coll); err != nil {
		log.LogError("anon-svc: GetCollectionByHash %s invalid JSON: %v", hash, err)
		return nil, fmt.Errorf("invalid collection json")
	}
	if coll.Version < 1 {
		log.LogError("anon-svc: GetCollectionByHash %s unsupported version: %d", hash, coll.Version)
		return nil, fmt.Errorf("unsupported version: %d", coll.Version)
	}
	coll.NormalizeEntries()
	log.LogInfo("anon-svc: GetCollectionByHash %s found (version=%d, entries=%d)", hash, coll.Version, len(coll.Entries))
	return &coll, nil
}

// ListCollections returns a summary list of all registered anonymous collections.
func (s *AnonService) ListCollections() ([]model.AnonCollectionSummary, error) {
	return repository.ListAnonCollections(s.config.StorageDir)
}

// GetCollectionVisibleTo returns a collection to the requester based on visibility:
// unauthorized access is equivalent to not existing (404 semantics).
// requester is the account bound to the current request; local WS sessions are passed
// the operator from the controller, so the operator can always see their own
// private/restricted collections.
// Pitfall: do NOT allow access just because "no account is available" --
// unauthenticated users can only see public collections.
func (s *AnonService) GetCollectionVisibleTo(hash, requester string) (*model.AnonCollection, error) {
	coll, err := s.GetCollectionByHash(hash)
	if err != nil {
		return nil, err
	}
	if !coll.CanView(requester) {
		log.LogWarn("anon-svc: GetCollectionVisibleTo denied hash=%s requester=%q visibility=%s",
			hash, requester, coll.EffectiveVisibility())
		return nil, fmt.Errorf("collection not found")
	}
	return coll, nil
}
