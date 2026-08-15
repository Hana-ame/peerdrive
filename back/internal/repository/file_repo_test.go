package repository

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

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
