// Package collection implements a minimal content-addressed "collection": a JSON
// document listing files as {path, sha, preview, source, metadata} entries, stored
// in peerdrive's sha-file system (content-addressed storage, CAS) keyed by its
// own SHA-256.
//
// A collection is therefore an ordinary sha file: it is saved like any other file
// (storageDir/<sha[:2]>/<sha>, the same layout anon_repo.SaveCollection uses),
// read back by its sha, and served to peers over the existing `req` frame channel
// (source.LocalSource resolves an unknown hash via the CAS fallback), so no
// transport change is needed for the "respond by sha" path.
//
// Distinct from the SQLite collections table and model.AnonCollection (which carry
// visibility / owner / tags / providers / version chains): this module is
// intentionally lean — path + sha (+ optional preview sha) plus, since the
// multi-alternative-source extension (2026-10-09), an optional per-entry `source`
// object (sha / url / ech-url / private.url — any one available is enough to
// fetch the file) and optional file metadata (name / mime / created_at /
// modified_at / size). All new fields are omitempty, so documents written by the
// pre-extension format still parse and keep their sha addresses; new documents
// only gain the extra fields when they carry data.
//
// Fetch semantics: Entry.Fetch tries the alternatives in priority order
// sha → ech-url → url → private.url and returns the first stream that works,
// recording every attempt in a FetchMonitor so the fallback trajectory and the
// per-source success distribution stay observable (see fetch.go / monitor.go).
// The sha alternative is resolved by an injected SHAOpener (the source.Manager /
// ShaSource / a CAS read satisfy it); the http alternatives go through an
// injected *http.Client. Collection itself stays free of the source package so
// its dependency leaf stays shallow.
//
// It is a backend-only internal package: no other go.mod consumes it
// (peerjs / signalserver / p2p_bt / signalframe are independent libraries), so it
// lives under internal/ rather than as a submodule (hashmap precedent).
package collection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
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

// SourceRef is a file entry's multi-alternative fetch source: the same logical
// file addressed through several channels. Any non-empty alternative is enough
// to fetch the file — "one of them works" is the fetch contract (see Fetch).
//   - SHA: the content is already in the sha-file system (or resolvable by
//     content address via a SHAOpener), the hot path.
//   - ECHURL: reachable through ECH / an ech-proxy entry (cold path).
//   - URL: direct URL (cold path).
//   - Private: a private URL, only fetched when the node is authorized (the
//     caller decides authorization; the format only carries the address).
type SourceRef struct {
	SHA     string      `json:"sha,omitempty"`
	URL     string      `json:"url,omitempty"`
	ECHURL  string      `json:"ech-url,omitempty"`
	Private *PrivateURL `json:"private,omitempty"`
}

// PrivateURL is the private-URL alternative of a source.
type PrivateURL struct {
	URL string `json:"url,omitempty"`
}

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
	// Optional only when the entry carries at least one other fetchable
	// alternative (Source.URL / ECHURL / private.url / Source.SHA); a remote-only
	// entry has sha == "" until its content is ingested into the sha-fs.
	SHA string `json:"sha"`
	// Preview is the 64-hex lowercase SHA-256 of a preview/thumbnail file
	// (usually an image) in the sha-file system; empty means no preview.
	// Same strictness as SHA (it is a sha-fs address too).
	Preview string `json:"preview,omitempty"`
	// Name is the file name for display and as the default download name. When
	// set it must be a bare name (no path separators). Empty = derived from Path.
	Name string `json:"name,omitempty"`
	// MIME is the file media type (e.g. image/jpeg, video/mp4) used by consumers
	// for type switching and preview rendering. Empty = unknown.
	MIME string `json:"mime,omitempty"`
	// CreatedAt / ModifiedAt are Unix seconds of creation/modification as known
	// by the data source. 0 = unknown (field omitted). Stored as Unix seconds —
	// compact in JSON, unambiguous, and what the API surface already uses.
	CreatedAt  int64 `json:"created_at,omitempty"`
	ModifiedAt int64 `json:"modified_at,omitempty"`
	// Size is the file size in bytes when known; 0 = unknown.
	Size int64 `json:"size,omitempty"`
	// Source carries the multi-alternative fetch sources of the file. nil = the
	// entry is sha-only (the pre-extension shape, still fully valid).
	Source *SourceRef `json:"source,omitempty"`
}

// Collection is the JSON document stored in the sha-file system.
type Collection struct {
	Version int     `json:"version"`
	Entries []Entry `json:"entries"`
}

// Validate checks the version and every entry:
//   - version must equal Version;
//   - path must be non-empty (whitespace-only counts as empty) and unique;
//   - the entry must carry at least one fetchable alternative — sha, or one of
//     source.{sha, url, ech-url, private.url} (an entry nobody can fetch is
//     useless even if it parses);
//   - sha / preview / source.sha must be empty or strict 64-hex SHA-256
//     (see hashutil.IsStrictSHA256);
//   - source url / ech-url / private.url must be absolute http(s) URLs;
//   - top-level sha and source.sha, when both set, must agree;
//   - name, when set, must be a bare file name (no path separators / CR/LF);
//   - created_at / modified_at / size must be non-negative when set.
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
		if !hasAlternative(e) {
			return fmt.Errorf("collection: entries[%d]: no fetchable alternative (need sha or source url/ech-url/private.url)", i)
		}
		if e.SHA != "" && !hashutil.IsStrictSHA256(e.SHA) {
			return fmt.Errorf("collection: entries[%d]: invalid sha %q", i, e.SHA)
		}
		if e.Preview != "" && !hashutil.IsStrictSHA256(e.Preview) {
			return fmt.Errorf("collection: entries[%d]: invalid preview sha %q", i, e.Preview)
		}
		if e.Source != nil {
			if err := e.Source.validate(); err != nil {
				return fmt.Errorf("collection: entries[%d]: %w", i, err)
			}
			// Two sha fields naming different content is a contradiction the
			// fetch path cannot resolve; reject instead of picking one silently.
			if e.Source.SHA != "" && e.SHA != "" && e.Source.SHA != e.SHA {
				return fmt.Errorf("collection: entries[%d]: conflicting sha alternatives (%s vs %s)", i, e.SHA, e.Source.SHA)
			}
		}
		if e.Name != "" && (strings.ContainsAny(e.Name, `/\`) || strings.ContainsAny(e.Name, "\r\n")) {
			return fmt.Errorf("collection: entries[%d]: invalid name %q (must be a bare file name)", i, e.Name)
		}
		if e.CreatedAt < 0 || e.ModifiedAt < 0 || e.Size < 0 {
			return fmt.Errorf("collection: entries[%d]: negative metadata (created_at/modified_at/size)", i)
		}
		if _, dup := seen[e.Path]; dup {
			return fmt.Errorf("collection: entries[%d]: duplicate path %q", i, e.Path)
		}
		seen[e.Path] = struct{}{}
	}
	return nil
}

// hasAlternative reports whether the entry carries at least one fetchable source.
func hasAlternative(e Entry) bool {
	if e.SHA != "" {
		return true
	}
	if e.Source == nil {
		return false
	}
	if e.Source.SHA != "" || e.Source.URL != "" || e.Source.ECHURL != "" {
		return true
	}
	return e.Source.Private != nil && e.Source.Private.URL != ""
}

func (s *SourceRef) validate() error {
	if s.SHA != "" && !hashutil.IsStrictSHA256(s.SHA) {
		return fmt.Errorf("invalid source sha %q", s.SHA)
	}
	for name, u := range map[string]string{"url": s.URL, "ech-url": s.ECHURL} {
		if err := validateSourceURL(name, u); err != nil {
			return err
		}
	}
	if s.Private != nil {
		if err := validateSourceURL("private.url", s.Private.URL); err != nil {
			return err
		}
	}
	return nil
}

// validateSourceURL accepts an absolute http(s) URL. A remote alternative is
// fetched verbatim, so control characters that could smuggle headers or path
// confusion are rejected at the boundary.
func validateSourceURL(name, raw string) error {
	if raw == "" {
		return nil
	}
	if strings.ContainsAny(raw, "\r\n") {
		return fmt.Errorf("invalid source %s: must not contain line breaks", name)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid source %s %q: %w", name, raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid source %s %q: scheme must be http or https", name, raw)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid source %s %q: missing host", name, raw)
	}
	return nil
}

// New builds a collection from entries: validates them, normalizes nil → empty,
// and sorts canonically so identical content always yields the same sha
// regardless of input order. Content-addressing determinism is the same reason
// repository.SaveCollection sorts entries by path before marshaling.
func New(entries []Entry) (*Collection, error) {
	c := &Collection{Version: Version, Entries: entries}
	if c.Entries == nil {
		c.Entries = []Entry{}
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sort.SliceStable(c.Entries, func(i, j int) bool {
		a, b := c.Entries[i], c.Entries[j]
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		if a.SHA != b.SHA {
			return a.SHA < b.SHA
		}
		if a.Preview != b.Preview {
			return a.Preview < b.Preview
		}
		// Entries identical in path/sha/preview still differ in the extended
		// fields (source/name/mime/times/size). Tie-break on the canonical JSON
		// so the total order is a pure function of content — identical content
		// in a different input order must not mint a second CAS address. For
		// pre-extension entries (path/sha/preview only) equality means the JSON
		// is equal too, so the old ordering is preserved exactly.
		bj, _ := json.Marshal(a)
		cj, _ := json.Marshal(b)
		return bytes.Compare(bj, cj) < 0
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
// storageDir via pathutil.SafeWriteFileAny — repo-wide rule: no path derived
// from input may be written with a bare os.WriteFile.
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
	return StoreFile(storageDir, canon)
}

// StoreFile writes arbitrary content-addressed bytes into the sha-file system
// (CAS) and returns their sha256. This is the generic "put content into the
// sha-fs" write used both for collection documents (via SaveJSON) and for
// ingested media files; content addressing makes the write idempotent (same
// bytes land on the same path).
func StoreFile(storageDir string, data []byte) (string, error) {
	if strings.TrimSpace(storageDir) == "" {
		return "", errors.New("collection: empty storage dir")
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	// SafeWriteFileAny auto-creates parent directories; content addressing
	// makes the overwrite harmless (same bytes on the same path).
	if err := pathutil.SafeWriteFileAny([]string{storageDir}, casPath(storageDir, sha), data, 0o644); err != nil {
		return "", fmt.Errorf("collection: store file: %w", err)
	}
	return sha, nil
}

// ReadFile reads arbitrary content-addressed bytes by sha from the sha-file
// system. Unlike ReadJSON it does not require the bytes to be a collection
// document — it is the read side of StoreFile (ingested media, previews, …).
func ReadFile(storageDir, sha string) ([]byte, error) {
	if strings.TrimSpace(storageDir) == "" {
		return nil, errors.New("collection: empty storage dir")
	}
	if !hashutil.IsStrictSHA256(sha) {
		return nil, fmt.Errorf("collection: invalid sha %q", sha)
	}
	filePath := casPath(storageDir, sha)
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
