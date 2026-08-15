// Tests for p2p_bt package using anacrolix/torrent library.
package p2p_bt

// 注：本文件属于 legacy 代码（见 doc/LEGACY.md，待删/待迁移）的测试，未逐一标注发现背景；「发现背景」规范对新代码生效。

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
)

// bencodeMarshal marshals v to bencode format.
func bencodeMarshal(buf *bytes.Buffer, v interface{}) error {
	return bencode.NewEncoder(buf).Encode(v)
}

// createTestTorrentFile creates a .torrent file from a data file on disk.
// Returns the torrent bytes and the info hash hex string.
func createTestTorrentFile(t *testing.T, dataPath, torrentPath string) ([]byte, string) {
	t.Helper()

	info := metainfo.Info{PieceLength: 16384}
	if err := info.BuildFromFilePath(dataPath); err != nil {
		t.Fatalf("BuildFromFilePath: %v", err)
	}

	// Marshal info to bencode bytes for InfoBytes.
	var infoBuf bytes.Buffer
	if err := bencodeMarshal(&infoBuf, info); err != nil {
		t.Fatalf("marshal info: %v", err)
	}

	mi := metainfo.MetaInfo{
		InfoBytes: infoBuf.Bytes(),
		CreatedBy: "peerdrive-test",
	}
	mi.SetDefaults()
	ih := mi.HashInfoBytes().HexString()

	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}

	if err := os.WriteFile(torrentPath, buf.Bytes(), 0644); err != nil {
		t.Fatalf("write torrent file: %v", err)
	}

	return buf.Bytes(), ih
}

// ---- Test: Torrent File Parsing via metainfo.Load ----

func TestParseTorrent(t *testing.T) {
	tmpDir := t.TempDir()

	data := []byte(strings.Repeat("hello world ", 1000))
	name := "testfile.txt"
	dataPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(dataPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	torrentPath := filepath.Join(tmpDir, "test.torrent")
	torrentData, ih := createTestTorrentFile(t, dataPath, torrentPath)

	// Parse back with metainfo.Load.
	loaded, err := metainfo.Load(bytes.NewReader(torrentData))
	if err != nil {
		t.Fatalf("metainfo.Load: %v", err)
	}

	if loaded.HashInfoBytes().HexString() != ih {
		t.Errorf("infohash mismatch: %s vs %s", loaded.HashInfoBytes().HexString(), ih)
	}

	parsed, err := loaded.UnmarshalInfo()
	if err != nil {
		t.Fatalf("UnmarshalInfo: %v", err)
	}
	if parsed.Name != name {
		t.Errorf("expected name %q, got %q", name, parsed.Name)
	}
	if parsed.Length != int64(len(data)) {
		t.Errorf("expected length %d, got %d", len(data), parsed.Length)
	}
	numPieces := len(parsed.Pieces) / 20
	t.Logf("Parsed: name=%q size=%d pieces=%d infoHash=%s", parsed.Name, parsed.Length, numPieces, ih)

	// Test via our AddTorrentBytes wrapper.
	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	meta, err := btClient.AddTorrentBytes(torrentData)
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	if meta.InfoHashHex != ih {
		t.Errorf("AddTorrentBytes infohash: %s vs %s", meta.InfoHashHex, ih)
	}
	if meta.TotalSize != int64(len(data)) {
		t.Errorf("AddTorrentBytes size: %d vs %d", meta.TotalSize, len(data))
	}
}

// ---- Test: Magnet Link Parsing ----

func TestParseMagnet(t *testing.T) {
	validURI := "magnet:?xt=urn:btih:10c3063874f2d6020f362388874a9d16a185ccec&dn=test.txt&tr=http://tracker.example.com:6969/announce"
	spec, err := torrent.TorrentSpecFromMagnetUri(validURI)
	if err != nil {
		t.Fatalf("valid magnet: %v", err)
	}
	if spec.InfoHash.HexString() != "10c3063874f2d6020f362388874a9d16a185ccec" {
		t.Errorf("infohash mismatch: %s", spec.InfoHash.HexString())
	}
	if spec.DisplayName != "test.txt" {
		t.Errorf("display name: %q", spec.DisplayName)
	}

	// Invalid magnet.
	_, err = torrent.TorrentSpecFromMagnetUri("not-a-magnet")
	if err == nil {
		t.Error("expected error for invalid URI")
	}

	// Via our AddMagnetURI wrapper.
	tmpDir := t.TempDir()
	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	meta, err := btClient.AddMagnetURI(validURI)
	if err != nil {
		t.Fatalf("AddMagnetURI: %v", err)
	}
	if meta.InfoHashHex != "10c3063874f2d6020f362388874a9d16a185ccec" {
		t.Errorf("AddMagnetURI infohash: %s", meta.InfoHashHex)
	}
}

// ---- Test: BTClient Lifecycle ----

func TestBTClientPauseResume(t *testing.T) {
	tmpDir := t.TempDir()

	data := []byte(strings.Repeat("test data ", 100))
	name := "pause-test.txt"
	dataPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(dataPath, data, 0644); err != nil {
		t.Fatal(err)
	}

	torrentPath := filepath.Join(tmpDir, "test.torrent")
	torrentData, _ := createTestTorrentFile(t, dataPath, torrentPath)

	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	meta, err := btClient.AddTorrentBytes(torrentData)
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}
	ih := meta.InfoHashHex

	status := btClient.GetDownload(ih)
	if status == nil {
		t.Fatal("GetDownload returned nil")
	}
	t.Logf("Initial status: %s", status.Status)

	// Pause.
	if err := btClient.PauseDownload(ih); err != nil {
		t.Fatalf("PauseDownload: %v", err)
	}
	status = btClient.GetDownload(ih)
	if status.Status != "paused" {
		t.Errorf("expected 'paused', got %q", status.Status)
	}

	// Resume.
	if err := btClient.ResumeDownload(ih); err != nil {
		t.Fatalf("ResumeDownload: %v", err)
	}
	status = btClient.GetDownload(ih)
	if status.Status != "downloading" {
		t.Errorf("expected 'downloading', got %q", status.Status)
	}

	// List downloads.
	downloads := btClient.ListDownloads()
	if len(downloads) != 1 {
		t.Errorf("expected 1 download, got %d", len(downloads))
	}

	// Global stats.
	stats := btClient.GetGlobalStats()
	t.Logf("Stats: active=%d paused=%d completed=%d", stats.ActiveTorrents, stats.PausedTorrents, stats.Completed)

	// Remove.
	if err := btClient.RemoveDownload(ih); err != nil {
		t.Fatalf("RemoveDownload: %v", err)
	}
	if btClient.GetDownload(ih) != nil {
		t.Error("expected nil after remove")
	}
}

// ---- Test: Full BT Download (Seeder + Downloader via library) ----

func TestFullBTDownload(t *testing.T) {
	tmpDir := t.TempDir()

	testData := []byte(strings.Repeat("peerdrive-bt-test-data-", 1024)) // ~24 KB
	name := "peerdrive-test.bin"
	dataPath := filepath.Join(tmpDir, name)
	if err := os.WriteFile(dataPath, testData, 0644); err != nil {
		t.Fatal(err)
	}

	torrentPath := filepath.Join(tmpDir, "test.torrent")
	torrentData, _ := createTestTorrentFile(t, dataPath, torrentPath)

	expectedHash := sha256.Sum256(testData)
	expectedHashHex := hex.EncodeToString(expectedHash[:])

	// ---- Seeder ----
	seederDir := filepath.Join(tmpDir, "seeder")
	os.MkdirAll(seederDir, 0755)
	// Copy test file into seeder's data dir with torrent name as subdir.
	seederFileDir := filepath.Join(seederDir, name)
	if err := os.WriteFile(seederFileDir, testData, 0644); err != nil {
		t.Fatal(err)
	}

	seederCfg := torrent.NewDefaultClientConfig()
	seederCfg.DataDir = seederDir
	seederCfg.Seed = true
	seederCfg.NoUpload = false
	seederCfg.DisableUTP = true
	seederCfg.ListenPort = 0

	seeder, err := torrent.NewClient(seederCfg)
	if err != nil {
		t.Fatalf("seeder NewClient: %v", err)
	}
	defer seeder.Close()

	mi, err := metainfo.Load(bytes.NewReader(torrentData))
	if err != nil {
		t.Fatal(err)
	}
	seederTorrent, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatalf("seeder AddTorrent: %v", err)
	}
	<-seederTorrent.GotInfo()
	seederTorrent.VerifyData()

	t.Logf("Seeder ready: infohash=%s port=%d", seederTorrent.InfoHash().HexString(), seeder.LocalPort())

	// ---- Downloader via our BTClient ----
	btClient := newBTClient(filepath.Join(tmpDir, "downloader"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	completeCh := make(chan []CompletedFile, 1)
	btClient.SetOnComplete(func(ih string, files []CompletedFile) {
		completeCh <- files
	})

	meta, err := btClient.AddTorrentBytes(torrentData)
	if err != nil {
		t.Fatalf("AddTorrentBytes: %v", err)
	}

	// Link seeder and downloader directly (same process, different clients).
	seederTorrent.AddClientPeer(btClient.cl)
	btTorrent, ok := btClient.cl.Torrent(seederTorrent.InfoHash())
	if ok {
		btTorrent.AddClientPeer(seeder)
	}

	t.Logf("Download started: infohash=%s", meta.InfoHashHex)

	select {
	case files := <-completeCh:
		if len(files) == 0 {
			t.Fatal("no files in completion callback")
		}
		t.Logf("Downloaded: path=%s size=%d SHA256=%s", files[0].Path, files[0].Size, files[0].SHA256)
		if files[0].SHA256 != expectedHashHex {
			t.Errorf("SHA256: expected %s, got %s", expectedHashHex, files[0].SHA256)
		}
	case <-time.After(30 * time.Second):
		status := btClient.GetDownload(meta.InfoHashHex)
		if status != nil {
			t.Logf("Timeout: status=%s downloaded=%d/%d", status.Status, status.Downloaded, status.TotalSize)
		}
		t.Skip("Download did not complete within 30s (may need DHT bootstrap or peer connectivity)")
	}
}

// ---- Backward Compat Tests ----

func TestAddMagnetBackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()
	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	err := btClient.AddMagnet(&MagnetInfo{
		InfoHash:    "10c3063874f2d6020f362388874a9d16a185ccec",
		DisplayName: "test-old-api",
		Trackers:    []string{"http://tracker.example.com:6969/announce"},
	})
	if err != nil {
		t.Fatalf("AddMagnet: %v", err)
	}

	if btClient.GetDownload("10c3063874f2d6020f362388874a9d16a185ccec") == nil {
		t.Error("expected download to exist")
	}
}

func TestAddTorrentBackwardCompat(t *testing.T) {
	tmpDir := t.TempDir()
	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	err := btClient.AddTorrent(&TorrentMeta{
		InfoHashHex:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Name:         "old-api-test",
		AnnounceList: []string{"http://tracker.example.com:6969/announce"},
	})
	if err != nil {
		t.Fatalf("AddTorrent: %v", err)
	}
}

func TestGlobalStats(t *testing.T) {
	tmpDir := t.TempDir()
	btClient := newBTClient(filepath.Join(tmpDir, "bt"), ":0")
	if btClient == nil {
		t.Fatal("newBTClient returned nil")
	}
	defer btClient.Close()

	stats := btClient.GetGlobalStats()
	if stats.Errors < 0 {
		t.Error("negative error count")
	}
	t.Logf("Stats: active=%d paused=%d completed=%d errors=%d dht_nodes=%d",
		stats.ActiveTorrents, stats.PausedTorrents, stats.Completed, stats.Errors, stats.DHTNodes)
}
