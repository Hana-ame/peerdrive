package repository

// Note: This file is a test for legacy code (see doc/archive/LEGACY.md, pending deletion/migration); discovery background is not annotated individually. The "discovery background" convention applies to new code.

import (
	"testing"

	"peerdrive/internal/model"

	"github.com/stretchr/testify/assert"
)

func setup() {
	InitDB(":memory:")
}

func TestInsertFileMetaAndGetFileMeta(t *testing.T) {
	setup()

	meta := &model.FileMeta{
		Hash:     "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
		Size:     1024,
		MimeType: "text/plain",
		Gziped:   false,
		Filename: "test.txt",
		Type:     FileTypeBlob,
	}

	err := InsertFileMeta(meta)
	assert.NoError(t, err)

	retrieved, err := GetFileMeta(meta.Hash)
	assert.NoError(t, err)
	assert.NotNil(t, retrieved)
	assert.Equal(t, meta.Hash, retrieved.Hash)
	assert.Equal(t, meta.Size, retrieved.Size)
	assert.Equal(t, meta.MimeType, retrieved.MimeType)
	assert.Equal(t, meta.Gziped, retrieved.Gziped)
	assert.Equal(t, meta.Filename, retrieved.Filename)
	assert.Equal(t, meta.Type, retrieved.Type)
}

func TestGetFileMetaNonexistent(t *testing.T) {
	setup()

	meta, err := GetFileMeta("nonexistent_hash_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.NoError(t, err)
	assert.Nil(t, meta)
}

func TestInsertFileProviderAndGetFileProviders(t *testing.T) {
	setup()

	hash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     100,
		Filename: "data.bin",
		Type:     FileTypeBlob,
	}
	err := InsertFileMeta(meta)
	assert.NoError(t, err)

	err = InsertFileProvider(hash, "local", "/tmp/data.bin")
	assert.NoError(t, err)

	err = InsertFileProvider(hash, "remote", "/remote/data.bin")
	assert.NoError(t, err)

	providers, err := GetFileProviders(hash)
	assert.NoError(t, err)
	assert.Len(t, providers, 2)

	assert.Equal(t, "local", providers[0].ProviderType)
	assert.Equal(t, "/tmp/data.bin", providers[0].Path)
	assert.True(t, providers[0].Available)

	assert.Equal(t, "remote", providers[1].ProviderType)
	assert.Equal(t, "/remote/data.bin", providers[1].Path)
	assert.True(t, providers[1].Available)
}

func TestMarkProviderUnavailable(t *testing.T) {
	setup()

	hash := "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     100,
		Filename: "data.bin",
		Type:     FileTypeBlob,
	}
	err := InsertFileMeta(meta)
	assert.NoError(t, err)

	err = InsertFileProvider(hash, "local", "/tmp/data.bin")
	assert.NoError(t, err)

	providers, err := GetFileProviders(hash)
	assert.NoError(t, err)
	assert.Len(t, providers, 1)

	err = MarkProviderUnavailable(providers[0].ID)
	assert.NoError(t, err)

	providers, err = GetFileProviders(hash)
	assert.NoError(t, err)
	assert.Len(t, providers, 0)
}

func TestGetFileProvidersEmptyForNonexistentHash(t *testing.T) {
	setup()

	providers, err := GetFileProviders("nonexistent_hash_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.NoError(t, err)
	assert.Nil(t, providers)
}

// TestCheckFileIndexConsistency verifies that the file_index vs file_meta
// divergence detector works correctly.
// 发现背景 (Issue #280): file_index (P2P path index) and file_meta
// (content-addressed metadata) are maintained independently and can
// silently diverge. This test verifies the consistency check detects
// mismatches in both directions.
func TestCheckFileIndexConsistency(t *testing.T) {
	setup()

	// Empty DB → no mismatches
	onlyIdx, onlyMeta, err := CheckFileIndexConsistency()
	assert.NoError(t, err)
	assert.Equal(t, 0, onlyIdx)
	assert.Equal(t, 0, onlyMeta)

	// Insert a file_meta row without file_index entry
	_, err = db.Exec(`INSERT INTO file_meta (hash, size, type) VALUES (?, 100, 'blob')`, "aaa1")
	assert.NoError(t, err)

	// Now 1 hash only in file_meta
	onlyIdx, onlyMeta, err = CheckFileIndexConsistency()
	assert.NoError(t, err)
	assert.Equal(t, 0, onlyIdx)
	assert.Equal(t, 1, onlyMeta)

	// Insert a file_index row without file_meta entry
	_, err = db.Exec(`INSERT INTO file_index (hash, path, seq) VALUES (?, '/tmp/test', 1)`, "bbb2")
	assert.NoError(t, err)

	// Now 1 hash only in file_index, 1 only in file_meta
	onlyIdx, onlyMeta, err = CheckFileIndexConsistency()
	assert.NoError(t, err)
	assert.Equal(t, 1, onlyIdx)
	assert.Equal(t, 1, onlyMeta)

	// Add file_meta for bbb2 → now both tables have it, no mismatch for bbb2
	_, err = db.Exec(`INSERT INTO file_meta (hash, size, type) VALUES (?, 200, 'blob')`, "bbb2")
	assert.NoError(t, err)

	onlyIdx, onlyMeta, err = CheckFileIndexConsistency()
	assert.NoError(t, err)
	assert.Equal(t, 0, onlyIdx) // bbb2 now in both
	assert.Equal(t, 1, onlyMeta) // aaa1 still only in meta
}
