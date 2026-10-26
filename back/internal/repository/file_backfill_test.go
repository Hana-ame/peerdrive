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

// 发现背景（Issue #405 Step 2）：file_index 已经是权威存储，file_meta /
// file_providers 只是废弃窗口内的兼容镜像。PEERDRIVE_LEGACY_META_MIRROR=0 是
// 灰度闸门——关掉后 RegisterFile 必须只剩 file_index 一条写入，否则 Step 3 的
// DROP TABLE 会直接丢数据。本用例把闸门行为钉死。
func TestRegisterFile_MirrorOffSkipsLegacyTables(t *testing.T) {
	initTestDB(t)
	require.True(t, LegacyMetaMirrorEnabled(), "mirror must default on")
	t.Cleanup(func() { SetLegacyMetaMirror(true) })

	onHash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err := RegisterFile(FileRecord{Hash: onHash, Path: "/tmp/on.bin", Name: "on.bin", Size: 5, ProviderPath: "aa/" + onHash})
	require.NoError(t, err)
	_, err = GetFileMeta(onHash)
	require.NoError(t, err)
	providers, err := GetFileProviders(onHash)
	require.NoError(t, err)
	assert.Len(t, providers, 1, "mirror on writes the legacy provider row")

	SetLegacyMetaMirror(false)
	offHash := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	_, err = RegisterFile(FileRecord{Hash: offHash, Path: "/tmp/off.bin", Name: "off.bin", Size: 5, ProviderPath: "bb/" + offHash})
	require.NoError(t, err)

	idx, err := GetFileIndex(offHash)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, FileOriginUnified, idx.Origin, "index write must not be gated by the mirror")
	assert.Equal(t, "/tmp/off.bin", idx.Path)

	meta, err := GetFileMeta(offHash)
	require.NoError(t, err)
	assert.Nil(t, meta, "mirror off must not write file_meta")
	providers, err = GetFileProviders(offHash)
	require.NoError(t, err)
	assert.Empty(t, providers, "mirror off must not write file_providers")
}

// 发现背景（Issue #405 Step 2）：同一个 hash 的多条 URL 注册内容等价，先写入的
// URL 不比后写入的更正确，覆盖会丢掉可能仍可访问的地址。remote_url 因此是
// 先到先得；本用例锁定这个取舍，防止以后有人顺手改成 last-writer-wins。
func TestRegisterFile_RemoteURLFirstWriterWins(t *testing.T) {
	initTestDB(t)

	hash := "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	_, err := RegisterFile(FileRecord{Hash: hash, Name: "r.bin", Size: 1, RemoteURL: "https://a.example/1"})
	require.NoError(t, err)
	_, err = RegisterFile(FileRecord{Hash: hash, Name: "r.bin", Size: 1, RemoteURL: "https://b.example/2"})
	require.NoError(t, err)

	idx, err := GetFileIndex(hash)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, "https://a.example/1", idx.RemoteURL, "the first registered URL must be kept")

	// 空的 remote_url 不得把已有值冲掉（ON CONFLICT 的 CASE 分支）。
	_, err = RegisterFile(FileRecord{Hash: hash, Name: "r.bin", Size: 1})
	require.NoError(t, err)
	idx, err = GetFileIndex(hash)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, "https://a.example/1", idx.RemoteURL)
}

// 发现背景（Issue #405 Step 2）：老库里的 URL 注册只存在 file_providers 的
// provider_type='http' 行里，且大多没有本地字节（file_index 回填按设计拒绝索引
// 无法本地服务的行），所以它们一直是 file_providers 的专属住户。Step 2 要把这条
// 腿迁到 file_index.remote_url，本用例锁定「填已有行 / 补索引缺失的纯 URL 行 /
// 忽略不可用 provider / 幂等」四条契约。
func TestBackfillRemoteURLsFromLegacy(t *testing.T) {
	initTestDB(t)

	existingHash := "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	urlOnlyHash := "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	unavailableHash := "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"

	// 已在 file_index、remote_url 为空：应被 UPDATE 填上。
	_, err := RegisterFile(FileRecord{Hash: existingHash, Path: "/tmp/x.bin", Name: "x.bin", Size: 3})
	require.NoError(t, err)
	// 纯 URL 注册：index 里完全没有行，也没有本地字节。file_providers.hash 有指向
	// file_meta 的外键，所以先补 legacy 元数据行（existingHash 已由 RegisterFile
	// 的镜像写入建好）。
	insertLegacyMeta := func(hash, name string) {
		require.NoError(t, InsertFileMeta(&model.FileMeta{Hash: hash, Size: 2, Filename: name, Type: model.FileTypeBlob}))
	}
	insertLegacyMeta(urlOnlyHash, "remote-old.bin")
	insertLegacyMeta(unavailableHash, "dead-old.bin")
	insertLegacyHTTP := func(hash, url string) {
		_, err := GetDB().Exec(`INSERT INTO file_providers (hash, provider_type, path, available) VALUES (?, 'http', ?, 1)`, hash, url)
		require.NoError(t, err)
	}
	insertLegacyHTTP(existingHash, "https://e.example/x.bin")
	insertLegacyHTTP(urlOnlyHash, "https://first.example/f.bin")
	insertLegacyHTTP(urlOnlyHash, "https://second.example/f.bin") // 同 hash 第二条：必须被忽略
	insertLegacyHTTP(unavailableHash, "https://dead.example/f.bin")
	_, err = GetDB().Exec(`UPDATE file_providers SET available = 0 WHERE path = 'https://dead.example/f.bin'`)
	require.NoError(t, err)

	st, err := BackfillRemoteURLsFromLegacy()
	require.NoError(t, err)
	assert.Equal(t, 1, st.Filled, "the existing index row must be filled in place")
	assert.Equal(t, 1, st.Imported, "the url-only legacy row must be indexed")

	fi, err := GetFileIndex(urlOnlyHash)
	require.NoError(t, err)
	require.NotNil(t, fi)
	assert.Equal(t, "https://first.example/f.bin", fi.RemoteURL, "lowest-id provider must win")
	assert.Equal(t, "", fi.Path, "a url-only registration has no local bytes")
	assert.Equal(t, FileOriginBackfill, fi.Origin)
	assert.Equal(t, model.FileTypeBlob, fi.Kind)
	assert.False(t, fi.Deleted)

	fi2, err := GetFileIndex(existingHash)
	require.NoError(t, err)
	require.NotNil(t, fi2)
	assert.Equal(t, "https://e.example/x.bin", fi2.RemoteURL)
	assert.Equal(t, "/tmp/x.bin", fi2.Path, "the local path must survive the URL backfill")

	fi3, err := GetFileIndex(unavailableHash)
	assert.Nil(t, fi3, "an unavailable provider must not create an index row")
	assert.ErrorIs(t, err, sql.ErrNoRows)

	// 幂等：再跑一遍不得重复填、不得重复建。
	st2, err := BackfillRemoteURLsFromLegacy()
	require.NoError(t, err)
	assert.Equal(t, 0, st2.Filled)
	assert.Equal(t, 0, st2.Imported)
}
