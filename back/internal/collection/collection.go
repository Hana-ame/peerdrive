// Package collection implements a minimal content-addressed "collection": a JSON
// document listing files as {path, sha, preview} entries, stored in peerdrive's
// sha-file system (content-addressed storage, CAS) keyed by its own SHA-256.
//
// A collection is therefore an ordinary sha file: it is saved like any other file
// (storageDir/<sha[:2]>/<sha>, the same layout anon_repo.SaveCollection uses),
// read back by its sha, and served to peers over the existing `req` frame channel
// (source.LocalSource resolves an unknown hash via the CAS fallback), so no
// transport change is needed for the "respond by sha" path.
//
// Distinct from the SQLite collections table and model.AnonCollection (which carry
// visibility / owner / tags / providers / version chains): this module is
// intentionally minimal — path + sha (+ optional preview sha) per entry, nothing
// else. It is a backend-only internal package: no other go.mod consumes it
// (peerjs / signalserver / p2p_bt / signalframe are independent libraries), so it
// lives under internal/ rather than as a submodule (hashmap precedent).
package collection

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"peerdrive/internal/pathutil"
	"peerdrive/pkg/hashutil"
)

// Version is the current collection JSON format version. Kept inside the document
// so a future format change is detectable on load — same reason
// model.AnonCollection rejects Version < 1 on read.
const Version = 1

// Entry is one file in a collection.
type Entry struct {
	// Path is the display path of the file inside the collection (relative path
	// or display name; the frontend renders entries as a folder view). Must be
	// non-empty; duplicate paths are rejected (a collection is a set of
	// path→sha mappings).
	Path string `json:"path"`
	// SHA is the 64-hex lowercase SHA-256 of the real file in the sha-file
	// system. Strict lowercase is required: it is used verbatim to build the CAS
	// path storageDir/<sha[:2]>/<sha> and for file_index lookups, both of which
	// store lowercase — accepting uppercase would silently miss both.
	SHA string `json:"sha"`
	// Preview is the 64-hex lowercase SHA-256 of a preview/thumbnail file
	// (usually an image) in the sha-file system; empty means no preview.
	// Same strictness as SHA (it is a sha-fs address too).
	Preview string `json:"preview,omitempty"`
}

// Collection is the JSON document stored in the sha-file system.
type Collection struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Validate checks the version and every entry:
//   - version must equal Version;
//   - path must be non-empty (whitespace-only counts as empty);
//   - sha must be a strict 64-hex SHA-256 (see hashutil.IsStrictSHA256);
//   - preview must be empty or a strict 64-hex SHA-256;
//   - paths must be unique (duplicate path would render ambiguously as a folder).
func (c *Collection) Validate() error {
	if c == nil {
		return errors.New("collection: nil collection")
	}
	if c.Version != Version {
		return fmt.Errorf("collection: unsupported version %d (want %d)", c.Version, Version)
	}
	seen := make(map[string]struct{}, len(c.Entries))
	for i, e := range c.Entries {
		if strings.TrimSpace(e.Path) == "" {
			return fmt.Errorf("collection: entries[%d]: empty path", i)
		}
		if !hashutil.IsStrictSHA256(e.SHA) {
			return fmt.Errorf("collection: entries[%d]: invalid sha %q", i, e.SHA)
		}
		if e.Preview != "" && !hashutil.IsStrictSHA256(e.Preview) {
			return fmt.Errorf("collection: entries[%d]: invalid preview sha %q", i, e.Preview)
		}
		if _, dup := seen[e.Path]; dup {
			return fmt.Errorf("collection: entries[%d]: duplicate path %q", i, e.Path)
		}
		seen[e.Path] = struct{}{}
	}
	return nil
}

// New builds a collection from entries: validates them, normalizes nil → empty,
// and sorts canonically (path, sha, preview) so identical content always yields
// the same sha regardless of input order. Content-addressing determinism is the
// same reason repository.SaveCollection sorts entries by path before marshaling.
func New(entries []Entry) (*Collection, error) {
	c := &Collection{Version: Version, Entries: entries}
	if c.Entries == nil {
		c.Entries = []Entry{}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sort.SliceStable(c.Entries, func(i, j int) bool {
		if c.Entries[i].Path != c.Entries[j].Path {
			return c.Entries[i].Path < c.Entries[j].Path
		}
		if c.Entries[i].SHA != c.Entries[j].SHA {
			return c.Entries[i].SHA < c.Entries[j].SHA
		}
		return c.Entries[i].Preview < c.Entries[j].Preview
	})
	return c, nil
}

// JSON returns the canonical JSON bytes: the collection's sha is computed over
// exactly these bytes and Save stores them verbatim. json.Marshal emits struct
// fields in declaration order, so identical entries always produce identical
// bytes.
func (c *Collection) JSON() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// SHA256 returns the lowercase hex SHA-256 of the canonical JSON.
func (c *Collection) SHA256() (string, error) {
	data, err := c.JSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// Unmarshal parses a collection JSON document and validates it. On success the
// returned collection is normalized (nil entries → empty slice).
func Unmarshal(data []byte) (*Collection, error) {
	var c Collection
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("collection: invalid json: %w", err)
	}
	if c.Entries == nil {
		c.Entries = []Entry{}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// casPath returns storageDir/<sha[:2]>/<sha>, the content-addressed layout the
// whole repo shares (anon_repo.SaveCollection writes it, source.LocalSource and
// transport serveFile fall back to it).
func casPath(storageDir, sha string) string {
	return filepath.Join(storageDir, sha[:2], sha)
}

// Save persists the collection's canonical JSON into the sha-file system and
// returns its sha. Content addressing makes the write idempotent: identical
// content lands on the identical path with identical bytes.
func Save(storageDir string, c *Collection) (string, error) {
	data, err := c.JSON()
	if err != nil {
		return "", err
	}
	return SaveJSON(storageDir, data)
}

// SaveJSON persists a validated collection JSON document into the sha-file
// system and returns its sha. The document is parsed and re-serialized to its
// canonical form first, so only structurally valid collections enter the CAS
// (the bytes could come from a peer). All file operations are rooted at
// storageDir via pathutil.Safe*Any — repo-wide rule: no path derived from
// input may be written with a bare os.WriteFile.
func SaveJSON(storageDir string, data []byte) (string, error) {
	if strings.TrimSpace(storageDir) == "" {
		return "", errors.New("collection: empty storage dir")
	}
	c, err := Unmarshal(data)
	if err != nil {
		return "", err
	}
	canon, err := c.JSON()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canon)
	sha := hex.EncodeToString(sum[:])
	// SafeWriteFileAny auto-creates parent directories; content addressing
	// makes the overwrite harmless (same bytes on the same path).
	if err := pathutil.SafeWriteFileAny([]string{storageDir}, casPath(storageDir, sha), canon, 0o644); err != nil {
		return "", fmt.Errorf("collection: save: %w", err)
	}
	return sha, nil
}

// ReadJSON reads the raw collection JSON bytes addressed by sha from the
// sha-file system. This is the "respond" path: the bytes are exactly what a
// peerjs `req` frame would deliver (LocalSource serves the same CAS file) or
// what a local/HTTP responder writes to the wire.
func ReadJSON(storageDir, sha string) ([]byte, error) {
	if strings.TrimSpace(storageDir) == "" {
		return nil, errors.New("collection: empty storage dir")
	}
	// Strict check before slicing: sha[:2] on a short string panics, and a
	// non-hex value would silently construct a path that can never match a real
	// CAS file. Same defense as repository.GetAnonCollectionByHash.
	if !hashutil.IsStrictSHA256(sha) {
		return nil, fmt.Errorf("collection: invalid sha %q", sha)
	}
	filePath := casPath(storageDir, sha)
	// Stat only for error classification ("not found" vs "invalid path"):
	// SafeOpen's normalize step EvalSymlinks a missing file into a confusing
	// ErrOutsideRoot. The actual read still goes through SafeOpen (os.Root), so
	// a symlink swap between Stat and Open fails closed.
	if _, err := os.Stat(filePath); err != nil {
		return nil, fmt.Errorf("collection: not found locally: %w", err)
	}
	f, err := pathutil.SafeOpen(storageDir, filePath)
	if err != nil {
		return nil, fmt.Errorf("collection: open %s: %w", filePath, err)
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Load reads and validates the collection addressed by sha.
func Load(storageDir, sha string) (*Collection, error) {
	data, err := ReadJSON(storageDir, sha)
	if err != nil {
		return nil, err
	}
	// Content-addressing integrity: the bytes stored under sha must hash to sha.
	// Catches corruption or a hand-placed file that does not match its address;
	// cheap on small JSON documents.
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != sha {
		return nil, fmt.Errorf("collection: sha mismatch: content is %s, want %s", got, sha)
	}
	return Unmarshal(data)
}
