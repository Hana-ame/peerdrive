package service

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"peerdrive/internal/model"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
)

func TestDownloader_LocalFile(t *testing.T) {
	repository.InitDB(filepath.Join(t.TempDir(), "test.db"))
	dir := t.TempDir()
	pMgr := provider.NewManager(dir)

	data := []byte("downloader test content")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Write to content-addressed storage
	subDir := filepath.Join(dir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	relPath := filepath.Join(hashStr[:2], hashStr)
	os.MkdirAll(filepath.Dir(filepath.Join(dir, relPath)), 0755)
	os.WriteFile(filepath.Join(dir, relPath), data, 0644)

	// Register in DB
	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: "test.txt"})
	repository.InsertFileProvider(hashStr, "local", relPath)

	d := NewDownloader(pMgr, nil, dir)
	reader, fn, _, err := d.GetFileStream(hashStr)
	if err != nil {
		t.Fatalf("GetFileStream failed: %v", err)
	}
	defer reader.Close()
	if fn == "" {
		t.Error("expected non-empty filename")
	}
}

func TestDownloader_NotFound(t *testing.T) {
	dir := t.TempDir()
	pMgr := provider.NewManager(dir)
	d := NewDownloader(pMgr, nil, dir)

	_, _, _, err := d.GetFileStream("0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestDownloader_EmptyHash(t *testing.T) {
	dir := t.TempDir()
	pMgr := provider.NewManager(dir)
	d := NewDownloader(pMgr, nil, dir)

	_, _, _, err := d.GetFileStream("")
	if err == nil {
		t.Error("expected error for empty hash")
	}
}
