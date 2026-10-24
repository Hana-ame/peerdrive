package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
