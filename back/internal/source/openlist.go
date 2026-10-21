package source

// openlist.go: OpenListSource — serves files from an OpenList instance (
// github.com/OpenListTeam/OpenList, an AList fork that aggregates many cloud
// storages behind one HTTP API).
//
// Why HTTP instead of import: OpenList's driver layer lives under internal/,
// which Go's import rules confine to OpenList's own module — peerdrive cannot
// link against it at all. The only integration seam is therefore OpenList's HTTP
// surface. Of its endpoints only /p/*path (handles.Proxy) is usable here:
// /d/*path issues a 302 to the cloud provider's direct URL, which would let a
// request bypass this source's sha256 verification entirely. /p/ streams the
// bytes through the OpenList process and honours the Range header, so it
// composes with the streaming semantics Manager.OpenRange expects.
//
// Why an external index: OpenList metadata does not carry a trustworthy sha256
// (model.Obj.GetHash is whatever the cloud provider happens to report and is
// frequently empty). Content addressing is therefore established on the
// peerdrive side by an external hash→path table; this source only translates
// "hash" into "path" and streams. Building that table (walking OpenList's
// /api/fs/list and hashing each file) is deliberately a separate PR — until it
// lands, the table is operator-supplied via PEERDRIVE_OPENLIST_INDEX_FILE.
//
// Verification: like URLSource, a full request (offset==0 && size<0) hashes the
// stream and fails on mismatch. Range chunks are not verified — a chunk cannot
// be verified on its own, the full-fetch path is the fallback that guarantees it.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	hashutil "peerdrive/pkg/hashutil"
)

const defaultOpenListName = "openlist"

// OpenListConfig holds OpenListSource settings. Defaults are owned by the
// config layer, not by this constructor: Verify in particular must be set
// deliberately, so it is a plain bool rather than a *bool with a hidden default.
type OpenListConfig struct {
	// Name is the registry key. Empty = "openlist".
	Name string
	// BaseURL is the OpenList base URL — scheme, host and optional mount prefix,
	// without a trailing /p. Required: the constructor rejects an empty value so
	// a misconfiguration surfaces here rather than on the first request.
	BaseURL string
	// Client is the HTTP client used for every request. nil = http.DefaultClient.
	// Injecting a client with a custom Transport routes the traffic through an
	// egress module (ech-proxy) the same way URLSource does.
	Client *http.Client
	// Token is sent as "Authorization: Bearer <token>" when non-empty. Needed for
	// OpenList deployments whose mounts are not public.
	Token string
	// Index maps sha256 hex → OpenList path. nil = an empty table, which makes
	// Available() report false until Reload supplies entries.
	Index map[string]string
	// Verify hashes full requests against the requested hash (see the package
	// comment above).
	Verify bool
	// Priority is the routing priority (smaller = tried first).
	Priority int
}

// OpenListSource is an HTTP-backed Source whose backend is an OpenList instance.
type OpenListSource struct {
	name   string
	base   string
	client *http.Client
	token  string
	verify bool

	mu    sync.RWMutex
	index map[string]string // sha256 hex → OpenList path, validated on the way in

	prioMu   sync.RWMutex
	priority int
}

// NewOpenListSource builds a source. BaseURL is required; every Index entry is
// validated, and the first invalid entry aborts construction with an error that
// names the offender — an operator's table must not silently lose files.
func NewOpenListSource(cfg OpenListConfig) (*OpenListSource, error) {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		return nil, fmt.Errorf("openlist: BaseURL is required")
	}
	if _, err := url.ParseRequestURI(base); err != nil {
		return nil, fmt.Errorf("openlist: BaseURL %q: %w", cfg.BaseURL, err)
	}

	index := make(map[string]string, len(cfg.Index))
	for h, v := range cfg.Index {
		if !hashutil.IsStrictSHA256(h) {
			return nil, fmt.Errorf("openlist: index key %q is not a 64-char lowercase sha256", h)
		}
		if _, _, err := splitOpenListPath(v); err != nil {
			return nil, fmt.Errorf("openlist: index %s: %w", h, err)
		}
		index[h] = v
	}

	client := cfg.Client
	if client == nil {
		client = http.DefaultClient
	}

	return &OpenListSource{
		name:     nameOr(cfg.Name, defaultOpenListName),
		base:     base,
		client:   client,
		token:    cfg.Token,
		verify:   cfg.Verify,
		index:    index,
		priority: cfg.Priority,
	}, nil
}

// LoadOpenListIndex reads a hash→path index table from path.
//
// Two shapes are accepted, so an operator can hand-write either:
//
//	{ "<sha256>": "/baidu/a.mp4" }                              // bare map
//	{ "version": 1, "files": { "<sha256>": "/baidu/a.mp4" } }   // enveloped
//
// The envelope is what a crawler-generated table looks like once it starts
// carrying metadata too; unknown keys are ignored either way. Values are
// "<path>[?<query>]" — the query, when present, is carried through to the
// server verbatim because an OpenList signed-download URL may need it.
func LoadOpenListIndex(filename string) (map[string]string, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("openlist: read index: %w", err)
	}

	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("openlist: parse index: %w", err)
	}
	raw, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("openlist: index must be a JSON object of hash → path, got %T", decoded)
	}

	// The envelope wins when both shapes are present, so {"files":{...}} is never
	// mistaken for a table that happens to have a "files" key.
	if files, ok := raw["files"].(map[string]any); ok {
		return buildIndexFrom(files), nil
	}
	return buildIndexFrom(raw), nil
}

// buildIndexFrom turns a loose JSON object into an index, skipping anything that
// is not a string value (e.g. an envelope key such as "version").
func buildIndexFrom(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

// Reload replaces the index table atomically. It re-validates every entry, so a
// bad delivery cannot tear down a working table — on error the previous table is
// left in place. This is the seam a future crawler/admin endpoint feeds; this PR
// only wires the static file.
func (s *OpenListSource) Reload(index map[string]string) error {
	if len(index) == 0 {
		return fmt.Errorf("openlist: refusing to replace the index with an empty table")
	}
	next := make(map[string]string, len(index))
	for h, v := range index {
		if !hashutil.IsStrictSHA256(h) {
			return fmt.Errorf("openlist: index key %q is not a 64-char lowercase sha256", h)
		}
		if _, _, err := splitOpenListPath(v); err != nil {
			return fmt.Errorf("openlist: index %s: %w", h, err)
		}
		next[h] = v
	}
	s.mu.Lock()
	s.index = next
	s.mu.Unlock()
	return nil
}

// LoadReload re-reads an index file and swaps it in. It exists so a caller that
// owns a file path does not have to repeat the read/validate dance.
func (s *OpenListSource) LoadReload(filename string) error {
	index, err := LoadOpenListIndex(filename)
	if err != nil {
		return err
	}
	return s.Reload(index)
}

// Count returns how many entries the current table holds.
func (s *OpenListSource) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.index)
}

func (s *OpenListSource) lookup(hash string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.index[hash]
	return v, ok
}

// splitOpenListPath validates an index value and splits it into a normalised
// path and a preserved query string.
//
// This is a remote mount path, not a path into the node's own storage, so
// pathutil does not apply here: nothing is ever written or opened under it.
// What it does guard against is a value that would address a directory rather
// than a file — /p/ on a directory returns a listing, which would make a
// content-addressed fetch return non-file bytes.
//
// Only the path part is path.Clean-ed; the query is carried verbatim so a signed
// OpenList URL reaches the server intact. Requiring a leading "/" both matches
// how OpenList addresses mounts and prevents a value that cleaned up to "/"
// from turning the request into a listing of the whole tree.
func splitOpenListPath(value string) (string, string, error) {
	raw := strings.TrimSpace(value)
	if raw == "" {
		return "", "", fmt.Errorf("empty path")
	}
	// The table is operator-supplied and the query is appended unescaped, so a
	// stray line break could become a header injection. Reject it at the boundary
	// instead of relying on net/http to notice.
	if strings.ContainsAny(raw, "\r\n") {
		return "", "", fmt.Errorf("path %q must not contain line breaks", value)
	}
	if strings.Contains(raw, "://") {
		return "", "", fmt.Errorf("path %q looks like a URL; index entries are OpenList mount paths, not URLs", raw)
	}

	p := raw
	q := ""
	if i := strings.IndexByte(raw, '?'); i >= 0 {
		p, q = raw[:i], raw[i+1:]
	}

	clean := path.Clean(p)
	if !strings.HasPrefix(clean, "/") {
		return "", "", fmt.Errorf("path %q must be absolute (start with \"/\")", raw)
	}
	if clean == "/" {
		return "", "", fmt.Errorf("path %q resolves to the mount root; name a file", raw)
	}
	return clean, q, nil
}

// buildURL turns an index value into the /p/ request URL. The path is escaped
// for transport and the preserved query is appended after it.
func (s *OpenListSource) buildURL(value string) (string, error) {
	p, q, err := splitOpenListPath(value)
	if err != nil {
		return "", err
	}
	// path.Clean produced a decoded path, so EscapedPath is the only place the
	// escaping happens — an operator writes "/a/b c" and the wire sees "/a/b%20c".
	escaped := (&url.URL{Path: p}).EscapedPath()
	u := s.base + "/p/" + strings.TrimPrefix(escaped, "/")
	if q != "" {
		u += "?" + q
	}
	return u, nil
}

func (s *OpenListSource) Name() string { return s.name }

func (s *OpenListSource) Type() string { return "openlist" }

// Capabilities is CapStream: /p/ honours Range, so chunked reads work. CapFile is
// deliberately not set — OpenList has no full-file endpoint of its own, Fetch is
// provided by delegating to Open(0, -1) and Manager.OpenAny prefers stream sources
// anyway.
func (s *OpenListSource) Capabilities() Capability { return CapStream }

func (s *OpenListSource) Priority() int {
	s.prioMu.RLock()
	defer s.prioMu.RUnlock()
	return s.priority
}

func (s *OpenListSource) SetPriority(p int) {
	s.prioMu.Lock()
	s.priority = p
	s.prioMu.Unlock()
}

// Available reports false while the table is empty: an empty index can serve
// nothing, and the Manager would otherwise burn one request per hash before
// falling through to the next source. No network probing is done — the same rule
// URLSource follows, so a source never hides a failing backend behind a green ping.
func (s *OpenListSource) Available(ctx context.Context) bool {
	return s.Count() > 0
}

// Open streams a slice of the indexed file. offset<0 normalises to 0; size<0
// means to end of file; a zero-length slice is served without a request at all.
func (s *OpenListSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	if offset < 0 {
		offset = 0
	}

	value, ok := s.lookup(hash)
	if !ok {
		return nil, fmt.Errorf("openlist: %s is not in the index", hash)
	}
	if size == 0 {
		// Nothing to fetch and nothing to verify — answer without a round trip.
		return io.NopCloser(strings.NewReader("")), nil
	}

	u, err := s.buildURL(value)
	if err != nil {
		return nil, fmt.Errorf("openlist: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}
	if offset > 0 || size >= 0 {
		end, err := rangeEnd(offset, size)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", offset, end))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable || resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("openlist: HTTP %d", resp.StatusCode)
	}

	var body io.ReadCloser
	if resp.StatusCode == http.StatusPartialContent {
		body = resp.Body
	} else {
		// The server ignored the Range header and sent the whole file (200).
		// Skimming past the offset is bandwidth-wasteful but correct — URLSource
		// makes the same tradeoff, and there is no cheap way to find out whether
		// the backend supports Range before asking.
		if offset > 0 {
			if _, err := io.CopyN(io.Discard, resp.Body, offset); err != nil {
				resp.Body.Close()
				return nil, err
			}
		}
		body = resp.Body
		if size >= 0 {
			body = &limitedReadCloser{r: io.LimitReader(resp.Body, size), c: resp.Body}
		}
	}

	// A full request is where content addressing is enforced: the bytes coming back
	// must actually be the content the hash names.
	if offset == 0 && size < 0 && s.verify {
		return &verifyingReadCloser{r: body, hash: hash, name: s.name}, nil
	}
	return body, nil
}

// Fetch retrieves the whole file. It always verifies, regardless of Verify,
// because the Source contract requires a full fetch to be content-addressed.
// When Verify is on, Open has already hashed the stream once on its way out —
// this second pass over an in-memory buffer is the price of keeping Fetch's
// contract self-contained rather than plumbing a "already verified" flag through.
func (s *OpenListSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	r, err := s.Open(ctx, hash, 0, -1)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("openlist: content hash mismatch for %s", hash)
	}
	return data, nil
}

// Info is not supported: OpenList can answer it via /api/fs/get, but that is a
// separate authenticated JSON endpoint and the index table already carries what
// upper layers need for this PR. Returning nil, nil is the documented
// "unsupported" answer for this capability, not an error.
func (s *OpenListSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}

// rangeEnd formats the Range header's end value. size<0 means "to end of file",
// which RFC 7233 encodes as an empty end field. The overflow check is real: a
// hostile or buggy caller passing size near MaxInt64 would otherwise wrap around
// to a negative end and produce a nonsensical range.
func rangeEnd(offset, size int64) (string, error) {
	if size < 0 {
		return "", nil
	}
	if size > 0 && offset > 0 && size > int64(1)<<62 {
		return "", fmt.Errorf("openlist: range %d..%d overflows", offset, size)
	}
	return strconv.FormatInt(offset+size-1, 10), nil
}

// verifyingReadCloser hashes what flows through and compares against hash at EOF
// — the same content-addressed fallback URLSource uses. Close is guarded by a
// Once because callers tend to both defer and explicitly close.
type verifyingReadCloser struct {
	r    io.ReadCloser
	hash string
	name string
	h    hash.Hash

	done   bool
	err    error
	closed atomic.Bool
	close  sync.Once
}

func (v *verifyingReadCloser) Read(p []byte) (int, error) {
	if v.done {
		return 0, v.err
	}
	// An aborted fetch closes the body without reading it. Without this guard the
	// next Read would turn the empty body into "content hash mismatch", which
	// points an operator at the wrong thing.
	if v.closed.Load() {
		return 0, fmt.Errorf("%s: read after close", v.name)
	}
	n, err := v.r.Read(p)
	if n > 0 {
		if v.h == nil {
			v.h = sha256.New()
		}
		v.h.Write(p[:n])
	}
	if err == io.EOF {
		v.done = true
		if v.h == nil || hex.EncodeToString(v.h.Sum(nil)) != v.hash {
			v.err = fmt.Errorf("%s: content hash mismatch for %s", v.name, v.hash)
		} else {
			v.err = io.EOF
		}
		return n, v.err
	}
	if err != nil {
		v.done = true
		v.err = err
	}
	return n, err
}

func (v *verifyingReadCloser) Close() error {
	var err error
	v.closed.Store(true)
	v.close.Do(func() { err = v.r.Close() })
	return err
}

func nameOr(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
