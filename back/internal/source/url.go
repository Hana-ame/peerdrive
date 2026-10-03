package source

// url.go: URLSource — fetches content by URL template (HTTP source).
// Usage:
//   - External resource table: hash → known URL (e.g. https://gateway/ipfs/<hash>)
//   - Can inject a Transport pointing to ech-proxy or other egress (wintools' ech-proxy
//     can serve as the outbound proxy for this source, no need for a separate source type)
//
// Semantics:
//   - Template uses fmt.Sprintf: %s = hash; %d (optionally twice) = offset, size
//   - Supports CapStream: prefer requests with Range header (if server supports 206,
//     streaming chunks); when server returns 200 full, extract offset..offset+size
//     segment (bandwidth waste but correct)
//   - Full requests (offset==0, size<0) perform sha256 verification after reading (content-
//     addressed fallback) — URL source content may be tampered with; verification is the
//     baseline of content-addressed semantics
//   - Info not supported (HTTP HEAD metadata left for future)

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"strings"
	"sync"
)

// URLSource HTTP URL file source.
type URLSource struct {
	name     string
	template string // fmt template: %s=hash, optional %d=offset %d=size
	client   *http.Client
	caps     Capability // derived from template (see NewURLSource)

	mu       sync.RWMutex
	priority int
}

// NewURLSource creates a URL source. template examples:
//
//	"https://example.com/ipfs/%s"                    (no chunking, CapFile)
//	"https://example.com/f/%s?off=%d&size=%d"        (supports chunking, CapStream)
//
// client can be nil (defaults to http.Client); when injecting a custom Transport
// (e.g. ech-proxy egress proxy), pass a client with that Transport.
func NewURLSource(template string, client *http.Client) *URLSource {
	if client == nil {
		client = http.DefaultClient
	}
	s := &URLSource{name: "url", template: template, client: client}
	// Template contains %d → supports range params → CapStream; otherwise CapFile
	if strings.Contains(template, "%d") {
		s.caps = CapStream
	} else {
		s.caps = CapFile
	}
	return s
}

func (s *URLSource) Name() string { return s.name }
func (s *URLSource) Type() string { return "url" }

// Capabilities determined by template: contains %d (offset/size params) → streaming chunks.
func (s *URLSource) Capabilities() Capability { return s.caps }

func (s *URLSource) Priority() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.priority
}

func (s *URLSource) SetPriority(p int) {
	s.mu.Lock()
	s.priority = p
	s.mu.Unlock()
}

// Available URL source has no active health check (ping would waste requests) — first
// version always returns true, failures are exposed through routing stats (LastErr).
// Future versions can add a recent-success time window.
func (s *URLSource) Available(ctx context.Context) bool { return true }

// buildURL generates the request URL from the template. When template contains %d,
// fills in offset/size (size<0 → -1, server semantics decide; passing -1 here means
// entire segment).
func (s *URLSource) buildURL(hash string, offset, size int64) string {
	if s.caps&CapStream != 0 {
		return fmt.Sprintf(s.template, hash, offset, size)
	}
	return fmt.Sprintf(s.template, hash)
}

// Open streaming open (requires template to contain %d).
func (s *URLSource) Open(ctx context.Context, hash string, offset, size int64) (io.ReadCloser, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	url := s.buildURL(hash, offset, size)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if offset > 0 || size >= 0 {
		// Range request: bytes=start-end (size<0 → to end of file)
		end := ""
		if size >= 0 {
			end = fmt.Sprintf("%d", offset+size-1)
		}
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%s", offset, end))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable || resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("url: HTTP %d", resp.StatusCode)
	}
	var body io.ReadCloser
	if resp.StatusCode == http.StatusPartialContent {
		body = resp.Body
	} else {
		// Server doesn't support Range (returns 200 full): extract the offset segment.
		// Correctness first, bandwidth waste acceptable (no HEAD probing in first version)
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
	// Full request (offset==0 && size<0) → verify sha256 during read (content-addressed fallback)
	if offset == 0 && size < 0 {
		return &verifyReadCloser{r: body, hash: hash}, nil
	}
	return body, nil
}

// Fetch full fetch (CapFile template or defensive implementation).
func (s *URLSource) Fetch(ctx context.Context, hash string) ([]byte, error) {
	if err := validHash(hash); err != nil {
		return nil, err
	}
	url := s.buildURL(hash, 0, -1)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("url: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Content-addressed fallback: full fetch must verify sha256
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != hash {
		return nil, fmt.Errorf("url: content hash mismatch for %s", hash)
	}
	return data, nil
}

// Info URL source does not do metadata queries.
func (s *URLSource) Info(ctx context.Context, hash string) (*FileMeta, error) {
	return nil, nil
}

// verifyReadCloser verifies sha256 after reading completes (content-addressed fallback —
// URL source content may be tampered with; without verification, corrupted content would
// be treated as the addressed file). Verification runs at EOF: success → returns io.EOF;
// failure → returns hash mismatch error (ReadAll will catch it).
type verifyReadCloser struct {
	r    io.ReadCloser
	hash string
	h    hash.Hash

	done bool
	err  error
}

func (v *verifyReadCloser) Read(p []byte) (int, error) {
	if v.done {
		return 0, v.err
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
			v.err = fmt.Errorf("url: content hash mismatch for %s", v.hash)
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

func (v *verifyReadCloser) Close() error { return v.r.Close() }
