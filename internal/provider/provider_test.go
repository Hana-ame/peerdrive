package provider

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalProvider_GetReader_Exists(t *testing.T) {
	dir := t.TempDir()
	testFile := filepath.Join(dir, "hello.txt")
	content := "hello world"
	os.WriteFile(testFile, []byte(content), 0644)

	lp := &LocalProvider{BaseDir: dir}
	reader, err := lp.GetReader("hello.txt")
	if err != nil {
		t.Fatalf("GetReader failed: %v", err)
	}
	defer reader.Close()

	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected %q, got %q", content, string(data))
	}
}

func TestLocalProvider_GetReader_NotFound(t *testing.T) {
	dir := t.TempDir()
	lp := &LocalProvider{BaseDir: dir}
	_, err := lp.GetReader("nonexistent.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLocalProvider_GetFilenameHint(t *testing.T) {
	dir := t.TempDir()
	lp := &LocalProvider{BaseDir: dir}
	// originalFilename takes priority
	hint := lp.GetFilenameHint("some/path/file.txt", "original.txt")
	if hint != "original.txt" {
		t.Errorf("expected original.txt, got %q", hint)
	}
	// empty originalFilename → uses path basename
	hint2 := lp.GetFilenameHint("some/path/nopath", "")
	if hint2 != "nopath" {
		t.Errorf("expected nopath, got %q", hint2)
	}
}

func TestManager_RegisterAndGet(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.bin"), []byte("binary"), 0644)

	mgr := NewManager(dir)
	if mgr == nil {
		t.Fatal("NewManager returned nil")
	}

	t.Run("local provider registered", func(t *testing.T) {
		r, _, err := mgr.GetReader("local", "test.bin")
		if err != nil {
			t.Fatalf("local provider should be registered: %v", err)
		}
		defer r.Close()
		data, _ := io.ReadAll(r)
		if string(data) != "binary" {
			t.Errorf("wrong content: %s", data)
		}
	})

	t.Run("http provider registered", func(t *testing.T) {
		_, _, err := mgr.GetReader("http", "https://example.com/file.txt")
		// HTTP will fail in test, but provider should exist (no "unknown provider" error)
		if err != nil && strings.Contains(err.Error(), "unknown provider type") {
			t.Error("http provider not found")
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		_, _, err := mgr.GetReader("unknown", "file.txt")
		if err == nil {
			t.Error("expected error for unknown provider")
		}
	})
}
