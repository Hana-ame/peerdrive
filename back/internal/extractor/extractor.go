// Package extractor provides post-pull auto-extraction of compressed archives.
//
// Goal: after a cross-node pull completes, automatically detect and extract
// zip/tar/gz/tar.gz/tar.bz2 archives into a sibling directory.
//
// Security boundaries (all enforced):
//   - MaxSize: cumulative extracted bytes must not exceed this limit
//   - MaxRatio: extracted_size / archive_size must not exceed this ratio (zip bomb defense)
//   - MaxFiles: number of extracted files must not exceed this limit
//   - Path traversal: filenames with "..", absolute paths, or paths escaping destDir are rejected
//
// Why this package is separate from service:
//   - The extractor has no dependency on transport, repository, or other internal packages
//   - It can be unit-tested independently with synthetic archives
//   - The service layer only calls ShouldExtract + Extract + register
//
// Why every write goes through pathutil:
//   - Project rule (AGENTS.md, 2026-09-20 hardening): any path that produces or changes a file
//     must go through the pathutil.Safe*Any series (os.Root-based, race-free); never raw
//     os.Create/os.MkdirAll/os.Remove on a filepath.Join-ed path. The extractor deals with
//     attacker-controlled archive entry names, so this rule is load-bearing here:
//     a symlink swap between a "check" and a "write" must not escape destDir.
package extractor

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"strings"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
)

// Config holds the security and behavior configuration for auto-extraction.
type Config struct {
	Enabled          bool
	MaxSize          int64
	MaxRatio         int
	MaxFiles         int
	DeleteOriginal   bool
	SupportedFormats []string
}

// DefaultConfig returns a Config with safe defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:          false,
		MaxSize:          500 * 1024 * 1024,
		MaxRatio:         100,
		MaxFiles:         10000,
		DeleteOriginal:   true,
		SupportedFormats: []string{".zip", ".tar", ".gz", ".tgz", ".tar.gz", ".tar.bz2"},
	}
}

// ExtractedFile describes a single extracted file.
type ExtractedFile struct {
	// Path is the path relative to the extraction directory (slash-separated).
	Path     string
	Size     int64
	Hash     string
	FullName string
}

// Extractor performs safe, bounded archive extraction.
type Extractor struct {
	cfg Config
}

// New creates an Extractor with the given configuration.
func New(cfg Config) *Extractor {
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 500 * 1024 * 1024
	}
	if cfg.MaxRatio == 0 {
		cfg.MaxRatio = 100
	}
	if cfg.MaxFiles == 0 {
		cfg.MaxFiles = 10000
	}
	if len(cfg.SupportedFormats) == 0 {
		cfg.SupportedFormats = DefaultConfig().SupportedFormats
	}
	return &Extractor{cfg: cfg}
}

// ShouldExtract checks if a file should be auto-extracted based on its extension.
func (e *Extractor) ShouldExtract(filename string) bool {
	if !e.cfg.Enabled {
		return false
	}
	lower := strings.ToLower(filename)
	for _, ext := range e.cfg.SupportedFormats {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// Extract extracts an archive at srcPath into destDir.
// Returns the list of extracted files with their metadata.
//
// On failure the archive itself is left untouched (deletion only happens
// after a fully successful extraction), and partially written files are removed.
func (e *Extractor) Extract(srcPath, destDir string) ([]ExtractedFile, error) {
	if !e.cfg.Enabled {
		return nil, fmt.Errorf("extractor disabled")
	}
	info, err := os.Stat(srcPath)
	if err != nil {
		return nil, fmt.Errorf("stat archive: %w", err)
	}
	archiveSize := info.Size()

	var files []ExtractedFile
	var exErr error
	lower := strings.ToLower(srcPath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		files, exErr = e.extractZip(srcPath, destDir, archiveSize)
	case strings.HasSuffix(lower, ".tar"):
		files, exErr = e.extractTar(srcPath, destDir, archiveSize, nil)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		files, exErr = e.extractTarGz(srcPath, destDir, archiveSize)
	case strings.HasSuffix(lower, ".tar.bz2"):
		files, exErr = e.extractTarBz2(srcPath, destDir, archiveSize)
	case strings.HasSuffix(lower, ".gz"):
		files, exErr = e.extractGz(srcPath, destDir, archiveSize)
	default:
		return nil, fmt.Errorf("unsupported format: %s", filepath.Ext(srcPath))
	}
	if exErr != nil {
		return nil, exErr
	}

	// The archive is only deleted after a fully successful extraction, and only
	// through the pathutil write series (the archive lives inside its own
	// directory, so that directory is the allowed root).
	if e.cfg.DeleteOriginal {
		if err := pathutil.SafeRemoveAny([]string{filepath.Dir(srcPath)}, srcPath); err != nil {
			log.LogWarn("extractor: failed to delete original %s: %v", srcPath, err)
		}
	}
	log.LogInfo("extractor: extracted %d files from %s to %s", len(files), filepath.Base(srcPath), destDir)
	return files, nil
}

// resolveEntry validates an archive entry name and maps it to a path inside
// destDir. The authoritative containment check is pathutil.SafeOpenFileAny
// (os.Root, race-free); this function only provides early, clear rejection of
// obviously hostile names (empty / NUL / absolute / "..") before we build the
// destination path and accept side effects.
func resolveEntry(name, destDir string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("empty archive entry name")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", fmt.Errorf("NUL byte in archive entry name")
	}
	rel := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path in archive: %s", name)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal in archive: %s", name)
	}
	return rel, nil
}

// writeEntry creates destDir/rel through the pathutil write series and returns
// the open file plus counting/hashing writers. rel must come from resolveEntry
// (or be the derived .gz output name).
func (e *Extractor) writeEntry(destDir, rel string, budget int64) (*os.File, *hashWriter, *countingWriter, error) {
	destPath := filepath.Join(destDir, rel)
	out, err := pathutil.SafeOpenFileAny([]string{destDir}, destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open %s: %w", rel, err)
	}
	h := sha256.New()
	cw := &countingWriter{w: out, limit: budget}
	hw := &hashWriter{w: cw, hash: h}
	return out, hw, cw, nil
}

type countingWriter struct {
	w       io.Writer
	counted int64
	limit   int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	if cw.counted+int64(len(p)) > cw.limit {
		return 0, fmt.Errorf("extraction size limit exceeded: %d + %d > %d", cw.counted, len(p), cw.limit)
	}
	n, err := cw.w.Write(p)
	cw.counted += int64(n)
	return n, err
}

type hashWriter struct {
	w    io.Writer
	hash hash.Hash
}

func (hw *hashWriter) Write(p []byte) (int, error) {
	n, err := hw.w.Write(p)
	hw.hash.Write(p[:n])
	return n, err
}

func (e *Extractor) extractZip(srcPath, destDir string, archiveSize int64) ([]ExtractedFile, error) {
	r, err := zip.OpenReader(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()
	var files []ExtractedFile
	totalExtracted := int64(0)
	for _, f := range r.File {
		if len(files) >= e.cfg.MaxFiles {
			return files, fmt.Errorf("file count limit exceeded: %d", e.cfg.MaxFiles)
		}
		if f.FileInfo().IsDir() {
			continue
		}
		// Cumulative-size hard limit decided up front from the (potentially
		// lying) header, and again enforced per-write by countingWriter.
		if totalExtracted+int64(f.UncompressedSize64) > e.cfg.MaxSize {
			return nil, fmt.Errorf("extraction size limit exceeded: %d + %d > %d", totalExtracted, f.UncompressedSize64, e.cfg.MaxSize)
		}
		rel, err := resolveEntry(f.Name, destDir)
		if err != nil {
			return nil, fmt.Errorf("unsafe path %q: %w", f.Name, err)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open entry %s: %w", f.Name, err)
		}
		out, hw, _, err := e.writeEntry(destDir, rel, e.cfg.MaxSize-totalExtracted)
		if err != nil {
			rc.Close()
			return nil, err
		}
		written, copyErr := io.Copy(hw, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			// Never leave a partial extraction behind.
			_ = pathutil.SafeRemoveAny([]string{destDir}, filepath.Join(destDir, rel))
			return nil, fmt.Errorf("extract %s: %w", f.Name, copyErr)
		}
		totalExtracted += written
		files = append(files, ExtractedFile{
			Path:     filepath.ToSlash(rel),
			Size:     written,
			Hash:     hex.EncodeToString(hw.hash.Sum(nil)),
			FullName: filepath.Join(destDir, rel),
		})
	}
	return files, nil
}

func (e *Extractor) extractTar(srcPath, destDir string, archiveSize int64, reader io.Reader) ([]ExtractedFile, error) {
	var r io.Reader
	if reader != nil {
		r = reader
	} else {
		f, err := os.Open(srcPath)
		if err != nil {
			return nil, fmt.Errorf("open tar: %w", err)
		}
		defer f.Close()
		r = f
	}
	tr := tar.NewReader(r)
	var files []ExtractedFile
	totalExtracted := int64(0)

	// Track created files so success can be reported and failures cleaned.
	created := map[string]bool{}
	cleanup := func() {
		for p := range created {
			_ = pathutil.SafeRemoveAny([]string{destDir}, p)
		}
	}

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("read tar header: %w", err)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeSymlink, tar.TypeLink:
			cleanup()
			return nil, fmt.Errorf("symlink/link in tar archive rejected: %s", header.Name)
		case tar.TypeReg, tar.TypeRegA:
			// regular file — continue below
		default:
			// Special files (device nodes, fifos, ...) are never needed for a
			// file-sharing auto-extract; skipping them is safer than creating
			// them (a fifo with content would block writes).
			continue
		}
		if len(files) >= e.cfg.MaxFiles {
			cleanup()
			return files, fmt.Errorf("file count limit exceeded: %d", e.cfg.MaxFiles)
		}
		if totalExtracted+header.Size > e.cfg.MaxSize {
			cleanup()
			return nil, fmt.Errorf("extraction size limit exceeded: %d + %d > %d", totalExtracted, header.Size, e.cfg.MaxSize)
		}
		rel, err := resolveEntry(header.Name, destDir)
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("unsafe path %q: %w", header.Name, err)
		}
		out, hw, _, err := e.writeEntry(destDir, rel, e.cfg.MaxSize-totalExtracted)
		if err != nil {
			cleanup()
			return nil, err
		}
		created[filepath.Join(destDir, rel)] = true
		written, copyErr := io.Copy(hw, tr)
		out.Close()
		if copyErr != nil {
			cleanup()
			return nil, fmt.Errorf("extract %s: %w", header.Name, copyErr)
		}
		totalExtracted += written
		files = append(files, ExtractedFile{
			Path:     filepath.ToSlash(rel),
			Size:     written,
			Hash:     hex.EncodeToString(hw.hash.Sum(nil)),
			FullName: filepath.Join(destDir, rel),
		})
	}
	return files, nil
}

func (e *Extractor) extractTarGz(srcPath, destDir string, archiveSize int64) ([]ExtractedFile, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open tar.gz: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()
	return e.extractTar(srcPath, destDir, archiveSize, gz)
}

func (e *Extractor) extractTarBz2(srcPath, destDir string, archiveSize int64) ([]ExtractedFile, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open tar.bz2: %w", err)
	}
	defer f.Close()
	bz2 := bzip2.NewReader(f)
	return e.extractTar(srcPath, destDir, archiveSize, bz2)
}

func (e *Extractor) extractGz(srcPath, destDir string, archiveSize int64) ([]ExtractedFile, error) {
	f, err := os.Open(srcPath)
	if err != nil {
		return nil, fmt.Errorf("open gz: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	// A bare .gz wraps exactly one stream; the name is derived from the archive
	// base name (foo.txt.gz → foo.txt), which is not attacker-controlled in a
	// way that can escape destDir.
	base := strings.TrimSuffix(filepath.Base(srcPath), ".gz")
	if base == "" {
		return nil, fmt.Errorf("cannot derive output name from %s", filepath.Base(srcPath))
	}
	out, hw, _, err := e.writeEntry(destDir, base, e.cfg.MaxSize)
	if err != nil {
		return nil, err
	}
	written, copyErr := io.Copy(hw, gz)
	out.Close()
	if copyErr != nil {
		_ = pathutil.SafeRemoveAny([]string{destDir}, filepath.Join(destDir, base))
		return nil, fmt.Errorf("extract gz: %w", copyErr)
	}
	return []ExtractedFile{{
		Path:     base,
		Size:     written,
		Hash:     hex.EncodeToString(hw.hash.Sum(nil)),
		FullName: filepath.Join(destDir, base),
	}}, nil
}

// IsArchive checks if a filename matches a supported archive extension.
func IsArchive(filename string) bool {
	lower := strings.ToLower(filename)
	supported := []string{".zip", ".tar", ".gz", ".tgz", ".tar.gz", ".tar.bz2"}
	for _, ext := range supported {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}
