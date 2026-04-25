package service

import (
	"os"
	"strings"
	"testing"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
	"github.com/stretchr/testify/assert"
)

func TestSyncService_PathTraversal(t *testing.T) {
	// Setup
	tmpDir, _ := os.MkdirTemp("", "peerdrive_test")
	defer os.RemoveAll(tmpDir)

	repository.InitDB(":memory:") // Use in-memory DB for tests
	syncRepo := repository.NewSyncRepository()
	
	// Mock downloader
	downloader := &Downloader{
		storageDir: tmpDir,
	}
	
	svc := NewSyncService(syncRepo, downloader)

	tests := []struct {
		name     string
		localPath string
		wantErr  bool
	}{
		{"ValidPath", tmpDir, false},
		{"TraversalAttempt", tmpDir + "/../../etc", true},
		{"ContainDotDot", "my..data", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := model.SaveLocalRequest{
				CollectionHash: "somehash",
				LocalPath:      tt.localPath,
			}
			err := svc.SaveToDisk(req)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "path traversal")
			} else {
				// It will fail later because GetAnonCollectionByHash will fail (empty DB), 
				// but the path check should pass first.
				if err != nil && strings.Contains(err.Error(), "path traversal") {
					t.Errorf("Unexpected path traversal error: %v", err)
				}
			}
		})
	}
}

func TestSyncService_Filtering(t *testing.T) {
	svc := NewSyncService(nil, nil)
	entries := []model.AnonCollectionEntry{
		{Path: "main.go", Hash: "h1"},
		{Path: "utils.go", Hash: "h2"},
		{Path: "README.md", Hash: "h3"},
		{Path: "docs/intro.md", Hash: "h4"},
	}

	tests := []struct {
		name     string
		include  []string
		exclude  []string
		expected int
	}{
		{"All", []string{}, []string{}, 4},
		{"ExcludeOne", []string{}, []string{"README.md"}, 3},
		{"IncludeOne", []string{"main.go"}, []string{}, 1},
		{"IncludeAndExclude", []string{"*.go"}, []string{"utils.go"}, 1}, // main.go only
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := svc.filterFiles(entries, tt.include, tt.exclude)
			assert.Equal(t, tt.expected, len(res))
		})
	}
}
