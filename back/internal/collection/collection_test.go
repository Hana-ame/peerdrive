package collection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"peerdrive/pkg/hashutil"
)

// shaOf returns the lowercase hex SHA-256 of s — a handy way to fabricate valid
// 64-hex file/preview addresses for fixtures without external data.
func shaOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// TestNew_SortsCanonicallyAndNormalizesNil verifies construction invariants that
// content addressing depends on: entries are canonicalized (sorted) so identical
// content yields an identical sha regardless of input order, and nil entries
// become an empty slice so the JSON is "entries":[] instead of "entries":null
// (null would silently differ across serializers).
func TestNew_SortsCanonicallyAndNormalizesNil(t *testing.T) {
	// 发现背景: repository.SaveCollection sorts entries by path before marshaling
	// for exactly this reason (deterministic content hash); New must do the same
	// on construction, otherwise "same collection, different input order" produces
	// a different sha and the CAS address is no longer a pure function of content.
	a := shaOf("file-a")
	b := shaOf("file-b")

	c, err := New([]Entry{
		{Path: "b.txt", SHA: b},
		{Path: "a.txt", SHA: a},
	})
	require.NoError(t, err)
	require.Equal(t, []Entry{{Path: "a.txt", SHA: a}, {Path: "b.txt", SHA: b}}, c.Entries)
	require.Equal(t, Version, c.Version)

	empty, err := New(nil)
	require.NoError(t, err)
	require.NotNil(t, empty.Entries)
	require.Empty(t, empty.Entries)

	// A valid preview sha is accepted and preserved.
	withPrev, err := New([]Entry{{Path: "a.jpg", SHA: a, Preview: shaOf("thumb-a")}})
	require.NoError(t, err)
	require.Equal(t, shaOf("thumb-a"), withPrev.Entries[0].Preview)
}

// TestNew_ValidationErrors drives the entry-level validation rules (bad sha /
// empty path / bad preview / duplicate path). Discovery background: these inputs
// can arrive from a peer's JSON or a hand-rolled collection; the module must
// reject them before they enter the sha-fs, because sha values are used verbatim
// to build CAS paths (storageDir/<sha[:2]>/<sha>).
func TestNew_ValidationErrors(t *testing.T) {
	valid := shaOf("file")
	validPrev := shaOf("thumb")

	tests := []struct {
		name    string
		entries []Entry
		wantErr string // substring expected in the error
	}{
		{name: "empty path", entries: []Entry{{Path: "", SHA: valid}}, wantErr: "empty path"},
		{name: "whitespace path", entries: []Entry{{Path: "  ", SHA: valid}}, wantErr: "empty path"},
		// 发现背景: 多备选源扩展后 sha 可为空（条目可纯远端备选）；但一个条目
		// 至少要有一种可取的备选，否则「能解析却永远取不到文件」。
		{name: "no alternative", entries: []Entry{{Path: "a", SHA: ""}}, wantErr: "no fetchable alternative"},
		{name: "short sha", entries: []Entry{{Path: "a", SHA: "abc"}}, wantErr: "invalid sha"},
		{name: "non-hex sha", entries: []Entry{{Path: "a", SHA: strings.Repeat("z", 64)}}, wantErr: "invalid sha"},
		{name: "uppercase sha", entries: []Entry{{Path: "a", SHA: strings.ToUpper(valid)}}, wantErr: "invalid sha"},
		{name: "bad preview", entries: []Entry{{Path: "a", SHA: valid, Preview: "not-a-sha"}}, wantErr: "invalid preview sha"},
		{name: "empty preview is fine", entries: []Entry{{Path: "a", SHA: valid, Preview: ""}}, wantErr: ""},
		{name: "duplicate path", entries: []Entry{{Path: "a", SHA: valid}, {Path: "a", SHA: shaOf("other")}}, wantErr: "duplicate path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.entries)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}

	// validPrev exercised only through the positive path above; sanity here.
	_, err := New([]Entry{{Path: "a", SHA: valid, Preview: validPrev}})
	require.NoError(t, err)
}

// TestCollection_JSONRoundTrip verifies the marshal → unmarshal round trip keeps
// entries intact. Discovery background: the JSON is the wire/storage format — the
// frontend renders it as a folder view and peers exchange it verbatim, so a
// round-trip loss (e.g. field drift, preview dropping) would corrupt collections
// without any error being raised.
func TestCollection_JSONRoundTrip(t *testing.T) {
	a := shaOf("file-a")
	p := shaOf("thumb-a")
	c, err := New([]Entry{
		{Path: "dir/b.txt", SHA: shaOf("file-b")},
		{Path: "dir/a.jpg", SHA: a, Preview: p},
	})
	require.NoError(t, err)

	data, err := c.JSON()
	require.NoError(t, err)

	got, err := Unmarshal(data)
	require.NoError(t, err)
	require.Equal(t, c.Version, got.Version)
	require.Equal(t, c.Entries, got.Entries)
}

// TestCollection_SHA256_LocksFormat verifies sha computation is deterministic
// and stable against accidental format drift (field rename/reorder). Discovery
// background: the sha is the collection's CAS address — if the JSON format ever
// changes, every previously stored address stops resolving; the hardcoded digest
// below pins the current canonical serialization.
func TestCollection_SHA256_LocksFormat(t *testing.T) {
	// 发现背景: this literal must stay byte-identical to json.Marshal's output
	// (struct field order: version, entries → path, sha, preview). A reordering
	// or a renamed tag would change the digest and this test fails immediately,
	// before any stored collection stops resolving.
	const lockedJSON = `{"version":1,"entries":[{"path":"a.txt","sha":"1111111111111111111111111111111111111111111111111111111111111111","preview":"2222222222222222222222222222222222222222222222222222222222222222"}]}`
	const wantDigest = "43382fddce9255676a28eec1a43a8cf011c8a067c43266a5ca061871dc51fe64"

	c, err := New([]Entry{{Path: "a.txt", SHA: strings.Repeat("1", 64), Preview: strings.Repeat("2", 64)}})
	require.NoError(t, err)
	data, err := c.JSON()
	require.NoError(t, err)
	require.Equal(t, lockedJSON, string(data))
	got, err := c.SHA256()
	require.NoError(t, err)
	require.Equal(t, wantDigest, got)
}

// TestCollection_SHA256_OrderIndependence verifies identical content always maps
// to one address. Discovery background: same entries entered in a different
// order must not mint a second CAS copy — the whole point of content addressing
// is that equal content has exactly one address.
func TestCollection_SHA256_OrderIndependence(t *testing.T) {
	a := shaOf("file-a")
	b := shaOf("file-b")
	c1, err := New([]Entry{{Path: "a", SHA: a}, {Path: "b", SHA: b}})
	require.NoError(t, err)
	c2, err := New([]Entry{{Path: "b", SHA: b}, {Path: "a", SHA: a}})
	require.NoError(t, err)
	s1, err := c1.SHA256()
	require.NoError(t, err)
	s2, err := c2.SHA256()
	require.NoError(t, err)
	require.Equal(t, s1, s2)

	// Different content must not collide.
	c3, err := New([]Entry{{Path: "a", SHA: a}})
	require.NoError(t, err)
	s3, err := c3.SHA256()
	require.NoError(t, err)
	require.NotEqual(t, s1, s3)
}

// TestCollection_Unmarshal_RejectsInvalid drives the parse-time validation.
// Discovery background: Unmarshal is the gate for bytes arriving from a peer or
// from the CAS; malformed documents must fail loudly instead of being served as
// a "valid" collection with garbage entries.
func TestCollection_Unmarshal_RejectsInvalid(t *testing.T) {
	valid := shaOf("file")
	validEntry := fmt.Sprintf(`{"path":"a","sha":%q}`, valid)

	tests := []struct {
		name    string
		json    string
		wantErr string
	}{
		{name: "not json", json: "not-json", wantErr: "invalid json"},
		{name: "array", json: `[]`, wantErr: "invalid json"},
		{name: "missing version", json: `{"entries":[]}`, wantErr: "unsupported version"},
		{name: "zero version", json: `{"version":0,"entries":[]}`, wantErr: "unsupported version"},
		{name: "future version", json: `{"version":2,"entries":[]}`, wantErr: "unsupported version"},
		{name: "bad entry sha", json: `{"version":1,"entries":[{"path":"a","sha":"zz"}]}`, wantErr: "invalid sha"},
		{name: "empty entry path", json: `{"version":1,"entries":[{"path":"","sha":` + fmt.Sprintf("%q", valid) + `}]}`, wantErr: "empty path"},
		{name: "duplicate path", json: `{"version":1,"entries":[` + validEntry + `,` + validEntry + `]}`, wantErr: "duplicate path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Unmarshal([]byte(tt.json))
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantErr)
		})
	}

	// A minimal valid document parses and normalizes null entries.
	got, err := Unmarshal([]byte(`{"version":1,"entries":null}`))
	require.NoError(t, err)
	require.Empty(t, got.Entries)
	require.NotNil(t, got.Entries)
}

// TestCollection_SaveLoad_RoundTrip verifies the full store → read-back cycle:
// the file lands at storageDir/<sha[:2]>/<sha>, its bytes hash back to the
// returned sha, Load returns identical entries, and re-saving the same content
// is idempotent (same address). Discovery background: this is the module's
// contract with the sha-file system — any drift here breaks both local reads and
// the peerjs req path (which serves the same CAS file).
func TestCollection_SaveLoad_RoundTrip(t *testing.T) {
	storageDir := t.TempDir()

	a := shaOf("file-a")
	p := shaOf("thumb-a")
	c, err := New([]Entry{
		{Path: "dir/a.jpg", SHA: a, Preview: p},
		{Path: "dir/b.txt", SHA: shaOf("file-b")},
	})
	require.NoError(t, err)

	sha, err := Save(storageDir, c)
	require.NoError(t, err)
	require.Len(t, sha, 64)

	// File exists at the content-addressed path and its bytes hash to sha.
	casFile := filepath.Join(storageDir, sha[:2], sha)
	raw, err := os.ReadFile(casFile)
	require.NoError(t, err)
	sum := sha256.Sum256(raw)
	require.Equal(t, sha, hex.EncodeToString(sum[:]))

	// Raw JSON read-back matches the canonical bytes (the respond path payload).
	rawJSON, err := ReadJSON(storageDir, sha)
	require.NoError(t, err)
	canon, err := c.JSON()
	require.NoError(t, err)
	require.Equal(t, canon, rawJSON)

	// Parsed read-back matches the original entries.
	got, err := Load(storageDir, sha)
	require.NoError(t, err)
	require.Equal(t, c.Entries, got.Entries)

	// Idempotent: identical content → identical address, no duplicate copy.
	sha2, err := Save(storageDir, c)
	require.NoError(t, err)
	require.Equal(t, sha, sha2)
}

// TestCollection_Load_InvalidOrMissing drives the load-side defenses: an
// invalid sha is rejected before any path is built (sha[:2] on a short string
// would panic), a well-formed but absent sha reports "not found", and a file
// whose content does not match its address is rejected (content-addressing
// integrity).
func TestCollection_Load_InvalidOrMissing(t *testing.T) {
	storageDir := t.TempDir()

	t.Run("invalid sha", func(t *testing.T) {
		// 发现背景: Load inputs may come from a URL/peer; without the strict
		// check, "ab" would panic at sha[:2] and a non-hex 64-char string would
		// build a path that can never resolve.
		for _, bad := range []string{"", "abc", strings.Repeat("z", 64), strings.ToUpper(shaOf("x")), strings.Repeat("0", 63)} {
			_, err := Load(storageDir, bad)
			require.Error(t, err)
			require.Contains(t, err.Error(), "invalid sha")
		}
	})

	t.Run("absent sha", func(t *testing.T) {
		_, err := Load(storageDir, shaOf("never-saved"))
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found locally")
	})

	t.Run("content does not match address", func(t *testing.T) {
		// 发现背景: a file hand-placed at <sha> with different bytes must not be
		// served as that collection — the address is defined by the content.
		addr := shaOf("file")
		dir := filepath.Join(storageDir, addr[:2])
		require.NoError(t, os.MkdirAll(dir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, addr), []byte(`{"version":1,"entries":[]}`), 0o644))
		_, err := Load(storageDir, addr)
		require.Error(t, err)
		require.Contains(t, err.Error(), "sha mismatch")
	})
}

// TestCollection_RespondBySHA_HTTP demonstrates the "respond by sha" path over
// HTTP, mirroring how download.go serves anon collections: validate the hash,
// read the CAS JSON, write it out. Discovery background: a peer requesting the
// collection over the peerjs `req` channel receives exactly these bytes
// (source.LocalSource serves the same CAS file), so the httptest here pins the
// wire payload the transport path will reuse.
func TestCollection_RespondBySHA_HTTP(t *testing.T) {
	storageDir := t.TempDir()
	c, err := New([]Entry{{Path: "a.txt", SHA: shaOf("file-a"), Preview: shaOf("thumb-a")}})
	require.NoError(t, err)
	sha, err := Save(storageDir, c)
	require.NoError(t, err)
	canon, err := c.JSON()
	require.NoError(t, err)

	// Handler shaped like controller.DownloadBySHA256Internal: 400 on bad hash,
	// 404 on missing, 200 + bytes otherwise.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := r.URL.Query().Get("sha")
		if !hashutil.IsStrictSHA256(hash) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		data, err := ReadJSON(storageDir, hash)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})

	t.Run("found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/collection?sha="+sha, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		require.Equal(t, canon, rec.Body.Bytes())
	})

	t.Run("missing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/collection?sha="+shaOf("never-saved"), nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("bad hash", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/collection?sha=not-a-hash", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})
}
