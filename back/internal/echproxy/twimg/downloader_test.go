package twimg

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

const assetName = "ech-proxy-windows-amd64.exe"

// TestParseChecksums covers the sha256sum format, including the binary-mode "*" prefix and
// malformed lines that must fail loudly (a silently dropped entry would masquerade as
// "asset not found").
func TestParseChecksums(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		in := "f9ff0e84d4187b4cb53b81570b831131f7bee084c21f7aae541595d1a3a1f2b6  ech-proxy-windows-amd64.exe\n" +
			"9d8e6936802737fb2b883e388cafbdc8f19de5308e7c5ace6f099a839d813da3  ech-proxy-windows-arm64.exe\n" +
			"# comment line\n\n"
		got, err := ParseChecksums([]byte(in))
		require.NoError(t, err)
		assert.Len(t, got, 2)
		assert.Equal(t, "f9ff0e84d4187b4cb53b81570b831131f7bee084c21f7aae541595d1a3a1f2b6", got["ech-proxy-windows-amd64.exe"])
		assert.Equal(t, "9d8e6936802737fb2b883e388cafbdc8f19de5308e7c5ace6f099a839d813da3", got["ech-proxy-windows-arm64.exe"])
	})
	t.Run("binary mode star prefix", func(t *testing.T) {
		got, err := ParseChecksums([]byte("*" + strings.Repeat("a", 64) + "  file.exe\n"))
		require.NoError(t, err)
		assert.Equal(t, strings.Repeat("a", 64), got["file.exe"])
	})
	t.Run("hash lowercased", func(t *testing.T) {
		upper := "ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"
		got, err := ParseChecksums([]byte(upper + "  f\n"))
		require.NoError(t, err)
		assert.Equal(t, strings.ToLower(upper), got["f"])
	})
	for _, tc := range []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"comments only", "# only comments\n"},
		{"short line", "nothash\n"},
		{"bad hash chars", "zzz  file.exe\n"},
		{"short hash", "abc123  file.exe\n"},
		{"empty name", strings.Repeat("a", 64) + "  \n"},
	} {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			_, err := ParseChecksums([]byte(tc.in))
			require.Error(t, err)
		})
	}
}

// fakeReleaseServer stands in for the GitHub release endpoint.
type fakeReleaseServer struct {
	server           *httptest.Server
	sums             map[string]string
	bodies           map[string][]byte
	checksumStatus   int
	assetStatus      int
	checksumsFetched atomic.Int64
	assetsFetched    atomic.Int64
}

func newFakeReleaseServer(t *testing.T, sums map[string]string, bodies map[string][]byte) *fakeReleaseServer {
	t.Helper()
	f := &fakeReleaseServer{sums: sums, bodies: bodies, checksumStatus: 200, assetStatus: 200}
	var b strings.Builder
	for name, digest := range sums {
		b.WriteString(digest + "  " + name + "\n")
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		segment := filepath.Base(r.URL.Path)
		switch segment {
		case ChecksumFile:
			f.checksumsFetched.Add(1)
			if f.checksumStatus != 200 {
				http.Error(w, "forced failure", f.checksumStatus)
				return
			}
			_, _ = io.WriteString(w, b.String())
		default:
			f.assetsFetched.Add(1)
			if f.assetStatus != 200 {
				http.Error(w, "forced failure", f.assetStatus)
				return
			}
			body, ok := f.bodies[segment]
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write(body)
		}
	}))
	return f
}

// client returns an http.Client that redirects the github.com release URLs onto the fake
// server, preserving the URL path.
func (f *fakeReleaseServer) client() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: redirectingRoundTripper{base: http.DefaultTransport,
			target: f.server.Listener.Addr().String()},
	}
}

// redirectingRoundTripper rewrites the request authority onto the fake server while
// keeping the path, so the downloader's URL construction is exercised unchanged.
type redirectingRoundTripper struct {
	base   http.RoundTripper
	target string
}

func (r redirectingRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.URL.Scheme = "http"
	clone.URL.Host = r.target
	clone.Host = r.target
	return r.base.RoundTrip(clone)
}

func newFakeDownloader(t *testing.T, dir string, f *fakeReleaseServer, asset string) *Downloader {
	t.Helper()
	return &Downloader{
		Repo:       "Hana-ame/ech-proxy",
		Version:    "v1.3.0",
		Asset:      asset,
		InstallDir: dir,
		Client:     f.client(),
	}
}

// TestDownloader_Ensure downloads the asset, verifies it against checksums.txt, installs
// it and writes the sidecar.
func TestDownloader_Ensure(t *testing.T) {
	body := []byte("MZ\x00\x00binary-bytes")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	bin, err := d.Ensure(context.Background())
	require.NoError(t, err)

	require.Equal(t, filepath.Join(d.InstallDir, assetName), bin)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, body, data)

	// Sidecar records the digest; it is what makes repeat starts network-free.
	side, err := os.ReadFile(d.SidecarPath())
	require.NoError(t, err)
	assert.Equal(t, sum+"\n", string(side))

	fi, err := os.Stat(bin)
	require.NoError(t, err)
	assert.NotZero(t, fi.Mode().Perm()&0o111, "the binary must be executable")
}

// TestDownloader_Ensure_NoRedownload ensures a verified binary is reused without touching
// the network — but the file hash is still recomputed against the sidecar.
func TestDownloader_Ensure_NoRedownload(t *testing.T) {
	body := []byte("payload-v1")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	_, err := d.Ensure(context.Background())
	require.NoError(t, err)
	afterFirst := f.checksumsFetched.Load()
	require.EqualValues(t, 1, afterFirst)
	require.EqualValues(t, 1, f.assetsFetched.Load())

	bin, err := d.Ensure(context.Background())
	require.NoError(t, err)
	require.Equal(t, filepath.Join(d.InstallDir, assetName), bin)
	assert.EqualValues(t, 1, f.checksumsFetched.Load(), "second run must not fetch checksums.txt")
	assert.EqualValues(t, 1, f.assetsFetched.Load(), "second run must not re-download the asset")
}

// TestDownloader_Ensure_HashDrift re-downloads when the installed file no longer matches
// the sidecar (edited binary, or a rebuilt release tag).
func TestDownloader_Ensure_HashDrift(t *testing.T) {
	body := []byte("payload-v1")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	bin, err := d.Ensure(context.Background())
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, []byte("tampered"), 0o755))

	_, err = d.Ensure(context.Background())
	require.NoError(t, err)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, body, data, "a drifted binary must be replaced by the verified one")
}

// TestDownloader_Ensure_SidecarWithoutFile ensures a stale sidecar alone is never trusted
// as proof of a verified binary.
func TestDownloader_Ensure_SidecarWithoutFile(t *testing.T) {
	body := []byte("payload")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	require.NoError(t, os.MkdirAll(d.InstallDir, 0o755))
	require.NoError(t, os.WriteFile(d.SidecarPath(), []byte(sum+"\n"), 0o600))

	_, err := d.Ensure(context.Background())
	require.NoError(t, err)
	_, statErr := os.Stat(d.BinaryPath())
	assert.NoError(t, statErr, "the binary must have been downloaded despite the sidecar")
}

// TestDownloader_Ensure_ChecksumMismatch is the security-critical case: the downloaded
// bytes do not match checksums.txt. The error must be explicit and no binary — verified
// or not — may be left installed, and no temp file may leak.
func TestDownloader_Ensure_ChecksumMismatch(t *testing.T) {
	body := []byte("tampered-bytes")
	wrongSum := strings.Repeat("0", 64)
	f := newFakeReleaseServer(t, map[string]string{assetName: wrongSum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	_, err := d.Ensure(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256 mismatch")
	_, statErr := os.Stat(d.BinaryPath())
	assert.True(t, os.IsNotExist(statErr), "no unverified binary may remain: %v", statErr)
	entries, rerr := os.ReadDir(d.InstallDir)
	require.NoError(t, rerr)
	assert.Empty(t, entries, "no temp file may leak")
}

// TestDownloader_Ensure_AssetMissing covers "no asset available": the release exists but
// does not publish this platform's binary. The error names what is available.
func TestDownloader_Ensure_AssetMissing(t *testing.T) {
	f := newFakeReleaseServer(t,
		map[string]string{"ech-proxy-linux-amd64": strings.Repeat("a", 64)},
		map[string][]byte{"ech-proxy-linux-amd64": []byte("linux")})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	_, err := d.Ensure(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not listed in checksums.txt")
	assert.Contains(t, err.Error(), "ech-proxy-linux-amd64", "available assets should be named")
}

// TestDownloader_Ensure_PinnedChecksumMismatch covers the operator pin: checksums.txt and
// ExpectedSHA256 disagree → refuse before downloading.
func TestDownloader_Ensure_PinnedChecksumMismatch(t *testing.T) {
	body := []byte("payload")
	sum := sha256Hex(body)
	f := newFakeReleaseServer(t, map[string]string{assetName: sum}, map[string][]byte{assetName: body})
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	d.ExpectedSHA256 = strings.Repeat("b", 64)
	_, err := d.Ensure(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checksum pin mismatch")
	assert.EqualValues(t, 0, f.assetsFetched.Load(), "must not download once the pin disagrees")
}

// TestDownloader_Ensure_ChecksumHTTPError reports the HTTP status instead of swallowing it.
func TestDownloader_Ensure_ChecksumHTTPError(t *testing.T) {
	body := []byte("payload")
	f := newFakeReleaseServer(t, map[string]string{assetName: sha256Hex(body)}, map[string][]byte{assetName: body})
	f.checksumStatus = 404
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	_, err := d.Ensure(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 404")
}

// TestDownloader_Ensure_AssetHTTPError reports the HTTP status and leaves no partial install.
func TestDownloader_Ensure_AssetHTTPError(t *testing.T) {
	body := []byte("payload")
	f := newFakeReleaseServer(t, map[string]string{assetName: sha256Hex(body)}, map[string][]byte{assetName: body})
	f.assetStatus = 500
	defer f.server.Close()

	d := newFakeDownloader(t, t.TempDir(), f, assetName)
	_, err := d.Ensure(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 500")
	entries, rerr := os.ReadDir(d.InstallDir)
	assert.True(t, os.IsNotExist(rerr) || len(entries) == 0, "no leftover files")
}

// TestDownloader_Ensure_Validation rejects bad configuration before any I/O.
func TestDownloader_Ensure_Validation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		mut   func(*Downloader)
		errIn string
	}{
		{"empty repo", func(d *Downloader) { d.Repo = "" }, "Repo is empty"},
		{"empty version", func(d *Downloader) { d.Version = "" }, "Version is empty"},
		{"empty asset", func(d *Downloader) { d.Asset = "" }, "Asset is empty"},
		{"empty dir", func(d *Downloader) { d.InstallDir = "" }, "InstallDir is empty"},
		{"nil client", func(d *Downloader) { d.Client = nil }, "Client is nil"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Downloader{Repo: "r", Version: "v", Asset: "a", InstallDir: t.TempDir(),
				Client: &http.Client{}}
			tc.mut(d)
			_, err := d.Ensure(context.Background())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.errIn)
		})
	}
}

// TestDownloader_URLs checks the GitHub release URL layout is the standard one.
func TestDownloader_URLs(t *testing.T) {
	d := &Downloader{Repo: "Hana-ame/ech-proxy", Version: "v1.3.0", Asset: assetName, InstallDir: "/x"}
	assert.Equal(t,
		"https://github.com/Hana-ame/ech-proxy/releases/download/v1.3.0/checksums.txt", d.ChecksumURL())
	assert.Equal(t,
		"https://github.com/Hana-ame/ech-proxy/releases/download/v1.3.0/ech-proxy-windows-amd64.exe", d.AssetURL())
	assert.Equal(t, "/x/ech-proxy-windows-amd64.exe.sha256", d.SidecarPath())
	assert.Equal(t, "/x/ech-proxy-windows-amd64.exe", d.BinaryPath())
}

// TestDownloader_DefaultDownloader wires the documented defaults for the current platform.
func TestDownloader_DefaultDownloader(t *testing.T) {
	d, err := DefaultDownloader(t.TempDir())
	require.NoError(t, err)
	assert.Equal(t, DefaultRepo, d.Repo)
	assert.Equal(t, DefaultVersion, d.Version)
	assert.Equal(t, ChecksumFile, filepath.Base(d.ChecksumURL()))
	asset, err := DefaultAssetForCurrent()
	require.NoError(t, err)
	assert.Equal(t, asset, d.Asset)
	assert.NotNil(t, d.Client)
}
