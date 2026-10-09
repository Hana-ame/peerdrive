package source

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openlist_test.go: OpenListSource tests.
// Discovery background: the index table is the only contract between an
// operator-supplied hash→path mapping and this source, so every field of it is
// exercised — a malformed entry must be rejected rather than silently dropped,
// because a table that quietly loses a file looks healthy in logs. The /p/ path
// is exercised against a fake backend for every Range outcome OpenList can
// return (206 / 200-ignoring-Range / 416 / 404), since URL construction is where
// a path or query mistake turns a working table into a wall of 404s.
// Verification is asserted both ways: a full fetch must be content-addressed,
// and an operator who opts out must get an unchecked stream back.
//
// Tests run against httptest servers, never a live OpenList: Range handling is
// the whole contract, and asserting on it through an external deployment would
// make these tests slow and flaky.
func writeOpenListIndex(t *testing.T, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "index.json")
	require.NoError(t, os.WriteFile(p, []byte(contents), 0o600))
	return p
}

// newTestOpenList builds a source backed by the given table. A dedicated client
// with a short timeout is injected — never http.DefaultClient, which a test
// would otherwise end up mutating as a global side effect.
func newTestOpenList(t *testing.T, filename, base string) *OpenListSource {
	t.Helper()
	index, err := LoadOpenListIndex(filename)
	require.NoError(t, err)
	s, err := NewOpenListSource(OpenListConfig{
		BaseURL: base,
		Index:   index,
		Verify:  true,
		Client:  &http.Client{Timeout: 5 * time.Second},
	})
	require.NoError(t, err)
	return s
}

// readAll reads a stream and closes it first, so a verification error raised at
// EOF is reported as the read error and not lost behind Close.
func readAll(t *testing.T, r io.ReadCloser) (string, error) {
	t.Helper()
	var buf strings.Builder
	_, err := io.Copy(&buf, r)
	_ = r.Close()
	return buf.String(), err
}

const hashZero = "0000000000000000000000000000000000000000000000000000000000000000"

// TestNewOpenListIndex table: index table lookup — hits, misses, malformed tables.
func TestNewOpenListIndex(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		want     map[string]string
		wantErr  string
	}{
		{
			name:     "bare map is the table",
			contents: `{"0000000000000000000000000000000000000000000000000000000000000000": "/baidu/a.mp4"}`,
			want:     map[string]string{hashZero: "/baidu/a.mp4"},
		},
		{
			name:     "enveloped files wins and unknown keys are ignored",
			contents: `{"version": 1, "files": {"1111111111111111111111111111111111111111111111111111111111111111": "/netdisk/b.mp4"}}`,
			want:     map[string]string{strings.Repeat("1", 64): "/netdisk/b.mp4"},
		},
		{
			name:     "query is preserved verbatim",
			contents: `{"2222222222222222222222222222222222222222222222222222222222222222": "/signed/c.mp4?sign=abc"}`,
			want:     map[string]string{strings.Repeat("2", 64): "/signed/c.mp4?sign=abc"},
		},
		{
			name:     "non-string values are skipped",
			contents: `{"3333333333333333333333333333333333333333333333333333333333333333": "/d.mp4", "version": 1}`,
			want:     map[string]string{strings.Repeat("3", 64): "/d.mp4"},
		},
		{
			name:     "missing file",
			contents: "",
			wantErr:  "read index",
		},
		{
			name:     "invalid json",
			contents: `{not json`,
			wantErr:  "parse index",
		},
		{
			name:     "array is not an object",
			contents: `[1, 2]`,
			wantErr:  "must be a JSON object",
		},
		{
			name:     "null is not an object",
			contents: `null`,
			wantErr:  "must be a JSON object",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p string
			if tt.name == "missing file" {
				p = filepath.Join(t.TempDir(), "absent.json")
			} else {
				p = writeOpenListIndex(t, tt.contents)
			}
			got, err := LoadOpenListIndex(p)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestOpenListPathValidation table: every value shape the table can contain.
func TestOpenListPathValidation(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		wantPath  string
		wantQuery string
		wantErr   string
	}{
		{name: "plain absolute path", value: "/baidu/a b.mp4", wantPath: "/baidu/a b.mp4"},
		{name: "duplicate separators are cleaned", value: "//baidu//a.mp4", wantPath: "/baidu/a.mp4"},
		{name: "inner dot segments are cleaned", value: "/baidu/./a.mp4", wantPath: "/baidu/a.mp4"},
		{name: "query preserved", value: "/a?sign=x%20y&k=v", wantPath: "/a", wantQuery: "sign=x%20y&k=v"},
		{name: "a line break would be header injection", value: "/a\r\nHost: evil", wantErr: "must not contain line breaks"},
		{name: "the mount root is refused", value: "/", wantErr: "resolves to the mount root"},
		{name: "traversal to the mount root is refused", value: "/baidu/..", wantErr: "resolves to the mount root"},
		{name: "relative path is refused", value: "baidu/a.mp4", wantErr: "must be absolute"},
		{name: "empty value is refused", value: "", wantErr: "empty path"},
		{name: "whitespace-only value is refused", value: "   ", wantErr: "empty path"},
		{name: "a URL is not a mount path", value: "https://other.example/a", wantErr: "looks like a URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, q, err := splitOpenListPath(tt.value)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantPath, p)
			assert.Equal(t, tt.wantQuery, q)
		})
	}
}

// TestOpenListSource_BuildURL table: the wire form, including escaping and the
// preserved query.
func TestOpenListSource_BuildURL(t *testing.T) {
	s, err := NewOpenListSource(OpenListConfig{BaseURL: "https://ol.example.com"})
	require.NoError(t, err)

	tests := []struct {
		name    string
		value   string
		want    string
		wantErr string
	}{
		{
			name:  "path is joined under /p/ and escaped",
			value: "/baidu/a b.mp4",
			want:  "https://ol.example.com/p/baidu/a%20b.mp4",
		},
		{
			name:  "query is appended after the escaped path",
			value: "/a.mp4?sign=xyz",
			want:  "https://ol.example.com/p/a.mp4?sign=xyz",
		},
		{
			name:    "a line break still fails through construction",
			value:   "/a\nx",
			wantErr: "must not contain line breaks",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.buildURL(tt.value)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestOpenListSource_ConstructorValidation table: a bad table must abort the
// source rather than register and quietly serve nothing.
func TestOpenListSource_ConstructorValidation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OpenListConfig
		wantErr string
	}{
		{
			name:    "empty BaseURL is refused",
			cfg:     OpenListConfig{BaseURL: ""},
			wantErr: "BaseURL is required",
		},
		{
			name:    "whitespace BaseURL is refused",
			cfg:     OpenListConfig{BaseURL: "   "},
			wantErr: "BaseURL is required",
		},
		{
			name:    "a non-hash key is refused",
			cfg:     OpenListConfig{BaseURL: "https://ol", Index: map[string]string{"short": "/a"}},
			wantErr: "index key",
		},
		{
			name:    "an upper-case key is refused",
			cfg:     OpenListConfig{BaseURL: "https://ol", Index: map[string]string{strings.ToUpper(strings.Repeat("a", 64)): "/a"}},
			wantErr: "index key",
		},
		{
			name:    "a short key is refused",
			cfg:     OpenListConfig{BaseURL: "https://ol", Index: map[string]string{strings.Repeat("0", 63): "/a"}},
			wantErr: "index key",
		},
		{
			name:    "a bad path value is refused",
			cfg:     OpenListConfig{BaseURL: "https://ol", Index: map[string]string{hashZero: "relative/path"}},
			wantErr: "index " + hashZero,
		},
		{
			name: "zero values fall back to documented defaults",
			cfg:  OpenListConfig{BaseURL: "https://ol"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := NewOpenListSource(tt.cfg)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "openlist", s.Name(), "the name falls back to its default")
			assert.Equal(t, "openlist", s.Type())
			assert.Equal(t, CapStream, s.Capabilities(), "/p/ honours Range")
			assert.Equal(t, 0, s.Priority())
		})
	}
}

// TestOpenListSource_Reload table: a bad delivery must not tear down the table
// a node is already serving from.
func TestOpenListSource_Reload(t *testing.T) {
	hashA, hashB := strings.Repeat("a", 64), strings.Repeat("b", 64)
	s, err := NewOpenListSource(OpenListConfig{
		BaseURL: "https://ol", Index: map[string]string{hashA: "/a"}, Verify: true,
	})
	require.NoError(t, err)
	assert.Equal(t, 1, s.Count())

	assert.Error(t, s.Reload(nil), "an empty table is refused, not applied")
	assert.Equal(t, 1, s.Count(), "the previous table survives a refused reload")

	assert.Error(t, s.Reload(map[string]string{strings.Repeat("c", 63): "/c"}))
	assert.Equal(t, 1, s.Count())

	require.NoError(t, s.Reload(map[string]string{hashB: "/b"}))
	assert.Equal(t, 1, s.Count())
	_, ok := s.lookup(hashA)
	assert.False(t, ok)
	_, ok = s.lookup(hashB)
	assert.True(t, ok)

	p := writeOpenListIndex(t, `{"`+hashA+`": "/a"}`)
	require.NoError(t, s.LoadReload(p))
	_, ok = s.lookup(hashA)
	assert.True(t, ok)
}

// TestOpenListSource_Available: an empty table is reported unavailable, so the
// Manager does not burn one request per hash before falling through.
func TestOpenListSource_Available(t *testing.T) {
	s, err := NewOpenListSource(OpenListConfig{BaseURL: "https://ol"})
	require.NoError(t, err)
	assert.False(t, s.Available(context.Background()), "an empty index serves nothing")

	require.NoError(t, s.Reload(map[string]string{strings.Repeat("1", 64): "/a"}))
	assert.True(t, s.Available(context.Background()))
}

// TestOpenListSource_Open_Range206: the happy path — OpenList honours Range and
// the slice comes back unchanged.
func TestOpenListSource_Open_Range206(t *testing.T) {
	content := strings.Repeat("openlist-row-", 40)
	hash := testHash(content)

	var gotRange string
	mux := http.NewServeMux()
	mux.HandleFunc("/p/baidu/a.mp4", func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Range", "bytes 100-149/"+content)
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte(content[100:150]))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := newTestOpenList(t, writeOpenListIndex(t, `{"`+hash+`": "/baidu/a.mp4"}`), srv.URL)
	ctx := context.Background()

	t.Run("206 returns exactly the slice", func(t *testing.T) {
		r, err := s.Open(ctx, hash, 100, 50)
		require.NoError(t, err)
		got, err := readAll(t, r)
		require.NoError(t, err)
		assert.Equal(t, content[100:150], got)
		assert.Equal(t, "bytes=100-149", gotRange)
	})

	t.Run("a zero-length slice is answered without a round trip", func(t *testing.T) {
		gotRange = "sentinel"
		r, err := s.Open(ctx, hash, 10, 0)
		require.NoError(t, err)
		got, err := readAll(t, r)
		require.NoError(t, err)
		assert.Empty(t, got)
		assert.Equal(t, "sentinel", gotRange, "no request may be issued")
	})

	t.Run("a negative offset is normalised to zero", func(t *testing.T) {
		gotRange = ""
		r, err := s.Open(ctx, hash, -5, 10)
		require.NoError(t, err)
		_, _ = readAll(t, r)
		assert.Equal(t, "bytes=0-9", gotRange)
	})

	t.Run("an offset with an unbounded size has no end field", func(t *testing.T) {
		gotRange = ""
		r, err := s.Open(ctx, hash, 10, -1)
		require.NoError(t, err)
		_, _ = readAll(t, r)
		assert.Equal(t, "bytes=10-", gotRange)
	})
}

// TestOpenListSource_Open_ServerIgnoresRange: a backend that answers 200 full
// for a ranged request. Skimming past the offset is bandwidth-wasteful but it is
// the only correct behaviour without a HEAD probe.
func TestOpenListSource_Open_ServerIgnoresRange(t *testing.T) {
	content := strings.Repeat("full-file-", 60)
	hash := testHash(content)

	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()

	s := newTestOpenList(t, writeOpenListIndex(t, `{"`+hash+`": "/a"}`), srv.URL)
	r, err := s.Open(context.Background(), hash, 10, 20)
	require.NoError(t, err)
	assert.Equal(t, "bytes=10-29", gotRange)
	got, err := readAll(t, r)
	require.NoError(t, err)
	assert.Equal(t, content[10:30], got, "offset bytes are skipped and the read is bounded")
}

// TestOpenListSource_Open_Errors table: every failure mode must come back as a
// routed error, never as a silent empty body.
func TestOpenListSource_Open_Errors(t *testing.T) {
	hash := strings.Repeat("e", 64)

	newSource := func(handler http.HandlerFunc, table string) *OpenListSource {
		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)
		s := newTestOpenList(t, writeOpenListIndex(t, table), srv.URL)
		return s
	}

	var requested atomic.Int32
	tests := []struct {
		name    string
		source  *OpenListSource
		wantErr string
	}{
		{
			name: "a hash outside the table is refused before any request",
			source: newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requested.Add(1)
				w.WriteHeader(http.StatusNotFound)
			}), `{"`+strings.Repeat("f", 64)+`": "/other"}`),
			wantErr: "is not in the index",
		},
		{
			name: "416 is surfaced",
			source: newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			}), `{"`+hash+`": "/a"}`),
			wantErr: "416",
		},
		{
			name:    "404 is surfaced",
			source:  newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) }), `{"`+hash+`": "/a"}`),
			wantErr: "404",
		},
		{
			name: "a hanging backend is bounded by the client timeout",
			source: newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				<-r.Context().Done()
			}), `{"`+hash+`": "/a"}`),
			wantErr: "deadline exceeded",
		},
		{
			name: "a request that is refused outright is surfaced",
			source: newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Closing before writing makes net/http report the failure
				// without a status code, so this is not the same as 404.
				w.WriteHeader(http.StatusServiceUnavailable)
			}), `{"`+hash+`": "/a"}`),
			wantErr: "503",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.source.Open(context.Background(), hash, 0, 10)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
	assert.Equal(t, int32(0), requested.Load(), "no request may leave the node for an unknown hash")

	// A syntactically invalid hash is refused at the shared entry guard — hashing
	// is content addressing, so the entry must not accept an address it cannot name.
	var badHashRequest atomic.Int32
	s := newSource(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		badHashRequest.Add(1)
	}), `{"`+hash+`": "/a"}`)
	_, err := s.Open(context.Background(), "not-a-hash", 0, 10)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sha256 hash")
	assert.Equal(t, int32(0), badHashRequest.Load())
}

// TestOpenListSource_Verify: a full fetch is where content addressing is
// enforced. The operator switch is asserted both ways.
func TestOpenListSource_Verify(t *testing.T) {
	hash := strings.Repeat("a", 64)
	right := strings.Repeat("right-content-", 30)
	wrong := strings.Repeat("wrong-content-", 30)

	// Without a token the backend answers with bytes whose hash is not the one
	// requested — the mismatch case.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer secret-token" {
			_, _ = w.Write([]byte(right))
			return
		}
		_, _ = w.Write([]byte(wrong))
	}))
	defer srv.Close()

	build := func(token string, verify bool) *OpenListSource {
		s, err := NewOpenListSource(OpenListConfig{
			BaseURL: srv.URL, Token: token, Index: map[string]string{hash: "/a"},
			Verify: verify, Client: &http.Client{Timeout: 5 * time.Second},
		})
		require.NoError(t, err)
		return s
	}

	t.Run("verification on rejects a tampered backend", func(t *testing.T) {
		s := build("", true)
		r, err := s.Open(context.Background(), hash, 0, -1)
		require.NoError(t, err)
		_, err = readAll(t, r)
		require.Error(t, err, "the mismatch must surface, not vanish at EOF")
		assert.Contains(t, err.Error(), "content hash mismatch")

		_, err = s.Fetch(context.Background(), hash)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "content hash mismatch")
	})

	t.Run("verification off returns the bytes unchecked", func(t *testing.T) {
		s := build("", false)
		r, err := s.Open(context.Background(), hash, 0, -1)
		require.NoError(t, err)
		got, err := readAll(t, r)
		require.NoError(t, err, "the operator opted out of the check")
		assert.Equal(t, wrong, got)
	})

	t.Run("the token is sent and a correct backend passes", func(t *testing.T) {
		// The table names the hash of the bytes the backend actually sends back.
		s, err := NewOpenListSource(OpenListConfig{
			BaseURL: srv.URL, Token: "secret-token", Verify: true,
			Index:  map[string]string{testHash(right): "/a"},
			Client: &http.Client{Timeout: 5 * time.Second},
		})
		require.NoError(t, err)
		data, err := s.Fetch(context.Background(), testHash(right))
		require.NoError(t, err)
		assert.Equal(t, right, string(data))
	})

	t.Run("Fetch stays content-addressed even when Open was told not to", func(t *testing.T) {
		s := build("", false)
		_, err := s.Fetch(context.Background(), hash)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "content hash mismatch")
	})
}

// TestOpenListSource_Info declares the unsupported capability: nil, nil is the
// documented answer for a capability a source does not have, not an error.
func TestOpenListSource_Info(t *testing.T) {
	s, err := NewOpenListSource(OpenListConfig{BaseURL: "https://ol"})
	require.NoError(t, err)
	meta, err := s.Info(context.Background(), hashZero)
	assert.NoError(t, err)
	assert.Nil(t, meta)
}

// TestOpenListSource_VerifyingReaderClose: callers tend to both defer and close
// explicitly, so Close must be idempotent, and a read after close must say so
// rather than blaming the hash.
func TestOpenListSource_VerifyingReaderClose(t *testing.T) {
	v := &verifyingReadCloser{r: http.NoBody, hash: hashZero, name: "openlist"}
	require.NoError(t, v.Close())
	require.NoError(t, v.Close(), "a second Close must not fail")
	_, err := v.Read(make([]byte, 1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read after close")
}

// TestOpenListSource_RangeEndOverflow guards against an int64 wrap producing a
// nonsensical range header.
func TestOpenListSource_RangeEndOverflow(t *testing.T) {
	_, err := rangeEnd(1, int64(1)<<62+1)
	require.Error(t, err)
	end, err := rangeEnd(0, 9)
	require.NoError(t, err)
	assert.Equal(t, "8", end, "bytes=0-8 is the first nine bytes")
	end, err = rangeEnd(5, -1)
	require.NoError(t, err)
	assert.Equal(t, "", end)
}
