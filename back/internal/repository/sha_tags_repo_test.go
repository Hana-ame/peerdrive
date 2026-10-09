package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShaTagsRepo(t *testing.T) {
	// 发现背景：Issue #91 建立 SHA 内容寻址级独立标签索引（sha_tags）
	dir := t.TempDir()
	require.NoError(t, InitDB(filepath.Join(dir, "test.db")))
	t.Cleanup(func() { _ = CloseDB() })

	sha1 := "1111111111111111111111111111111111111111111111111111111111111111"
	sha2 := "2222222222222222222222222222222222222222222222222222222222222222"

	t.Run("add and get tags", func(t *testing.T) {
		require.NoError(t, AddShaTag(sha1, "nature"))
		require.NoError(t, AddShaTag(sha1, "wallpaper"))
		// 重复添加幂等
		require.NoError(t, AddShaTag(sha1, "nature"))

		tags, err := GetShaTags(sha1)
		require.NoError(t, err)
		assert.Equal(t, []string{"nature", "wallpaper"}, tags)
	})

	t.Run("get shas by tag", func(t *testing.T) {
		require.NoError(t, AddShaTag(sha2, "nature"))

		shas, err := GetShasByTag("nature")
		require.NoError(t, err)
		assert.Len(t, shas, 2)
		assert.Contains(t, shas, sha1)
		assert.Contains(t, shas, sha2)
	})

	t.Run("remove tag", func(t *testing.T) {
		require.NoError(t, RemoveShaTag(sha1, "nature"))
		tags, err := GetShaTags(sha1)
		require.NoError(t, err)
		assert.Equal(t, []string{"wallpaper"}, tags)
	})

	t.Run("empty sha or tag rejected", func(t *testing.T) {
		assert.Error(t, AddShaTag("", "tag"))
		assert.Error(t, AddShaTag(sha1, ""))
	})
}

func TestSearchFileIndexWithTag(t *testing.T) {
	// 发现背景：Issue #91 复用 SearchFileIndex 检索管线支持 tag 过滤
	dir := t.TempDir()
	require.NoError(t, InitDB(filepath.Join(dir, "test.db")))
	t.Cleanup(func() { _ = CloseDB() })

	shaA := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	shaB := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	_, err := UpsertFileIndex(shaA, "/path/photo1.jpg", "photo1.jpg", 1024, false)
	require.NoError(t, err)
	_, err = UpsertFileIndex(shaB, "/path/doc1.pdf", "doc1.pdf", 2048, false)
	require.NoError(t, err)

	require.NoError(t, AddShaTag(shaA, "photos"))
	require.NoError(t, AddShaTag(shaB, "docs"))

	// 仅按 tag 搜
	res, total, err := SearchFileIndex(SearchQuery{Tag: "photos"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, res, 1)
	assert.Equal(t, shaA, res[0].Hash)

	// 组合 Q 与 Tag
	res, total, err = SearchFileIndex(SearchQuery{Q: "photo", Tag: "photos"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, res, 1)

	// Q 匹配但 Tag 不匹配
	res, total, err = SearchFileIndex(SearchQuery{Q: "photo", Tag: "docs"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, res)
}
