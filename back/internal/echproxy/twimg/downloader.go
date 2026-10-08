package twimg

// downloader.go: release download + sha256 verification.
//
// Download model (GitHub release standard):
//
//	https://github.com/<repo>/releases/download/<version>/<asset>      the binary
//	https://github.com/<repo>/releases/download/<version>/checksums.txt the digests
//
// checksums.txt is the source of truth for the expected digest — it ships in the same
// release as the binary, so a mismatch means either a broken download or a tampered
// release. ExpectedSHA256 is an *additional* operator pin on top of it, not a
// replacement: pinning to a hand-written constant would silently go stale when the tag
// is rebuilt.
//
// A verified binary is remembered in a "<binary>.sha256" sidecar so repeat starts do
// not touch the network at all. The sidecar is written only after a successful verify,
// and is never trusted over the file itself (the file's hash is always recomputed).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Downloader fetches and verifies the ech-proxy binary for one release.
type Downloader struct {
	// Repo is "owner/repo". Default DefaultRepo.
	Repo string
	// Version is the release tag. Default DefaultVersion.
	Version string
	// Asset is the release asset name. Default derived from runtime.GOOS/GOARCH.
	Asset string
	// InstallDir is where the binary (+ .sha256 sidecar) lands. Required.
	InstallDir string

	// ExpectedSHA256 optionally pins the digest; must agree with checksums.txt.
	ExpectedSHA256 string

	// Client is used for all requests (inject *http.Client for tests/proxies).
	Client *http.Client
}

// ChecksumURL is the release's checksums.txt location.
func (d *Downloader) ChecksumURL() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", d.Repo, d.Version, ChecksumFile)
}

// AssetURL is the release asset location.
func (d *Downloader) AssetURL() string {
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", d.Repo, d.Version, d.Asset)
}

// SidecarPath is the sha256 sidecar next to the installed binary.
func (d *Downloader) SidecarPath() string { return d.binaryPath() + ".sha256" }

// BinaryPath is where the verified binary is installed.
func (d *Downloader) BinaryPath() string { return d.binaryPath() }

func (d *Downloader) binaryPath() string { return filepath.Join(d.InstallDir, d.Asset) }

func (d *Downloader) check() error {
	if strings.TrimSpace(d.Repo) == "" {
		return errors.New("echproxy: Downloader.Repo is empty")
	}
	if strings.TrimSpace(d.Version) == "" {
		return errors.New("echproxy: Downloader.Version is empty")
	}
	if strings.TrimSpace(d.Asset) == "" {
		return errors.New("echproxy: Downloader.Asset is empty")
	}
	if strings.TrimSpace(d.InstallDir) == "" {
		return errors.New("echproxy: Downloader.InstallDir is empty")
	}
	if d.Client == nil {
		return errors.New("echproxy: Downloader.Client is nil")
	}
	return nil
}

// ParseChecksums parses a sha256sum-style file ("<hex>[  | *]<name>") into a name→hex map.
//
// It is deliberately a pure function: the format is small enough that a strict parser
// is worth more than a regex, and a malformed line must fail loudly rather than
// silently dropping an entry (a dropped entry would masquerade as "asset not found").
func ParseChecksums(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("echproxy: checksums.txt line %d malformed: %q", i+1, line)
		}
		hash := strings.TrimPrefix(fields[0], "*")
		if len(hash) != 64 || !isHex(hash) {
			return nil, fmt.Errorf("echproxy: checksums.txt line %d has an invalid sha256 %q", i+1, fields[0])
		}
		name := strings.TrimPrefix(fields[1], "*")
		if name == "" {
			return nil, fmt.Errorf("echproxy: checksums.txt line %d has an empty file name", i+1)
		}
		out[name] = strings.ToLower(hash)
	}
	if len(out) == 0 {
		return nil, errors.New("echproxy: checksums.txt contains no entries")
	}
	return out, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// Ensure returns the absolute path of a verified ech-proxy binary, downloading it first
// if necessary.
//
// Failure modes are all explicit errors — never a silent fallback to an unverified
// binary and never a partial file left in place:
//   - network/HTTP error while fetching checksums.txt or the asset
//   - asset not listed in checksums.txt ("no asset available")
//   - checksums.txt disagrees with ExpectedSHA256
//   - downloaded bytes hash differently from checksums.txt
func (d *Downloader) Ensure(ctx context.Context) (string, error) {
	if err := d.check(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(d.InstallDir, 0o755); err != nil {
		return "", fmt.Errorf("echproxy: mkdir %s: %w", d.InstallDir, err)
	}

	bin := d.binaryPath()
	side := d.SidecarPath()

	// Fast path: an existing binary whose hash still matches the recorded sidecar.
	// The file hash is always recomputed — a stale sidecar alone is not proof.
	if data, err := os.ReadFile(bin); err == nil {
		got := sha256Hex(data)
		if recorded, err := os.ReadFile(side); err == nil && strings.TrimSpace(string(recorded)) == got {
			_ = os.Chmod(bin, 0o755) // best effort; Windows ignores most of this
			return bin, nil
		}
		// Hash drift (edited file, or a rebuilt release): discard and re-download.
		_ = os.Remove(bin)
		_ = os.Remove(side)
	}

	sums, err := d.fetchChecksums(ctx)
	if err != nil {
		return "", err
	}
	want, ok := sums[d.Asset]
	if !ok {
		known := make([]string, 0, len(sums))
		for k := range sums {
			known = append(known, k)
		}
		return "", fmt.Errorf("echproxy: asset %q is not listed in checksums.txt of %s %s (available: %v)",
			d.Asset, d.Repo, d.Version, known)
	}
	if exp := strings.ToLower(strings.TrimSpace(d.ExpectedSHA256)); exp != "" && exp != want {
		return "", fmt.Errorf("echproxy: checksum pin mismatch for %s: release checksums.txt says %s, ExpectedSHA256 says %s",
			d.Asset, want, d.ExpectedSHA256)
	}

	hash, tmp, err := d.download(ctx)
	if err != nil {
		return "", err
	}
	if hash != want {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("echproxy: sha256 mismatch for %s: downloaded %s, release checksums.txt says %s",
			d.Asset, hash, want)
	}
	if err := os.Rename(tmp, bin); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("echproxy: install %s: %w", bin, err)
	}
	if err := os.Chmod(bin, 0o755); err != nil {
		// Non-fatal: on Windows file modes are advisory and the binary still runs.
		return "", fmt.Errorf("echproxy: chmod %s: %w", bin, err)
	}
	if err := os.WriteFile(side, []byte(want+"\n"), 0o600); err != nil {
		// The binary is installed and correct; a missing sidecar only costs one extra
		// network round trip on the next start, so do not roll the install back.
		return bin, fmt.Errorf("echproxy: write sidecar %s: %w (binary is installed and verified)", side, err)
	}
	return bin, nil
}

func (d *Downloader) fetchChecksums(ctx context.Context) (map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.ChecksumURL(), nil)
	if err != nil {
		return nil, fmt.Errorf("echproxy: build checksums request: %w", err)
	}
	body, err := d.do(req)
	if err != nil {
		return nil, err
	}
	sums, err := ParseChecksums(body)
	if err != nil {
		return nil, fmt.Errorf("echproxy: parse %s: %w", d.ChecksumURL(), err)
	}
	return sums, nil
}

// download streams the asset to a temp file in InstallDir (same filesystem as the final
// path, so the rename is atomic) and returns its sha256 hex plus the temp path.
func (d *Downloader) download(ctx context.Context) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.AssetURL(), nil)
	if err != nil {
		return "", "", fmt.Errorf("echproxy: build asset request: %w", err)
	}
	resp, err := d.Client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("echproxy: download %s: %w", d.AssetURL(), err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", "", fmt.Errorf("echproxy: %s returned HTTP %d: %s",
			d.AssetURL(), resp.StatusCode, strings.TrimSpace(string(snippet)))
	}

	tmp, err := os.CreateTemp(d.InstallDir, ".ech-proxy-*.tmp")
	if err != nil {
		return "", "", fmt.Errorf("echproxy: create temp file in %s: %w", d.InstallDir, err)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}

	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, h), resp.Body)
	if cerr := tmp.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", "", fmt.Errorf("echproxy: write asset %s: %w", d.AssetURL(), err)
	}
	if written == 0 {
		cleanup()
		return "", "", fmt.Errorf("echproxy: %s downloaded 0 bytes", d.AssetURL())
	}
	return hex.EncodeToString(h.Sum(nil)), tmpName, nil
}

func (d *Downloader) do(req *http.Request) ([]byte, error) {
	resp, err := d.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("echproxy: fetch %s: %w", req.URL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("echproxy: %s returned HTTP %d: %s",
			req.URL, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("echproxy: read %s: %w", req.URL, err)
	}
	return body, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// DefaultDownloader builds a Downloader with documented defaults for the given install dir.
func DefaultDownloader(installDir string) (*Downloader, error) {
	asset, err := DefaultAssetForCurrent()
	if err != nil {
		return nil, err
	}
	return &Downloader{
		Repo:       DefaultRepo,
		Version:    DefaultVersion,
		Asset:      asset,
		InstallDir: installDir,
		Client:     &http.Client{Timeout: 5 * time.Minute},
	}, nil
}
