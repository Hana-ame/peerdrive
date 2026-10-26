package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/pathutil"
	"peerdrive/internal/repository"
	"peerdrive/pkg/urlguard"
)

// ResolveURL fetches a file from a URL, computes SHA256 and detects MIME type, without writing to storage/DB.
// When followRedirects=false, 301/302 redirects are rejected.
func (s *FileService) ResolveURL(rawURL string, followRedirects bool) (hash string, mimeType string, size int64, body []byte, filename string, err error) {
	defer log.LogDuration("FileService.ResolveURL")()
	log.LogDebug("file-svc: ResolveURL url=%s followRedirects=%v", rawURL, followRedirects)

	// SSRF guard (2026-10-04): this function fetches a caller-supplied URL on the node's
	// behalf, exactly like the P2P pull verb does.
	if err := urlguard.GuardExternalURL(rawURL); err != nil {
		log.LogWarn("file-svc: ResolveURL rejected %s: %v", rawURL, err)
		return "", "", 0, nil, "", fmt.Errorf("URL rejected by SSRF guard: %w", err)
	}

	safeTransport := urlguard.NewSafeTransport()
	client := &http.Client{
		Timeout:   5 * time.Minute,
		Transport: safeTransport,
	}
	if !followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	} else {
		// Per-hop redirect validation: checking only the first hop lets a public URL 302 to
		// 127.0.0.1 straight back into the internal network. Same reasoning as pull.go.
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("redirect exceeds 10 hops, aborting")
			}
			if err := urlguard.GuardExternalURL(req.URL.String()); err != nil {
				return fmt.Errorf("redirect target rejected: %w", err)
			}
			return nil
		}
	}

	// codeql[go/request-forgery]: rawURL is validated by urlguard.GuardExternalURL on initial hop (:29) and every redirect hop (:50) with SafeTransport IP pinning.
	resp, err := client.Get(rawURL)
	if err != nil {
		log.LogError("file-svc: ResolveURL GET %s failed: %v", rawURL, err)
		return "", "", 0, nil, "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.LogWarn("file-svc: ResolveURL %s returned status %d", rawURL, resp.StatusCode)
		return "", "", 0, nil, "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, resp.Status)
	}

	maxLimit := s.cfg.MaxUploadBytes
	if maxLimit <= 0 {
		maxLimit = 100 * 1024 * 1024 // 100MB default fallback limit
	}
	if cl := resp.ContentLength; cl > maxLimit {
		return "", "", 0, nil, "", fmt.Errorf("content length %d exceeds maximum allowed limit of %d bytes", cl, maxLimit)
	}

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxLimit+1))
	if err != nil {
		log.LogError("file-svc: ResolveURL read body failed: %v", err)
		return "", "", 0, nil, "", fmt.Errorf("read body: %w", err)
	}
	if int64(len(body)) > maxLimit {
		return "", "", 0, nil, "", fmt.Errorf("response body exceeds maximum allowed limit of %d bytes", maxLimit)
	}

	h := sha256.Sum256(body)
	hash = hex.EncodeToString(h[:])
	size = int64(len(body))

	// MIME detection: magic number sniffing first, Content-Type fallback
	mimeType = http.DetectContentType(body[:min(len(body), 512)])
	if ct := resp.Header.Get("Content-Type"); ct != "" && mimeType == "application/octet-stream" {
		mimeType = ct
	}

	// Filename extraction: Content-Disposition -> URL basename
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, encoded, ok := strings.Cut(cd, "filename*="); ok {
			if idx := strings.Index(encoded, "''"); idx > 0 && idx+2 < len(encoded) {
				part := encoded[idx+2:]
				if end := strings.IndexByte(part, ';'); end > 0 {
					part = part[:end]
				}
				decoded, decErr := percentUnescape(strings.TrimSpace(part))
				if decErr == nil && decoded != "" {
					filename = decoded
				}
			}
		}
		if filename == "" {
			if _, f, ok := strings.Cut(cd, "filename="); ok {
				filename = strings.Trim(f, "\" ")
			}
		}
	}
	if filename == "" {
		filename = path.Base(rawURL)
	}

	log.LogInfo("file-svc: ResolveURL %s -> hash=%s mime=%s size=%d", rawURL, hash, mimeType, size)
	return hash, mimeType, size, body, filename, nil
}

// RegisterURL fetches a file from a URL, computes SHA256 and registers it (provider_type="http"), automatically following 301/302 redirects.
func (s *FileService) RegisterURL(rawURL string, filename string) (*model.FileMeta, error) {
	defer log.LogDuration("FileService.RegisterURL")()
	log.LogDebug("file-svc: RegisterURL url=%s filename=%s", rawURL, filename)

	hash, mimeType, size, body, autoFilename, err := s.ResolveURL(rawURL, true)
	if err != nil {
		return nil, err
	}
	if filename == "" {
		filename = autoFilename
	}

	// Issue #405 Step 2: one unified write replaces the old
	// InsertFileMeta + InsertFileProvider("http") [+ RegisterFile] triplet.
	// remote_url carries the URL-provider leg into file_index, so file_providers
	// no longer needs provider_type='http' rows — this pair was the only real
	// reader/writer of that table (see doc/TODO-SIMPLIFY.md M2).
	var (
		fullPath string
		relPath  string
	)
	if s.storageEnable {
		relPath = hash[:2] + "/" + hash
		fullPath = filepath.Join(s.storageDir, relPath)
		if err := pathutil.SafeWriteFileAny(s.allowedRoots(), fullPath, body, 0644); err != nil {
			log.LogWarn("file-svc: RegisterURL save to storage failed (non-fatal): %v", err)
			fullPath, relPath = "", ""
		}
	}
	if _, err := repository.RegisterFile(repository.FileRecord{
		Hash: hash, Path: fullPath, Name: filename, Size: size,
		Kind: model.FileTypeBlob, MimeType: mimeType,
		RemoteURL: rawURL, ProviderPath: relPath,
	}); err != nil {
		log.LogError("file-svc: RegisterURL register file failed: %v", err)
		return nil, fmt.Errorf("register file: %w", err)
	}

	meta := &model.FileMeta{
		Hash:     hash,
		Size:     size,
		MimeType: mimeType,
		Gziped:   false,
		Filename: filename,
		Type:     repository.FileTypeBlob,
	}

	log.LogInfo("file-svc: RegisterURL %s -> hash=%s size=%d", rawURL, hash, size)
	return meta, nil
}

// percentUnescape decodes percent-encoded sequences (e.g. %20 -> space).
func percentUnescape(s string) (string, error) {
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			hi, err1 := hexDecodeNibble(s[i+1])
			lo, err2 := hexDecodeNibble(s[i+2])
			if err1 == nil && err2 == nil {
				buf.WriteByte(hi<<4 | lo)
				i += 2
				continue
			}
		}
		buf.WriteByte(s[i])
	}
	return buf.String(), nil
}

func hexDecodeNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex nibble: %c", c)
	}
}
