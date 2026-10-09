// CollectionService is the collection domain use-case orchestration layer.
// M2 convergence layer: controllers previously called repository directly (60+ scattered
// across collection/fork/merge/file controllers); now all collection read/write goes
// through this layer -- controllers only depend on service, and repository is only
// referenced by service. Method names map one-to-one with repository methods, acting as
// transparent forwarders (future caching/transactions go here).
package service

import (
	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

type CollectionService struct{}

func NewCollectionService() *CollectionService { return &CollectionService{} }

// --- Query ---

// Get looks up a collection by username + collection name (returns nil, nil if not found).
func (s *CollectionService) Get(username, collectionName string) (*model.Collection, error) {
	return repository.GetCollection(username, collectionName)
}

// List lists all collections for the specified user.
func (s *CollectionService) List(username string) ([]model.Collection, error) {
	return repository.ListCollections(username)
}

// Search performs fuzzy search among public collections.
func (s *CollectionService) Search(q string) ([]model.Collection, error) {
	return repository.SearchCollections(q)
}

// SearchWithFilter performs fuzzy search among public collections with optional tag filter.
func (s *CollectionService) SearchWithFilter(q, tag string) ([]model.Collection, error) {
	return repository.SearchCollectionsWithFilter(q, tag, "public")
}

// ListPublic lists all public collections; when q is non-empty, filters by username/collection name.
func (s *CollectionService) ListPublic(q string) ([]model.Collection, error) {
	return repository.ListPublicCollections(q)
}

// Create creates a collection and returns the new collection ID.
// When followRedirects != nil, the explicit value is used; when tags is non-empty, tags are included.
// This merges the semantics of the three repository Create variants (originally three branches in the controller).
func (s *CollectionService) Create(username, collectionName, visibility string, followRedirects *bool, tags []string) (int, error) {
	if followRedirects != nil {
		return repository.CreateCollectionWithFull(username, collectionName, visibility, *followRedirects, tags)
	}
	if len(tags) > 0 {
		return repository.CreateCollectionWithTags(username, collectionName, visibility, tags)
	}
	return repository.CreateCollectionWithVisibility(username, collectionName, visibility)
}

// CreatePlain creates a collection (without visibility/tags, used by the fork flow).
func (s *CollectionService) CreatePlain(username, collectionName string) (int, error) {
	return repository.CreateCollection(username, collectionName)
}

// GetOrCreate queries or creates, returning the collection ID.
func (s *CollectionService) GetOrCreate(username, collectionName string) (int, error) {
	return repository.GetOrCreateCollection(username, collectionName)
}

// SetVisibility sets the visibility.
func (s *CollectionService) SetVisibility(username, collectionName, visibility string) error {
	return repository.SetCollectionVisibility(username, collectionName, visibility)
}

// UpdateTags updates the collection tags.
func (s *CollectionService) UpdateTags(username, collectionName string, tags []string) error {
	return repository.UpdateCollectionTags(username, collectionName, tags)
}

// UpdateCurrentHash updates the current snapshot pointer (called after commit).
func (s *CollectionService) UpdateCurrentHash(collectionID int, hash string) error {
	return repository.UpdateCurrentHash(collectionID, hash)
}

// --- Entries ---

// ListEntries lists all entries in a collection.
func (s *CollectionService) ListEntries(collectionID int) ([]model.CollectionEntry, error) {
	return repository.ListCollectionEntries(collectionID)
}

// GetEntry looks up a single entry by path.
func (s *CollectionService) GetEntry(collectionID int, path string) (*model.CollectionEntry, error) {
	return repository.GetCollectionEntry(collectionID, path)
}

// AddEntry adds an entry (stores only the hash).
func (s *CollectionService) AddEntry(collectionID int, path, hash string) error {
	return repository.AddCollectionEntry(collectionID, path, hash)
}

// AddProviderEntry adds an entry with providers.
func (s *CollectionService) AddProviderEntry(collectionID int, path string, providers []model.Provider) error {
	return repository.AddProviderCollectionEntry(collectionID, path, providers)
}

// RemoveEntry removes an entry.
func (s *CollectionService) RemoveEntry(collectionID int, path string) error {
	return repository.RemoveCollectionEntry(collectionID, path)
}

// --- Versions ---

// CreateVersion creates a version snapshot, returning the version ID and version number.
func (s *CollectionService) CreateVersion(collectionID int, commitMsg string, parentVersionID *int) (int, int, error) {
	return repository.CreateVersion(collectionID, commitMsg, parentVersionID)
}

// SnapshotEntries copies the current collection entries into version_entries.
func (s *CollectionService) SnapshotEntries(versionID, collectionID int) error {
	return repository.SnapshotVersionEntries(versionID, collectionID)
}

// VersionLog returns the version history.
func (s *CollectionService) VersionLog(collectionID int) ([]model.CollectionVersion, error) {
	return repository.GetVersionLog(collectionID)
}

// VersionEntries returns the snapshot entries for a specified version.
func (s *CollectionService) VersionEntries(versionID int) ([]model.VersionEntry, error) {
	return repository.GetVersionEntries(versionID)
}

// RestoreVersion overwrites the current entries with an old version snapshot.
func (s *CollectionService) RestoreVersion(versionID, collectionID int) error {
	return repository.RestoreVersionEntries(versionID, collectionID)
}

// --- Anonymous collection adapter (Plaza/AnonExplorer main path) ---

// GetAnonByHash reads anonymous collection metadata (JSON storage, with version validation).
func (s *CollectionService) GetAnonByHash(hash, storageDir string) (*model.AnonCollection, error) {
	return repository.GetAnonCollectionByHash(hash, storageDir)
}

// SaveAnon persists an anonymous collection (JSON to disk + file_meta registration).
func (s *CollectionService) SaveAnon(coll *model.AnonCollection, storageDir string) (string, error) {
	return repository.SaveCollection(coll, storageDir)
}
