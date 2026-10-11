package service

import (
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/config"
	"peerdrive/internal/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAnonService_SeedSampleCollectionIfEmpty tests automatic seeding of sample collections from ~/ files (Issue #341).
// Discovery background: When starting up a new node, /#/collections is empty with no sample collections to explore.
// SeedSampleCollectionIfEmpty populates a sample collection with files from the user environment.
func TestAnonService_SeedSampleCollectionIfEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	storageDir := filepath.Join(tmpDir, "storage")
	require.NoError(t, os.MkdirAll(storageDir, 0755))

	require.NoError(t, repository.InitDB(dbPath))
	repository.SetAnonStorageDir(storageDir)
	t.Cleanup(func() {
		_ = repository.CloseDB()
	})

	cfg := &config.Config{
		DBPath:     dbPath,
		StorageDir: storageDir,
	}
	svc := NewAnonService(cfg)

	// 1. Initial list should be empty
	initialList, err := svc.ListCollections()
	require.NoError(t, err)
	assert.Empty(t, initialList)

	// 2. Run seeding
	readRootsAdded := make([]string, 0)
	hash, err := svc.SeedSampleCollectionIfEmpty(storageDir, func(dir string) {
		readRootsAdded = append(readRootsAdded, dir)
	})
	require.NoError(t, err)
	assert.NotEmpty(t, hash, "Should return seeded collection hash")
	assert.NotEmpty(t, readRootsAdded, "Should add at least one read root")

	// 3. Verify collection exists in ListCollections
	afterList, err := svc.ListCollections()
	require.NoError(t, err)
	require.Len(t, afterList, 1)
	assert.Equal(t, hash, afterList[0].Hash)
	assert.Equal(t, "Home Sample Collection", afterList[0].FriendlyName)

	// 4. Verify collection details
	coll, err := svc.GetCollectionByHash(hash)
	require.NoError(t, err)
	assert.Equal(t, "Home Sample Collection", coll.FriendlyName)
	assert.Equal(t, "public", coll.Visibility)
	assert.NotEmpty(t, coll.Entries)
	assert.Contains(t, coll.Tags, "sample")

	// 5. Calling seed again should be idempotent (no duplicate created)
	hashSecond, err := svc.SeedSampleCollectionIfEmpty(storageDir, nil)
	require.NoError(t, err)
	assert.Empty(t, hashSecond, "Should return empty string on already-seeded repository")

	listFinal, err := svc.ListCollections()
	require.NoError(t, err)
	assert.Len(t, listFinal, 1, "Should not duplicate collections")
}
