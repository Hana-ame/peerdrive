package source

// local.go: LocalSource -- local disk source (file_index mapping preferred + content-addressed storage fallback).
// Semantics are fully consistent with transport.serveFile's path decision logic (the same logic converged in one place):
//   - file_index hit and path is within allowed roots -> read the mapped path
//   - otherwise -> content-addressed storage storageDir/<hash[:2]>/<hash>
// Local file writes already complete sha256 verification (upload Complete), so Open does not
// verify again (consistent with serveFile behavior); Available = the storage directory is readable.

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/transport"
)

// LocalSource is the local disk file source.
type LocalSource struct {
	name       string
	storageDir string
	fileIndex  *transport.FileIndexService

	mu       sync.RWMutex
	priority int
}

// NewLocalSource creates a local source. name defaults to "local"; storageDir is the content-addressed storage root;
// fileIndex may be nil (content-addressed only).
func NewLocalSource(storageDir string, fileIndex *transport.FileIndexService) *LocalSource {
	return &LocalSource{
		name:       "local",
		storageDir: storageDir,
		fileIndex:  fileIndex,
	}
}

func (s *LocalSource) Name() string { return s.name }
func (s *LocalSource) Type() string { return "local" }

// Capabilities local disk natively supports streaming ranges (os.File Seek/ReadAt).
func (s *LocalSource) Capabilities() Capability { return CapStream }

func (s *LocalSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *LocalSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available checks whether the storage directory exists and is readable.
func (s *LocalSource) Available(ctx context.Context) bool {
	f, err := os.Open(s.storageDir)
	if err != nil {
		return false
	}
	f.Close()
	return true
}

// resolvePath replicates serveFile's path decision: file_index first (path must be **readable**,
// otherwise fall back to CAS -- historical dirty data/malicious registrations do not return
// files outside the root, H2).
//
// Note: uses IsPathReadable instead of IsPathAllowed. The latter is the registration/write
// boundary and only recognizes the download directory. When the operator places shared directories
// outside the download directory, using it would misclassify a **legitimate** file as unauthorized,
// then fall back to a non-existent CAS copy -> the peer gets "read failed".
func (s *LocalSource) resolvePath(hash string) string {
	path := filepath.Join(s.storageDir, hash[:2], hash)
	if s.fileIndex != nil {
		if fi, err := s.fileIndex.Info(hash); err == nil && fi.Path != "" {
			if s.fileIndex.IsPathReadable(fi.Path) {
				return fi.Path
			}
			log.LogWarn("source/local: index path not readable, serving content-addressed: %s", fi.Path)
		}
	}
	return path
}

// open safely opens the result of resolvePath.
//
// Does not use os.Open: the index path returned by resolvePath was **validated at registration
// time**, but it may have been replaced with a symlink by now. Using pathutil.SafeOpen (os.Root)
// lets the kernel re-evaluate at the moment of opening. Shared root paths use fileIndex.OpenReadable,
// CAS copies are anchored to storageDir.
func (s *LocalSource) open(hash string) (*os.File, error) {
	p := s.resolvePath(hash)
	if s.fileIndex != nil && s.fileIndex.IsPathReadable(p) {
		if f, err := s.fileIndex.OpenReadable(p); err == nil {
			return f, nil
		}
	}
	if s.storageDir == "" {
		return os.Open(p) // storageDir not configured (pure test assembly), keep old behavior
	}
	return pathutil.SafeOpen(s.storageDir, p)
}

// Open streams open: offset<0 -> 0; size<0 -> to end of file. Ranges use os.File.Seek
// (local files have no network cost, so just give the raw file handle).
func (s *LocalSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	f, err := s.open(hash)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	off := offset
	if off < 0 {
		off = 0
	}
	if off > st.Size() {
		off = st.Size()
	}
	length := size
	if length < 0 || off+length > st.Size() {
		length = st.Size() - off
	}
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		f.Close()
		return nil, err
	}
	// Length-limited read: io.LimitReader truncates to length (prevents out-of-bounds reads --
	// offset/size are caller input, handled defensively)
	return &limitedReadCloser{r: io.LimitReader(f, length), c: f}, nil
}

// Fetch does a full fetch (CapStream already covers this; defensive implementation, Manager will not call it).
func (s *LocalSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}

// Info metadata: prefer file_index (has name/path), otherwise CAS file stat.
func (s *LocalSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if s.fileIndex != nil {
		if fi, err := s.fileIndex.Info(hash); err == nil && fi.Path != "" {
			return &FileMeta{Hash: hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
		}
	}
	f, err := s.open(hash)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &FileMeta{Hash: hash, Size: st.Size()}, nil
}

// AddLocalFile adds an existing local file to the local source (Source control plane).
// Underlying implementation reuses FileIndexService.Create; security boundaries, dedup, and index
// semantics are consistent with the create verb.
func (s *LocalSource) AddLocalFile(path string) (*FileMeta, error) {
	if s.fileIndex == nil {
		return nil, ErrControlUnsupported
	}
	fi, err := s.fileIndex.Create(path)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/local: add local file path=%s hash=%s size=%d", path, fi.Hash, fi.Size)
	return &FileMeta{Hash: fi.Hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
}

// WriteFile writes a file directly to the local source (Source control plane).
// Underlying implementation reuses FileIndexService.WriteFile: streams into the index directory, then registers.
func (s *LocalSource) WriteFile(name string, r io.Reader) (*FileMeta, error) {
	if s.fileIndex == nil {
		return nil, ErrControlUnsupported
	}
	fi, err := s.fileIndex.WriteFile(name, r)
	if err != nil {
		return nil, err
	}
	log.LogInfo("source/local: write file name=%s hash=%s size=%d", name, fi.Hash, fi.Size)
	return &FileMeta{Hash: fi.Hash, Size: fi.Size, Name: fi.Name, Path: fi.Path}, nil
}

// limitedReadCloser limits read length and closes the underlying file.
type limitedReadCloser struct {
	r io.Reader
	c io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.c.Close() }
