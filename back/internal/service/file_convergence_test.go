package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

// 发现背景（Issue #405）：读路径原先 file_meta 优先、file_index 兜底，两个来源
// 在旧库里会给出不同的 filename/大小。收敛后 file_index 是权威；本用例把
// 「index 与镜像故意不一致时读哪一边」钉死，防止以后有人把顺序改回去。

func TestGetMeta_IndexIsAuthoritativeOverLegacyMirror(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "real-name.txt")
	require.NoError(t, os.WriteFile(src, []byte("index first"), 0o644))
	hash, err := svc.RegisterLocal(src, "real-name.txt")
	require.NoError(t, err)

	idx, err := repository.GetFileIndex(hash)
	require.NoError(t, err)
	require.NotNil(t, idx)
	assert.Equal(t, repository.FileOriginUnified, idx.Origin, "local registration must use the unified write")
	assert.NotEmpty(t, idx.CID)

	// Make the legacy mirror disagree on purpose: an index-first read must ignore it.
	_, err = repository.GetDB().Exec(`UPDATE file_meta SET filename = 'stale-legacy.txt', mime_type = 'text/x-stale' WHERE hash = ?`, hash)
	require.NoError(t, err)

	meta, err := svc.GetMeta(hash)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "real-name.txt", meta.Filename, "filename must come from file_index")
	assert.Equal(t, int64(len("index first")), meta.Size)

	byCID, err := svc.GetMetaByCID(idx.CID)
	require.NoError(t, err)
	require.NotNil(t, byCID)
	assert.Equal(t, hash, byCID.Hash, "CID lookup must resolve through file_index")

	items, err := svc.ListAll("time")
	require.NoError(t, err)
	occurrences := 0
	for _, it := range items {
		if it.Hash == hash {
			occurrences++
			assert.Equal(t, "real-name.txt", it.Filename)
			assert.Equal(t, idx.Path, it.ProviderPath)
		}
	}
	assert.Equal(t, 1, occurrences, "index+legacy merge must not duplicate a file")
}

func TestDelete_RemovesUnifiedIndexAndLegacyMirror(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, "doomed.txt")
	require.NoError(t, os.WriteFile(src, []byte("bye"), 0o644))
	hash, err := svc.RegisterLocal(src, "doomed.txt")
	require.NoError(t, err)

	require.NoError(t, svc.Delete(hash))

	meta, err := svc.GetMeta(hash)
	require.NoError(t, err)
	assert.Nil(t, meta, "deleted file must not be readable from either track")
}

// 发现背景（Issue #405 Step 2）：GetMeta/GetMetaByCID 的 file_meta 兜底已经删掉。
// 只有 legacy 元数据行、没有 file_index 行的内容必须不可见——让旧数据进 index 的
// 唯一合法通道是启动回填（BackfillFileIndexFromLegacy / BackfillRemoteURLsFromLegacy）。
// 兜底留着就等于 file_index 永远不是唯一真相源。顺带确认空 path 的 index 行
// （URL-only 注册）仍然可读：path 为空不再等于未索引。
func TestGetMeta_IndexOnlyNoLegacyFallback(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	legacyHash := "77777777777777777777777777777777777777777777777777777777777777777"
	_, err := repository.GetDB().Exec(`INSERT INTO file_meta (hash, size, filename, type, cid) VALUES (?, 9, 'legacy-only.txt', 'blob', 'cid-legacy-only')`, legacyHash)
	require.NoError(t, err)

	meta, err := svc.GetMeta(legacyHash)
	require.NoError(t, err)
	assert.Nil(t, meta, "a file_meta-only row must not be readable without an index row")
	byCID, err := svc.GetMetaByCID("cid-legacy-only")
	require.NoError(t, err)
	assert.Nil(t, byCID, "same for the CID lookup")

	urlHash := "88888888888888888888888888888888888888888888888888888888888888888"
	_, err = repository.RegisterFile(repository.FileRecord{Hash: urlHash, Name: "remote.txt", Size: 4, RemoteURL: "https://e.example/r.txt", CID: "cid-remote"})
	require.NoError(t, err)
	meta, err = svc.GetMeta(urlHash)
	require.NoError(t, err)
	require.NotNil(t, meta)
	assert.Equal(t, "remote.txt", meta.Filename, "an empty-path index row must still be readable")
	byCID, err = svc.GetMetaByCID("cid-remote")
	require.NoError(t, err)
	require.NotNil(t, byCID)
	assert.Equal(t, urlHash, byCID.Hash)
}

// 发现背景（Issue #405 Step 2）：file_index 现在可能只有一条 remote_url 腿、没有
// 本地 path（URL-only 注册）。这类行原先靠 file_providers 的 join 才出现在列表里，
// 现在直接从 index 投影——投影必须报 provider_type='http'，否则 API 会把一个只有
// 远端 URL 的文件谎报成本地文件。
func TestListAll_URLOnlyIndexRowIsAnHTTPProvider(t *testing.T) {
	dir, svc := setupFileServiceTest()
	defer os.RemoveAll(dir)

	urlHash := "99999999999999999999999999999999999999999999999999999999999999999"
	_, err := repository.RegisterFile(repository.FileRecord{Hash: urlHash, Name: "only-remote.bin", Size: 7, MimeType: "application/octet-stream", RemoteURL: "https://r.example/o.bin"})
	require.NoError(t, err)

	items, err := svc.ListAll("time")
	require.NoError(t, err)
	var got []model.FileListItem
	for _, it := range items {
		if it.Hash == urlHash {
			got = append(got, it)
		}
	}
	require.Len(t, got, 1, "a url-only index row must appear in the listing exactly once")
	assert.Equal(t, "http", got[0].ProviderType)
	assert.Equal(t, "https://r.example/o.bin", got[0].ProviderPath)
	assert.Equal(t, "only-remote.bin", got[0].Filename)
}
