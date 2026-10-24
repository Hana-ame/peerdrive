package repository

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/model"
)

// 发现背景（Issue #405）：file_meta/file_providers 是 v1.0 之前的主存储，
// file_index 是后来的统一索引。两者长期并存，开发库里可能只有旧表有数据。
// 本用例锁定「旧库打开即升级、回填幂等、不删除旧数据」这三条升级契约。

// oldSchemaDDL is the pre-#405 schema (file_index has no kind/mime/cid/origin).
const oldSchemaDDL = `
CREATE TABLE file_meta (
	hash TEXT PRIMARY KEY,
	size INTEGER DEFAULT 0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	mime_type TEXT DEFAULT '',
	gziped INTEGER DEFAULT 0,
	filename TEXT,
	type TEXT DEFAULT 'blob',
	cid TEXT DEFAULT ''
);
CREATE TABLE file_providers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	hash TEXT NOT NULL REFERENCES file_meta(hash),
	provider_type TEXT NOT NULL,
	path TEXT NOT NULL,
	available INTEGER DEFAULT 1
);
CREATE TABLE file_index (
	hash TEXT PRIMARY KEY,
	path TEXT NOT NULL,
	name TEXT DEFAULT '',
	size INTEGER DEFAULT 0,
	deleted INTEGER DEFAULT 0,
	seq INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

func TestBackfillFileIndexFromLegacy_UpgradesOldDatabase(t *testing.T) {
	dir := t.TempDir()
	storageDir := filepath.Join(dir, "storage")
	dbPath := filepath.Join(dir, "legacy.db")
	require.NoError(t, os.MkdirAll(storageDir, 0o755))

	// Two legacy files that really exist on disk (blob + anon collection manifest),
	// one http-only row (no local path → cannot be indexed) and one stale local row
	// whose bytes are gone (must not be advertised).
	blobHash := "1111111111111111111111111111111111111111111111111111111111111111"
	anonHash := "2222222222222222222222222222222222222222222222222222222222222222"
	httpHash := "3333333333333333333333333333333333333333333333333333333333333333"
	staleHash := "4444444444444444444444444444444444444444444444444444444444444444"

	blobRel := filepath.Join(blobHash[:2], blobHash)
	anonRel := filepath.Join(anonHash[:2], anonHash)
	for _, rel := range []string{blobRel, anonRel} {
		full := filepath.Join(storageDir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte("legacy bytes"), 0o644))
	}

	raw, err := sql.Open(sqliteDriver, dbPath)
	require.NoError(t, err)
	_, err = raw.Exec(oldSchemaDDL)
	require.NoError(t, err)
	insertMeta := func(hash, name, kind, mime, cid string) {
		_, err := raw.Exec(`INSERT INTO file_meta (hash, size, mime_type, filename, type, cid) VALUES (?, ?, ?, ?, ?, ?)`,
			hash, 12, mime, name, kind, cid)
		require.NoError(t, err)
	}
	insertProvider := func(hash, providerType, path string) {
		_, err := raw.Exec(`INSERT INTO file_providers (hash, provider_type, path) VALUES (?, ?, ?)`, hash, providerType, path)
		require.NoError(t, err)
	}
	insertMeta(blobHash, "old-blob.txt", model.FileTypeBlob, "text/plain", "cid-blob")
	insertProvider(blobHash, "local", blobRel)
	insertMeta(anonHash, "anon_old.json", model.FileTypeAnonCollection, "application/json", "cid-anon")
	insertProvider(anonHash, "local", anonRel)
	insertMeta(httpHash, "remote.bin", model.FileTypeBlob, "", "cid-http")
	insertProvider(httpHash, "http", "https://example.com/remote.bin")
	insertMeta(staleHash, "gone.bin", model.FileTypeBlob, "", "cid-gone")
	insertProvider(staleHash, "local", filepath.Join(staleHash[:2], staleHash))
	require.NoError(t, raw.Close())

	// Opening with the new binary must add the missing file_index columns without
	// touching the legacy rows.
	handle, err := OpenDB(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = CloseHandle(handle) })
	SetDB(handle)
	t.Cleanup(func() { SetDB(nil) })

	for _, col := range []string{"kind", "mime_type", "gziped", "cid", "origin"} {
		var n int
		require.NoError(t, handle.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('file_index') WHERE name = ?`, col).Scan(&n))
		assert.Equal(t, 1, n, "old DB must gain file_index.%s on upgrade", col)
	}

	stats, err := BackfillFileIndexFromLegacy(storageDir)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Imported, "blob + anon collection with existing bytes must be imported")
	assert.Equal(t, 1, stats.SkippedNoPath, "http-only row has no local path to index")
	assert.Equal(t, 1, stats.SkippedMissingFile, "stale local row must not be advertised")

	blobRow, err := GetFileIndex(blobHash)
	require.NoError(t, err)
	require.NotNil(t, blobRow)
	assert.Equal(t, filepath.Join(storageDir, blobRel), blobRow.Path)
	assert.Equal(t, "old-blob.txt", blobRow.Name)
	assert.Equal(t, model.FileTypeBlob, blobRow.Kind)
	assert.Equal(t, "text/plain", blobRow.MimeType)
	assert.Equal(t, "cid-blob", blobRow.CID)
	assert.Equal(t, FileOriginBackfill, blobRow.Origin)

	anonRow, err := GetFileIndex(anonHash)
	require.NoError(t, err)
	require.NotNil(t, anonRow)
	assert.Equal(t, model.FileTypeAnonCollection, anonRow.Kind, "collection manifests must keep their kind")
	assert.Equal(t, "application/json", anonRow.MimeType)

	// Non-blob rows must stay out of the P2P file surfaces.
	blobList, err := ListFileIndex(0, 100)
	require.NoError(t, err)
	require.Len(t, blobList, 1)
	assert.Equal(t, blobHash, blobList[0].Hash)

	// Legacy data is retained (additive migration, rollback-safe).
	var legacyCount int
	require.NoError(t, handle.QueryRow(`SELECT COUNT(*) FROM file_meta`).Scan(&legacyCount))
	assert.Equal(t, 4, legacyCount, "backfill must not delete legacy rows")
	require.NoError(t, handle.QueryRow(`SELECT COUNT(*) FROM file_providers`).Scan(&legacyCount))
	assert.Equal(t, 4, legacyCount)

	// Idempotent: a second startup imports nothing and does not duplicate rows.
	stats, err = BackfillFileIndexFromLegacy(storageDir)
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Imported)
	var indexCount int
	require.NoError(t, handle.QueryRow(`SELECT COUNT(*) FROM file_index`).Scan(&indexCount))
	assert.Equal(t, 2, indexCount)
}

func TestRegisterFile_DualWritesWithUnifiedOrigin(t *testing.T) {
	initTestDB(t)

	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	seq, err := RegisterFile(FileRecord{
		Hash:         hash,
		Path:         "/tmp/unified.bin",
		Name:         "unified.bin",
		Size:         42,
		Kind:         model.FileTypeBlob,
		MimeType:     "application/octet-stream",
		ProviderPath: "aa/" + hash,
	})
	require.NoError(t, err)
	assert.Greater(t, seq, int64(0))

	// Authoritative row.
	fi, err := GetFileIndex(hash)
	require.NoError(t, err)
	require.NotNil(t, fi)
	assert.Equal(t, FileOriginUnified, fi.Origin)
	assert.Equal(t, "application/octet-stream", fi.MimeType)
	assert.NotEmpty(t, fi.CID, "CID must be derived when the caller does not provide one")

	// Compatibility mirror.
	meta, err := GetFileMeta(hash)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "unified.bin", meta.Filename)
	providers, err := GetFileProviders(hash)
	require.NoError(t, err)
	require.Len(t, providers, 1)
	assert.Equal(t, "aa/"+hash, providers[0].Path)

	// Re-register with a forcing empty name must not blank the existing filename,
	// and must not duplicate the provider row.
	_, err = RegisterFile(FileRecord{Hash: hash, Path: "/tmp/unified.bin", Size: 42, ProviderPath: "aa/" + hash})
	require.NoError(t, err)
	meta, err = GetFileMeta(hash)
	require.NoError(t, err)
	assert.Equal(t, "unified.bin", meta.Filename)
	providers, err = GetFileProviders(hash)
	require.NoError(t, err)
	assert.Len(t, providers, 1)

	// Index-only writes (P2P sync) must not resurrect legacy rows.
	syncHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, err = UpsertFileIndex(syncHash, "/remote/path.bin", "path.bin", 7, false)
	require.NoError(t, err)
	meta, err = GetFileMeta(syncHash)
	require.NoError(t, err)
	assert.Nil(t, meta, "UpsertFileIndex stays index-only; only RegisterFile mirrors")
}
