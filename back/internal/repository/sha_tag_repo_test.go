package repository

import (
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
