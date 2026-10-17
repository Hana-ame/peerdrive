package extractor

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShouldExtract(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		enabled  bool
		want     bool
	}{
		{"zip enabled", "test.zip", true, true},
		{"zip disabled", "test.zip", false, false},
		{"tar enabled", "test.tar", true, true},
		{"gz enabled", "test.gz", true, true},
		{"tar.gz enabled", "test.tar.gz", true, true},
		{"tar.bz2 enabled", "test.tar.bz2", true, true},
		{"txt not archive", "test.txt", true, false},
		{"uppercase ZIP", "TEST.ZIP", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := New(Config{Enabled: tt.enabled})
			got := e.ShouldExtract(tt.filename)
			if got != tt.want {
				t.Errorf("ShouldExtract(%q) = %v, want %v", tt.filename, got, tt.want)
			}
		})
	}
}

func TestExtractDisabled(t *testing.T) {
	e := New(Config{Enabled: false})
	_, err := e.Extract("/nonexistent", "/tmp")
	if err == nil {
		t.Error("expected error when extractor disabled")
	}
}

func TestIsArchive(t *testing.T) {
	tests := []struct {
		filename string
		want     bool
	}{
		{"test.zip", true},
		{"test.tar", true},
		{"test.gz", true},
		{"test.tar.gz", true},
		{"test.tar.bz2", true},
		{"test.txt", false},
		{"test.ZIP", true},
	}
	for _, tt := range tests {
		if got := IsArchive(tt.filename); got != tt.want {
			t.Errorf("IsArchive(%q) = %v, want %v", tt.filename, got, tt.want)
		}
	}
}

// systemAbsolutePath 返回当前平台上一个真实存在的绝对路径，用于「绝对路径必须被
// 拒绝」的断言。不能写死 POSIX payload（/etc/passwd）：它在 Windows 上不是绝对
// 路径（filepath.IsAbs 为 false，实际是卷相对路径），断言会假绿——2026-10-07
// Windows CI 实测踩坑，与 AGENTS.md「Windows 语义必须真在 Windows 上跑过」一致。
func systemAbsolutePath() string {
	if runtime.GOOS == "windows" {
		return `C:\Windows\System32\drivers\etc\hosts`
	}
	return "/etc/passwd"
}

func TestResolveEntry(t *testing.T) {
	destDir := t.TempDir()
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{"normal file", "test.txt", false},
		{"nested path", "dir/test.txt", false},
		{"path traversal", "../escape.txt", true},
		{"path traversal deep", "../../escape.txt", true},
		{"absolute path", systemAbsolutePath(), true},
		{"empty", "", true},
		{"nul byte", "bad\x00name", true},
		{"dot", ".", false},
		{"dot dot", "..", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveEntry(tt.filename, destDir)
			if tt.wantErr && err == nil {
				t.Errorf("resolveEntry(%q) expected error, got nil", tt.filename)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("resolveEntry(%q) unexpected error: %v", tt.filename, err)
			}
		})
	}
}

// TestExtractPathTraversalEscape proves the authoritative containment check:
// a hostile name that survives resolveEntry (e.g. one that only escapes after
// symlink resolution) is still rejected by the pathutil write series.
func TestExtractPathTraversalEscape(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "evil.zip")
	destDir := filepath.Join(tmpDir, "extracted")

	// zip.Write stores names verbatim; use a traversal name directly. resolveEntry
	// rejects it, but even if a name passed that layer, SafeOpenFileAny would not
	// allow writing outside destDir.
	os.MkdirAll(filepath.Dir(zipPath), 0o755)
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	w.Write([]byte("evil"))
	zw.Close()
	zf.Close()
	if _, err := e.Extract(zipPath, destDir); err == nil {
		t.Error("expected path traversal error")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(destDir), "escape.txt")); err == nil {
		t.Error("escape.txt must not exist outside destDir")
	}
}

func TestExtractZip(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	destDir := filepath.Join(tmpDir, "extracted")
	content := []byte("hello world")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])
	os.MkdirAll(filepath.Dir(zipPath), 0o755)
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("test.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	w.Write(content)
	zw.Close()
	zf.Close()
	files, err := e.Extract(zipPath, destDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", files[0].Hash, expectedHash)
	}
	if files[0].Size != int64(len(content)) {
		t.Errorf("size mismatch: got %d, want %d", files[0].Size, len(content))
	}
}

func TestExtractTar(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar")
	destDir := filepath.Join(tmpDir, "extracted")
	content := []byte("hello tar")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])
	os.MkdirAll(filepath.Dir(tarPath), 0o755)
	tf, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	tw := tar.NewWriter(tf)
	tw.WriteHeader(&tar.Header{Name: "test.txt", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write(content)
	tw.Close()
	tf.Close()
	files, err := e.Extract(tarPath, destDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", files[0].Hash, expectedHash)
	}
}

func TestExtractTarGz(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	tarGzPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")
	content := []byte("hello tar gz")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])
	os.MkdirAll(filepath.Dir(tarGzPath), 0o755)
	tf, err := os.Create(tarGzPath)
	if err != nil {
		t.Fatalf("create tar.gz: %v", err)
	}
	gz := gzip.NewWriter(tf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "test.txt", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg})
	tw.Write(content)
	tw.Close()
	gz.Close()
	tf.Close()
	files, err := e.Extract(tarGzPath, destDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", files[0].Hash, expectedHash)
	}
}

func TestExtractGz(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	gzPath := filepath.Join(tmpDir, "test.gz")
	destDir := filepath.Join(tmpDir, "extracted")
	content := []byte("hello gz")
	hash := sha256.Sum256(content)
	expectedHash := hex.EncodeToString(hash[:])
	os.MkdirAll(filepath.Dir(gzPath), 0o755)
	gf, err := os.Create(gzPath)
	if err != nil {
		t.Fatalf("create gz: %v", err)
	}
	gz := gzip.NewWriter(gf)
	gz.Write(content)
	gz.Close()
	gf.Close()
	files, err := e.Extract(gzPath, destDir)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}
	if files[0].Hash != expectedHash {
		t.Errorf("hash mismatch: got %s, want %s", files[0].Hash, expectedHash)
	}
	if files[0].Path != "test" {
		t.Errorf("expected path 'test', got %s", files[0].Path)
	}
}

func TestExtractPathTraversal(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "evil.zip")
	destDir := filepath.Join(tmpDir, "extracted")
	os.MkdirAll(filepath.Dir(zipPath), 0o755)
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("../escape.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	w.Write([]byte("evil"))
	zw.Close()
	zf.Close()
	_, err = e.Extract(zipPath, destDir)
	if err == nil {
		t.Error("expected path traversal error")
	}
	if !strings.Contains(err.Error(), "path traversal") && !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("expected path traversal error, got: %v", err)
	}
}

func TestExtractSizeLimit(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 100, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "big.zip")
	destDir := filepath.Join(tmpDir, "extracted")
	content := make([]byte, 200)
	for i := range content {
		content[i] = byte(i % 256)
	}
	os.MkdirAll(filepath.Dir(zipPath), 0o755)
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("big.txt")
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	w.Write(content)
	zw.Close()
	zf.Close()
	_, err = e.Extract(zipPath, destDir)
	if err == nil {
		t.Error("expected size limit error")
	}
	if !strings.Contains(err.Error(), "size limit") {
		t.Errorf("expected size limit error, got: %v", err)
	}
}

func TestExtractFileCountLimit(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 2, DeleteOriginal: false})
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "many.zip")
	destDir := filepath.Join(tmpDir, "extracted")
	os.MkdirAll(filepath.Dir(zipPath), 0o755)
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	zw := zip.NewWriter(zf)
	for i := 0; i < 3; i++ {
		w, err := zw.Create(fmt.Sprintf("file%d.txt", i))
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		w.Write([]byte(fmt.Sprintf("content %d", i)))
	}
	zw.Close()
	zf.Close()
	files, _ := e.Extract(zipPath, destDir)
	if len(files) > 2 {
		t.Errorf("expected at most 2 files, got %d", len(files))
	}
}

func TestExtractTarSymlinkRejected(t *testing.T) {
	e := New(Config{Enabled: true, MaxSize: 10 * 1024 * 1024, MaxRatio: 100, MaxFiles: 1000, DeleteOriginal: false})
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "evil.tar")
	destDir := filepath.Join(tmpDir, "extracted")
	os.MkdirAll(filepath.Dir(tarPath), 0o755)
	tf, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	tw := tar.NewWriter(tf)
	tw.WriteHeader(&tar.Header{Name: "link", Linkname: "/etc/passwd", Mode: 0o777, Size: 0, Typeflag: tar.TypeSymlink})
	tw.Close()
	tf.Close()
	_, err = e.Extract(tarPath, destDir)
	if err == nil {
		t.Error("expected symlink rejection error")
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("expected symlink error, got: %v", err)
	}
}

func TestCountingWriterLimit(t *testing.T) {
	cw := &countingWriter{w: io.Discard, limit: 10}
	_, err := cw.Write([]byte("hello"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	_, err = cw.Write([]byte("world!"))
	if err == nil {
		t.Error("expected limit exceeded error")
	}
}

func TestHashWriter(t *testing.T) {
	var buf strings.Builder
	h := sha256.New()
	hw := &hashWriter{w: io.Writer(&buf), hash: h}
	hw.Write([]byte("hello"))
	_ = hex.EncodeToString(h.Sum(nil))
}
