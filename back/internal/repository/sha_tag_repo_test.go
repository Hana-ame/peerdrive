package repository

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestShaTags_CRUDAndSearch(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sha_tags.db")

	err := InitDB(dbPath)
	require.NoError(t, err)
	defer CloseDB()

	sha1 := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	sha2 := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// 1. Initial tags should be empty
	tags, err := GetShaTags(sha1)
	require.NoError(t, err)
	assert.Empty(t, tags)

	// 2. Add tags
	err = AddShaTag(sha1, "work")
	require.NoError(t, err)
	err = AddShaTag(sha1, "important")
	require.NoError(t, err)

	tags, err = GetShaTags(sha1)
	require.NoError(t, err)
	assert.Equal(t, []string{"important", "work"}, tags)

	// 3. Remove tag
	err = RemoveShaTag(sha1, "work")
	require.NoError(t, err)

	tags, err = GetShaTags(sha1)
	require.NoError(t, err)
	assert.Equal(t, []string{"important"}, tags)

	// 4. SetShaTags replacement
	err = SetShaTags(sha1, []string{"media", "video", "personal"})
	require.NoError(t, err)

	tags, err = GetShaTags(sha1)
	require.NoError(t, err)
	assert.Equal(t, []string{"media", "personal", "video"}, tags)

	// 5. Add same tag to sha2 and ListShasByTag
	err = AddShaTag(sha2, "personal")
	require.NoError(t, err)

	shas, err := ListShasByTag("personal")
	require.NoError(t, err)
	assert.Len(t, shas, 2)
	assert.Contains(t, shas, sha1)
	assert.Contains(t, shas, sha2)

	// 6. SearchFilesByTag with file_index join
	_, err = UpsertFileIndex(sha1, "/path/to/vid1.mp4", "vid1.mp4", 1024, false)
	require.NoError(t, err)
	_, err = UpsertFileIndex(sha2, "/path/to/vid2.mp4", "vid2.mp4", 2048, false)
	require.NoError(t, err)

	files, err := SearchFilesByTag("personal")
	require.NoError(t, err)
	assert.Len(t, files, 2)
	assert.Equal(t, "vid2.mp4", files[0].Name) // latest seq first
	assert.Equal(t, "vid1.mp4", files[1].Name)
}

func TestSearchCollectionsWithFilters(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_collection_tags.db")

	err := InitDB(dbPath)
	require.NoError(t, err)
	defer CloseDB()

	// Create test collections with tags
	_, err = CreateCollectionWithTags("alice", "photos", "public", []string{"media", "image"})
	require.NoError(t, err)

	_, err = CreateCollectionWithTags("alice", "documents", "public", []string{"work", "pdf"})
	require.NoError(t, err)

	_, err = CreateCollectionWithTags("bob", "vacation_photos", "public", []string{"media", "vacation"})
	require.NoError(t, err)

	_, err = CreateCollectionWithTags("alice", "secret", "private", []string{"secret", "work"})
	require.NoError(t, err)

	// 1. Search by tag "media"
	res, err := SearchCollectionsWithFilters("", []string{"media"}, "public")
	require.NoError(t, err)
	assert.Len(t, res, 2)

	// 2. Search by multiple tags (AND)
	res, err = SearchCollectionsWithFilters("", []string{"media", "image"}, "public")
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, "photos", res[0].CollectionName)

	// 3. Search by query + tag
	res, err = SearchCollectionsWithFilters("vacation", []string{"media"}, "public")
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, "vacation_photos", res[0].CollectionName)

	// 4. Visibility filter: public should not return secret
	res, err = SearchCollectionsWithFilters("", []string{"work"}, "public")
	require.NoError(t, err)
	assert.Len(t, res, 1)
	assert.Equal(t, "documents", res[0].CollectionName)

	// 5. Visibility filter: all includes private
	res, err = SearchCollectionsWithFilters("", []string{"work"}, "all")
	require.NoError(t, err)
	assert.Len(t, res, 2)
}

// TestShaTags_BatchAndSummary 验证批量标签查询与全局聚合统计。
//
// 发现背景：前端网盘列表加载若逐个文件请求 /tags/sha/:sha，会导致严重 N+1 瀑布流；
// 同时标签栏需要全局标签和频次统计以供即时过滤与排序。
// 此测试验证：
//  1. GetBatchShaTags 在单次查询中批量加载多个 SHA 的标签列表；
//  2. 空/空白/重复 SHA 过滤与初始化兜底；
//  3. 超大批次（> 1000 个 SHA）的分块 chunk 机制（防止击穿 SQLite 变量数上限）；
//  4. GetAllTagsWithCounts 聚合统计正确性与排序规则（频次降序、名称升序）。
func TestShaTags_BatchAndSummary(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_sha_batch_tags.db")

	err := InitDB(dbPath)
	require.NoError(t, err)
	defer CloseDB()

	sha1 := "1111111111111111111111111111111111111111111111111111111111111111"
	sha2 := "2222222222222222222222222222222222222222222222222222222222222222"
	sha3 := "3333333333333333333333333333333333333333333333333333333333333333"

	require.NoError(t, AddShaTag(sha1, "work"))
	require.NoError(t, AddShaTag(sha1, "important"))
	require.NoError(t, AddShaTag(sha2, "work"))
	require.NoError(t, AddShaTag(sha2, "media"))

	// 1. 空输入 / 全空白输入边界
	emptyBatch, err := GetBatchShaTags([]string{})
	require.NoError(t, err)
	assert.Empty(t, emptyBatch)

	wsBatch, err := GetBatchShaTags([]string{"  ", "", "\t"})
	require.NoError(t, err)
	assert.Empty(t, wsBatch)

	// 2. 正常多项批量查询，含未打标的 sha3 与重复项
	batchRes, err := GetBatchShaTags([]string{sha1, "  " + sha2 + "  ", sha1, sha3})
	require.NoError(t, err)
	assert.Equal(t, []string{"important", "work"}, batchRes[sha1])
	assert.Equal(t, []string{"media", "work"}, batchRes[sha2])
	assert.Equal(t, []string{}, batchRes[sha3], "未打标的 SHA 应当返回空切片而非 nil")

	// 3. 全局统计
	counts, err := GetAllTagsWithCounts()
	require.NoError(t, err)
	// work: 2 (sha1, sha2)
	// important: 1 (sha1)
	// media: 1 (sha2)
	require.Len(t, counts, 3)
	assert.Equal(t, "work", counts[0].Tag)
	assert.Equal(t, 2, counts[0].Count)
	// 频次相同时按字母序升序: important 在 media 前
	assert.Equal(t, "important", counts[1].Tag)
	assert.Equal(t, 1, counts[1].Count)
	assert.Equal(t, "media", counts[2].Tag)
	assert.Equal(t, 1, counts[2].Count)

	// 4. 超大批次分块测试（1200 个 SHA，验证分批 500 chunk 机制）
	largeShas := make([]string, 1200)
	for i := 0; i < 1200; i++ {
		largeShas[i] = fmt.Sprintf("%064x", i+100)
	}
	largeShas[550] = sha1
	largeShas[1050] = sha2

	largeRes, err := GetBatchShaTags(largeShas)
	require.NoError(t, err)
	assert.Equal(t, []string{"important", "work"}, largeRes[sha1])
	assert.Equal(t, []string{"media", "work"}, largeRes[sha2])
	assert.Equal(t, []string{}, largeRes[largeShas[0]])
}

