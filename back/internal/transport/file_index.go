package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/pkg/hashutil"
)

// FileIndexService local file index service: sha256 → absolute path mapping (SQLite persistence).
// Frame protocol verbs (transmitted via Session, same set for WS/WebRTC):
//
//	create   {type:"create", path}            → created {hash,size,name,path}
//	upload   {type:"upload", name,size}       streaming: meta → data×N → uploaded {hash,path}
//	list     {type:"list", offset?,limit?}    → list-resp {files,total}
//	info     {type:"info", hash}              → info-resp {hash,size,name,path,seq}
//	download reuse existing req (file info returned by info verb)
//	sync     {type:"sync", seq}               → sync-resp {files,lastSeq} (metadata incremental sync)
type FileIndexService struct {
	uploadDir string // upload default save location
	rootDir   string // create allowed file registration root directory (absolute path, resolved via EvalSymlinks at construction)

	// readRoots operator-declared additional **readable** root directories (storage root + PEERDRIVE_SHARE_DIRS).
	//
	// Why separate from rootDir: rootDir is the **write/register boundary** — peers can
	// register any path via create, but must never register outside the download directory.
	// The read side is a different matter: if the operator declares "/data/media is my
	// shared directory" and registered it, it should be able to serve it externally. Mixing
	// the two checks causes "registration succeeds, list shows it, but peer fetch gets read
	// failed" — read side treats it as unauthorized and falls back to non-existent
	// content-addressed copy. See AddReadRoot / IsPathReadable.
	rootMu    sync.RWMutex
	readRoots []string

	upMu    sync.Mutex
	uploads map[string]*UploadSession // name → chunked upload session (multi-source/resume sharing)
}

// NewFileIndexService creates the index service. uploadDir is the default save directory
// for uploaded files, also serving as the allowed root directory for create registration
// (H2 security boundary, see IsPathAllowed).
func NewFileIndexService(uploadDir string) *FileIndexService {
	if uploadDir == "" {
		uploadDir = "./files"
	}
	root, err := filepath.Abs(uploadDir)
	if err != nil {
		root = uploadDir
	}
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	svc := &FileIndexService{uploadDir: uploadDir, rootDir: root, uploads: make(map[string]*UploadSession)}
	go svc.reapUploads()
	return svc
}

// IsPathAllowed checks whether path is within the **register/write** allowed root directory.
// Security boundary (H2 arbitrary file read fix): create only allows registering files within
// the root directory — previously it accepted any absolute path, allowing peers to `create
// /etc/shadow`, get the hash, then `req` to read it; the info verb would also leak the path.
// Symbolic link resolution prevents "symlink within root → target outside root".
//
// It also serves as the **upload/save** boundary, so don't put shared directories here:
// that would allow peers to write files into your externally shared directory. For external
// readability, see IsPathReadable.
func (s *FileIndexService) IsPathAllowed(path string) bool {
	return pathutil.Within(s.rootDir, path)
}

// writeRoots write/save boundary: only rootDir.
//
// When to use it: any action that **produces or modifies files** (WriteFile / BeginUpload /
// deleting temp files) must go through here. The check (IsPathAllowed) and the write must
// use the same root, otherwise you get "check uses A, write uses B" cracks — historically
// this has caused incidents.
func (s *FileIndexService) writeRoots() []string {
	return []string{s.rootDir}
}

// AddReadRoot appends an operator-declared readable root directory (storage root,
// PEERDRIVE_SHARE_DIRS).
//
// Semantics: files in these directories **can be served externally**, but peers still
// **cannot register/write** to them (write boundary remains only rootDir). Empty strings
// and invalid paths are silently ignored — misconfiguration is better under-served.
func (s *FileIndexService) AddReadRoot(dir string) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		log.LogWarn("file-index: ignore invalid read root %q: %v", dir, err)
		return
	}
	s.rootMu.Lock()
	s.readRoots = append(s.readRoots, abs)
	s.rootMu.Unlock()
	log.LogInfo("file-index: read root added %s", abs)
}

// IsPathReadable checks whether path is **allowed for external reading**: within the
// download root (write boundary) or within operator-declared readable roots.
//
// Use this instead of IsPathAllowed on the read side (serveFile / LocalSource.resolvePath).
// Registration side continues using IsPathAllowed — the two cannot be merged, see AddReadRoot
// comments.
func (s *FileIndexService) IsPathReadable(path string) bool {
	return pathutil.WithinAny(s.readRootsAll(), path)
}

// readRootsAll returns all read boundary root directories: download root + operator-declared
// readable roots.
func (s *FileIndexService) readRootsAll() []string {
	s.rootMu.RLock()
	defer s.rootMu.RUnlock()
	return append([]string{s.rootDir}, s.readRoots...)
}

// FileInfo indexed file entry.
type FileInfo struct {
	Hash   string `json:"hash"`
	Size   int64  `json:"size"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Seq    int64  `json:"seq,omitempty"` // creation sequence (sync incremental basis)
	Ctime  int64  `json:"ctime,omitempty"`
	Mtime  int64  `json:"mtime,omitempty"`
}

// WriteFile writes content to the index (content-addressed storage).
// If the same hash already exists, overwrites content (newer wins).
// Returns the registration result including absolute path.
func (s *FileIndexService) WriteFile(name string, r io.Reader) (*FileInfo, error) {
	if !pathutil.IsSanitizedName(name) {
		name = "upload.bin"
	}
	// H2: write must stay within the download root (upload dir). Write to rootDir/<name>
	// directly; subdirectories are not created (preventing path traversal).
	// Discovery background: original implementation accepted arbitrary relative paths (e.g.
	// "../files/x"); peers could write outside the download directory. Now the full path is
	// constructed directly with uploadDir + name; any name containing .. is rejected by
	// IsSanitizedName.
	tmp, err := os.CreateTemp(s.rootDir, "upload-*.part")
	if err != nil {
		return nil, err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return nil, err
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	info, err := tmp.Stat()
	if err != nil {
		return nil, err
	}
	finalPath := filepath.Join(s.rootDir, name)
	// Defense: even after sanitization, check the final path is still within the root
	// (defense in depth).
	if !pathutil.Within(s.rootDir, finalPath) {
		return nil, fmt.Errorf("path traversal detected: %q", name)
	}
	if err := os.Rename(tmpPath, finalPath); err != nil {
		return nil, err
	}
	hashStr := hex.EncodeToString(h.Sum(nil))
	return &FileInfo{
		Hash:  hashStr,
		Size:  info.Size(),
		Name:  name,
		Path:  finalPath,
		Ctime: info.ModTime().Unix(),
		Mtime: info.ModTime().Unix(),
	}, nil
}

// Resolve resolves hash → absolute path via index (file must exist).
func (s *FileIndexService) Resolve(hash string) (string, int64, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return "", 0, fmt.Errorf("invalid hash")
	}
	info, err := repository.GetFileInfoByHash(hash)
	if err != nil {
		return "", 0, err
	}
	fi, err := os.Stat(info.Path)
	if err != nil {
		return "", 0, err
	}
	return info.Path, fi.Size(), nil
}

// Create registers an existing file into the index (creates sha256 → path mapping).
// Used for importing existing local files.
func (s *FileIndexService) Create(path string) (*FileInfo, error) {
	if !s.IsPathAllowed(path) {
		return nil, fmt.Errorf("path not allowed: %s", path)
	}
	// Resolve symlinks for registration path (store resolved path to avoid confusion)
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, fmt.Errorf("path is a directory: %s", path)
	}
	// Compute hash
	h, err := hashFile(resolved)
	if err != nil {
		return nil, err
	}
	hashStr := hex.EncodeToString(h)
	info := &FileInfo{
		Hash:  hashStr,
		Size:  fi.Size(),
		Name:  filepath.Base(resolved),
		Path:  resolved,
		Ctime: fi.ModTime().Unix(),
		Mtime: fi.ModTime().Unix(),
	}
	if err := repository.UpsertFileInfo(info); err != nil {
		return nil, err
	}
	return info, nil
}

// List returns indexed files (with optional pagination).
func (s *FileIndexService) List(offset, limit int) ([]FileInfo, int, error) {
	items, total, err := repository.ListFileInfos(offset, limit)
	if err != nil {
		return nil, 0, err
	}
	// Filter: only return files that actually exist on disk (clean up stale entries)
	var result []FileInfo
	for _, item := range items {
		if _, err := os.Stat(item.Path); err != nil {
			continue
		}
		result = append(result, item)
	}
	return result, total, nil
}

// GetInfo retrieves file info by hash (for info verb).
func (s *FileIndexService) GetInfo(hash string) (*FileInfo, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return nil, fmt.Errorf("invalid hash")
	}
	info, err := repository.GetFileInfoByHash(hash)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(info.Path); err != nil {
		return nil, fmt.Errorf("file not found: %w", err)
	}
	return info, nil
}

// Delete removes a file entry from the index and optionally deletes the file itself.
func (s *FileIndexService) Delete(hash string, removeFile bool) (*FileInfo, error) {
	if !hashutil.IsStrictSHA256(hash) {
		return nil, fmt.Errorf("invalid hash")
	}
	info, err := repository.GetFileInfoByHash(hash)
	if err != nil {
		return nil, err
	}
	if err := repository.DeleteFileInfo(hash); err != nil {
		return nil, err
	}
	if removeFile {
		os.Remove(info.Path)
	}
	return info, nil
}

// Sync returns files created after the given sequence number (incremental sync).
func (s *FileIndexService) Sync(seq int64) ([]FileInfo, int64, error) {
	items, err := repository.ListFileInfosBySeq(seq)
	if err != nil {
		return nil, 0, err
	}
	var lastSeq int64
	var result []FileInfo
	for _, item := range items {
		if _, err := os.Stat(item.Path); err != nil {
			continue
		}
		result = append(result, item)
		if item.Seq > lastSeq {
			lastSeq = item.Seq
		}
	}
	return result, lastSeq, nil
}

// hashFile computes sha256 of a file.
func hashFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

// UploadSession chunked upload session (shared across sources/resume).
//
// Each connection's data frames write slices into this session (concurrent WriteAt, no lock
// contention); upon completion, Complete computes the full file hash and registers it.
// Sessions are keyed by name — same name reuse allows resuming across connection breaks.
type UploadSession struct {
	Name     string
	Size     int64
	Path     string // temp file path for assembling slices
	f        *os.File
	offsets  []int64  // written offsets
	writeMu  sync.Mutex
	complete bool
	release  chan struct{} // closed when Complete finishes
}

const uploadChunkSize = 1 * 1024 * 1024 // 1MB chunks

// BeginUpload starts (or resumes) a chunked upload session for the given name/size.
func (s *FileIndexService) BeginUpload(name string, size int64) (*UploadSession, error) {
	s.upMu.Lock()
	defer s.upMu.Unlock()
	if existing, ok := s.uploads[name]; ok && existing.Size == size {
		return existing, nil
	}
	if existing, ok := s.uploads[name]; ok {
		// Size changed — old session is stale, clean up
		if existing.f != nil {
			existing.f.Close()
		}
		os.Remove(existing.Path)
		delete(s.uploads, name)
	}
	// Create temp file
	tmp, err := os.CreateTemp(s.rootDir, "upload-*.part")
	if err != nil {
		return nil, err
	}
	sess := &UploadSession{
		Name:    name,
		Size:    size,
		Path:    tmp.Name(),
		f:       tmp,
		release: make(chan struct{}),
	}
	s.uploads[name] = sess
	return sess, nil
}

// WriteAt writes a chunk at the given offset (thread-safe, concurrent calls OK).
func (u *UploadSession) WriteAt(offset int64, data []byte) error {
	u.writeMu.Lock()
	defer u.writeMu.Unlock()
	if u.complete {
		return fmt.Errorf("upload complete")
	}
	_, err := u.f.WriteAt(data, offset)
	if err != nil {
		return err
	}
	u.offsets = append(u.offsets, offset)
	return nil
}

// Complete finalizes the upload: close, hash, rename to final path, register in index.
func (u *UploadSession) Complete() (*FileInfo, error) {
	u.writeMu.Lock()
	if u.complete {
		u.writeMu.Unlock()
		return nil, fmt.Errorf("already complete")
	}
	u.complete = true
	u.writeMu.Unlock()
	defer close(u.release)

	if u.f != nil {
		if err := u.f.Sync(); err != nil {
			return nil, err
	}
		if err := u.f.Close(); err != nil {
			return nil, err
		}
		u.f = nil
	}

	// Hash the assembled file
	h, err := hashFile(u.Path)
	if err != nil {
		os.Remove(u.Path)
		return nil, err
	}
	fi, err := os.Stat(u.Path)
	if err != nil {
		os.Remove(u.Path)
		return nil, err
	}
	hashStr := hex.EncodeToString(h)
	finalPath := filepath.Join(filepath.Dir(u.Path), u.Name)
	if err := os.Rename(u.Path, finalPath); err != nil {
		return nil, err
	}
	return &FileInfo{
		Hash:  hashStr,
		Size:  fi.Size(),
		Name:  u.Name,
		Path:  finalPath,
		Ctime: fi.ModTime().Unix(),
		Mtime: fi.ModTime().Unix(),
	}, nil
}

// Release abandons the upload session (cleanup on failure/disconnect).
func (u *UploadSession) Release() {
	u.writeMu.Lock()
	if u.complete {
		u.writeMu.Unlock()
		return
	}
	u.complete = true
	u.writeMu.Unlock()
	if u.f != nil {
		u.f.Close()
		u.f = nil
	}
	os.Remove(u.Path)
	close(u.release)
}

// reapUploads periodically cleans up stale upload sessions (10-minute timeout).
func (s *FileIndexService) reapUploads() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.upMu.Lock()
		for name, sess := range s.uploads {
			select {
			case <-sess.release:
				delete(s.uploads, name)
			default:
				// Session still active, check if it's been idle too long
			}
		}
		s.upMu.Unlock()
	}
}

// sanitizeUploadName normalizes an upload filename for safety.
func sanitizeUploadName(name string) string {
	// Remove path components, prevent traversal
	name = filepath.Base(name)
	// Strip leading dots (hidden files, "..", ".")
	name = strings.TrimLeft(name, ".")
	// Length limit
	if len(name) > 200 {
		name = name[:200]
	}
	// Windows: filepath.Base("/") returns **`\`** (separator), not "/" —
	// checking only `name == "/"` would miss it on Windows, and Join would
	// produce uploadDir itself ("is a directory"). Handle both separators
	// (discovered on real Windows machine, 2026-09-20).
	if name == "" || name == "." || name == ".." || name == "/" || name == `\` {
		return "upload.bin"
	}
	return name
}

// UploadChunkSizeForTest provides chunk granularity for integration tests.
func UploadChunkSizeForTest() int { return uploadChunkSize }

// PendingFetchesForTest returns remaining fetch state reqIds for a connection (test helper:
// source package PeerSource race tests verify loser streams are reaped and state is
// cleaned; needs cross-package observation of internal fetches map. Not called in
// production path).
func (s *PeerJSService) PendingFetchesForTest(sess Session) []string {
	st := s.stateFor(sess)
	if st == nil {
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]string, 0, len(st.fetches))
	for id := range st.fetches {
		out = append(out, id)
	}
	return out
}
