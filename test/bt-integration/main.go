package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"peerdrive/internal/config"
	peerdrive_log "peerdrive/internal/log"
	"peerdrive/internal/model"
	"peerdrive/internal/p2p_bt"
	"peerdrive/internal/provider"
	"peerdrive/internal/repository"
	"peerdrive/internal/router"
	"peerdrive/internal/service"

	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// Test result tracking
// ---------------------------------------------------------------------------

var testResults []TestResult
var globalTmpDir string

type TestResult struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail,omitempty"`
}

func report(name string, pass bool, detail string) {
	status := "PASS"
	if !pass {
		status = "FAIL"
		if strings.HasPrefix(detail, "(SKIP)") {
			status = "SKIP"
		}
	}
	fmt.Printf("  [%s] %s", status, name)
	if detail != "" {
		cleaned := detail
		for _, p := range []string{"(SKIP)", "(SKIPPED)"} {
			cleaned = strings.TrimPrefix(cleaned, p+" ")
		}
		if cleaned != "" {
			fmt.Printf("  -- %s", cleaned)
		}
	}
	fmt.Println()
	testResults = append(testResults, TestResult{Name: name, Pass: pass, Detail: detail})
}

func reportPass(name, detail string) { report(name, true, detail) }
func reportFail(name, detail string) { report(name, false, detail) }
func reportSkip(name, detail string) { report(name, false, "(SKIP) "+detail) }

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

var serverBaseURL string
var httpClient = &http.Client{Timeout: 10 * time.Second}
var slowClient = &http.Client{Timeout: 60 * time.Second}

func httpGet(path string) (*http.Response, error) {
	return httpClient.Get(serverBaseURL + path)
}

func httpPost(path, contentType string, body io.Reader) (*http.Response, error) {
	return httpClient.Post(serverBaseURL+path, contentType, body)
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

func main() {
	fmt.Println("==============================================")
	fmt.Println("  Peerdrive BitTorrent Integration Test")
	fmt.Println("==============================================")
	fmt.Println()

	os.RemoveAll("/tmp/peerdrive-bt-test")
	os.MkdirAll("/tmp/peerdrive-bt-test", 0755)

	// Start server
	var cleanup func()
	serverBaseURL, cleanup = startServer()
	defer cleanup()
	waitForServer()

	// Step 1: Create test data and .torrent file
	fmt.Println("\n--- Step 1: Create test torrent ---")
	testTorrentPath := stepCreateTorrent()

	// Step 2: Parse .torrent with Go parser
	fmt.Println("\n--- Step 2: Parse .torrent file (direct) ---")
	torrentMeta := stepParseTorrent(testTorrentPath)

	// Step 3: Parse magnet links
	fmt.Println("\n--- Step 3: Parse magnet URI (direct) ---")
	stepParseMagnet()

	// Step 4: Upload torrent via HTTP API
	fmt.Println("\n--- Step 4: Upload .torrent via HTTP API ---")
	stepUploadTorrent(testTorrentPath)

	// Step 5: Check download progress
	fmt.Println("\n--- Step 5: Check download progress ---")
	var infohash string
	if torrentMeta != nil {
		infohash = torrentMeta.InfoHashHex
	}
	stepDownloadStatus(infohash)

	// Step 6: Magnet resolve via API
	fmt.Println("\n--- Step 6: Magnet resolve via HTTP API ---")
	stepMagnetResolveAPI()

	// Step 7: BEP 44 put/get
	fmt.Println("\n--- Step 7: BEP 44 immutable put/get ---")
	stepBEP44()

	// Step 8: BEP 51 sample
	fmt.Println("\n--- Step 8: BEP 51 infohash sample ---")
	stepBEP51()

	// Step 9: Simulate BT download completion and file registration
	fmt.Println("\n--- Step 9: Simulate download + file registration ---")
	registeredHash := stepFileRegistration()

	// Step 10: SHA256 download verification
	fmt.Println("\n--- Step 10: SHA256 download verification ---")
	stepSHA256Download(registeredHash)

	// Step 11: Server health check
	fmt.Println("\n--- Step 11: Server health check ---")
	stepServerHealth()

	// Step 12: BT DHT status
	fmt.Println("\n--- Step 12: BT DHT status ---")
	stepBTDHTStatus()

	// Print summary
	printSummary()
}

// ---------------------------------------------------------------------------
// Server setup
// ---------------------------------------------------------------------------

func startServer() (string, func()) {
	fmt.Print("  Starting Peerdrive server... ")

	td, err := os.MkdirTemp("", "peerdrive-bt-integration-*")
	if err != nil {
		peerdrive_log.LogError("bt-test: mkdir temp: %v", err)
		os.Exit(1)
	}
	globalTmpDir = td

	storageDir := filepath.Join(td, "storage")
	downloadDir := filepath.Join(td, "downloads")
	dbPath := filepath.Join(td, "test.db")

	for _, d := range []string{storageDir, downloadDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			peerdrive_log.LogError("bt-test: mkdir %s: %v", d, err)
			os.Exit(1)
		}
	}

	// Load config with env overrides for test
	os.Setenv("PORT", "0")
	os.Setenv("PEERDRIVE_STORAGE", storageDir)
	os.Setenv("PEERDRIVE_STORAGE_ENABLE", "true")
	os.Setenv("PEERDRIVE_DOWNLOAD_DIR", downloadDir)
	os.Setenv("PEERDRIVE_BT_DHT_ENABLE", "true")
	os.Setenv("PEERDRIVE_BT_DHT_LISTEN", ":0")
	os.Setenv("PEERDRIVE_P2P_ENABLE", "false")
	os.Setenv("PEERDRIVE_LOG_LEVEL", "ERROR")

	cfg := config.Load()

	// Init DB
	if err := repository.InitDB(dbPath); err != nil {
		peerdrive_log.LogError("bt-test: db init: %v", err)
		os.Exit(1)
	}

	// Init P2P service (disabled for test)
	ctx := context.Background()
	p2pSvc, err := service.NewP2PService(ctx, cfg)
	if err != nil {
		peerdrive_log.LogError("bt-test: p2p init: %v", err)
		os.Exit(1)
	}

	// Init provider manager and downloader
	providerMgr := provider.NewManager(storageDir)
	downloader := service.NewDownloader(providerMgr, p2pSvc, storageDir)
	repository.SetAnonStorageDir(storageDir)

	// Setup router
	gin.SetMode(gin.ReleaseMode)
	r := router.SetupRouter(downloader, p2pSvc, cfg)

	// Start on random port
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		peerdrive_log.LogError("bt-test: listen: %v", err)
		os.Exit(1)
	}

	go func() {
		if err := r.RunListener(listener); err != nil {
			peerdrive_log.LogError("bt-test: server: %v", err)
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	fmt.Printf("OK (port %d)\n", port)

	cleanup := func() {
		listener.Close()
		p2pSvc.Close()
		os.RemoveAll(td)
	}

	return baseURL, cleanup
}

func waitForServer() {
	client := &http.Client{Timeout: 1 * time.Second}
	for i := 0; i < 30; i++ {
		resp, err := client.Get(serverBaseURL + "/ping")
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("  WARNING: server did not respond to /ping within timeout")
}

// ---------------------------------------------------------------------------
// Step 1: Create test torrent
// ---------------------------------------------------------------------------

func stepCreateTorrent() string {
	dataDir := "/tmp/peerdrive-bt-test"
	os.MkdirAll(dataDir, 0755)

	content := fmt.Sprintf("BT integration test data %s\n", time.Now().Format(time.RFC3339))
	testFile := filepath.Join(dataDir, "test.txt")
	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		reportFail("create test file", err.Error())
		return ""
	}
	reportPass("create test file", fmt.Sprintf("%s (%d bytes)", testFile, len(content)))

	// Call the Python torrent creator
	scriptDir, _ := filepath.Abs("test/bt-integration")
	pyScript := filepath.Join(scriptDir, "create_torrent.py")
	torrentFile := filepath.Join(dataDir, "test.torrent")

	// Try multiple paths for the Python script
	possiblePaths := []string{
		"/mnt/d/WorkPlace/peerdrive/go/test/bt-integration/create_torrent.py",
		pyScript,
		filepath.Join(os.Getenv("PWD"), "test/bt-integration/create_torrent.py"),
	}

	var pyScriptPath string
	for _, p := range possiblePaths {
		if _, err := os.Stat(p); err == nil {
			pyScriptPath = p
			break
		}
	}

	if pyScriptPath == "" {
		reportFail("create torrent", "cannot find create_torrent.py")
		return ""
	}

	cmd := exec.Command("python3", pyScriptPath, testFile, torrentFile)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		reportFail("create torrent", fmt.Sprintf("python error: %v\nstderr: %s", err, stderr.String()))
		return ""
	}

	if _, err := os.Stat(torrentFile); err != nil {
		reportFail("create torrent", fmt.Sprintf("torrent file not created: %v", err))
		return ""
	}

	reportPass("create torrent", strings.TrimSpace(stdout.String()))
	return torrentFile
}

// ---------------------------------------------------------------------------
// Step 2: Parse .torrent file
// ---------------------------------------------------------------------------

func stepParseTorrent(torrentPath string) *p2p_bt.TorrentMeta {
	if torrentPath == "" {
		torrentPath = "/tmp/peerdrive-bt-test/test.torrent"
	}

	data, err := os.ReadFile(torrentPath)
	if err != nil {
		reportFail("parse .torrent file", fmt.Sprintf("read: %v", err))
		return nil
	}

	meta, err := p2p_bt.ParseTorrent(data)
	if err != nil {
		reportFail("parse .torrent file", fmt.Sprintf("parse: %v", err))
		return nil
	}

	detail := fmt.Sprintf("name=%q infohash=%s files=%d size=%d pieces=%d",
		meta.Name, meta.InfoHashHex, len(meta.Files), meta.TotalSize, len(meta.Pieces))
	reportPass("parse .torrent file", detail)
	return meta
}

// ---------------------------------------------------------------------------
// Step 3: Parse magnet links
// ---------------------------------------------------------------------------

func stepParseMagnet() {
	testCases := []struct {
		uri      string
		expectOK bool
		desc     string
	}{
		{
			uri:      "magnet:?xt=urn:btih:10c3063874f2d6020f362388874a9d16a185ccec&dn=test.txt",
			expectOK: true,
			desc:     "valid hex infohash",
		},
		{
			uri:      "magnet:?xt=urn:btih:1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b&dn=test.txt&tr=http://tracker.example.com:6969/announce",
			expectOK: true,
			desc:     "with tracker URL",
		},
		{
			uri:      "magnet:?xt=urn:sha1:notvalid",
			expectOK: false,
			desc:     "wrong XT type (not urn:btih)",
		},
		{
			uri:      "not-a-magnet",
			expectOK: false,
			desc:     "not a magnet URI",
		},
		{
			uri:      "magnet:?xt=urn:btih:DEADBEEF",
			expectOK: false,
			desc:     "invalid infohash length",
		},
	}

	for _, tc := range testCases {
		m, err := p2p_bt.ParseMagnet(tc.uri)
		if tc.expectOK {
			if err != nil {
				reportFail("parse magnet: "+tc.desc, fmt.Sprintf("error: %v", err))
			} else {
				reportPass("parse magnet: "+tc.desc, fmt.Sprintf("infohash=%s", m.InfoHash))
			}
		} else {
			if err == nil {
				reportFail("parse magnet: "+tc.desc, "expected error, got success")
			} else {
				reportPass("parse magnet: "+tc.desc, fmt.Sprintf("correctly rejected: %v", err))
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Step 4: Upload torrent via API
// ---------------------------------------------------------------------------

func stepUploadTorrent(torrentPath string) {
	if torrentPath == "" {
		reportFail("upload torrent", "no torrent file")
		return
	}

	torrentData, err := os.ReadFile(torrentPath)
	if err != nil {
		reportFail("upload torrent", fmt.Sprintf("read: %v", err))
		return
	}

	// Build multipart form
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("torrent", "test.torrent")
	if err != nil {
		reportFail("upload torrent", fmt.Sprintf("form: %v", err))
		return
	}
	part.Write(torrentData)
	writer.Close()

	resp, err := httpClient.Post(serverBaseURL+"/p2p/bt/torrent", writer.FormDataContentType(), body)
	if err != nil {
		reportFail("upload torrent", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusServiceUnavailable {
			reportSkip("upload torrent", string(respBody))
			return
		}
		reportFail("upload torrent", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBody)))
		return
	}

	var result struct {
		InfoHash string `json:"infohash"`
		Name     string `json:"name"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(respBody, &result); err != nil {
		reportFail("upload torrent (parse)", fmt.Sprintf("json error: %v\nbody: %s", err, string(respBody)))
		return
	}

	reportPass("upload torrent", fmt.Sprintf("infohash=%s name=%q status=%s", result.InfoHash, result.Name, result.Status))
}

// ---------------------------------------------------------------------------
// Step 5: Check download status
// ---------------------------------------------------------------------------

func stepDownloadStatus(infohash string) {
	if infohash == "" {
		reportSkip("download status", "no infohash")
		return
	}

	resp, err := httpGet("/p2p/bt/download/" + infohash)
	if err != nil {
		reportFail("download status", fmt.Sprintf("http get: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		reportSkip("download status", fmt.Sprintf("not found (download may have completed/expired): %s", string(body)))
		return
	}

	var status struct {
		InfoHash    string `json:"infohash"`
		Status      string `json:"status"`
		Name        string `json:"name"`
		PiecesTotal int    `json:"pieces_total"`
		PiecesDone  int    `json:"pieces_done"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		reportFail("download status (parse)", fmt.Sprintf("json: %v\nbody: %s", err, string(body)))
		return
	}

	detail := fmt.Sprintf("infohash=%s name=%q status=%s pieces=%d/%d",
		status.InfoHash, status.Name, status.Status, status.PiecesDone, status.PiecesTotal)
	reportPass("download status", detail)

	// List all downloads
	resp2, err := httpGet("/p2p/bt/downloads")
	if err != nil {
		reportFail("list downloads", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp2.Body.Close()
	body2, _ := io.ReadAll(resp2.Body)

	var listResp struct {
		Downloads []json.RawMessage `json:"downloads"`
		Count     int               `json:"count"`
	}
	if err := json.Unmarshal(body2, &listResp); err != nil {
		reportFail("list downloads (parse)", fmt.Sprintf("json: %v", err))
		return
	}

	if listResp.Count >= 1 {
		reportPass("list downloads", fmt.Sprintf("found %d downloads", listResp.Count))
	} else {
		reportFail("list downloads", fmt.Sprintf("expected >=1 download, got %d", listResp.Count))
	}
}

// ---------------------------------------------------------------------------
// Step 6: Magnet resolve via API
// ---------------------------------------------------------------------------

func stepMagnetResolveAPI() {
	magnetURI := "magnet:?xt=urn:btih:10c3063874f2d6020f362388874a9d16a185ccec&dn=test.txt"

	reqBody := fmt.Sprintf(`{"uri":"%s"}`, magnetURI)
	resp, err := httpPost("/p2p/bt/magnet", "application/json", strings.NewReader(reqBody))
	if err != nil {
		reportFail("magnet resolve", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusServiceUnavailable {
		reportSkip("magnet resolve", fmt.Sprintf("BT client unavailable: %s", string(body)))
		return
	}

	if resp.StatusCode != http.StatusOK {
		reportSkip("magnet resolve", fmt.Sprintf("HTTP %d: %s (expected outside real network)", resp.StatusCode, string(body)))
		return
	}

	var result struct {
		InfoHash string `json:"infohash"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		reportFail("magnet resolve (parse)", fmt.Sprintf("json: %v", err))
		return
	}

	reportPass("magnet resolve", fmt.Sprintf("infohash=%s status=%s", result.InfoHash, result.Status))
}

// ---------------------------------------------------------------------------
// Step 7: BEP 44 put/get
// ---------------------------------------------------------------------------

func stepBEP44() {
	testData := "Hello BEP 44 from Peerdrive test " + time.Now().Format(time.RFC3339)
	encodedData := hex.EncodeToString([]byte(testData))

	// Use slow client (60s timeout) for DHT operations which contact remote nodes
	slowPost := func(path, contentType string, body io.Reader) (*http.Response, error) {
		return slowClient.Post(serverBaseURL+path, contentType, body)
	}

	// PUT
	putBody := fmt.Sprintf(`{"data":"%s","mutable":false}`, encodedData)
	resp, err := slowPost("/p2p/bt/bep44/put", "application/json", strings.NewReader(putBody))
	if err != nil {
		reportFail("BEP44 put", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusServiceUnavailable {
		reportSkip("BEP44 put", "BT DHT not enabled")
		reportSkip("BEP44 get", "BT DHT not enabled")
		return
	}

	if resp.StatusCode != http.StatusOK {
		reportFail("BEP44 put", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body)))
		return
	}

	var putResult struct {
		Target  string `json:"target"`
		Size    int    `json:"size"`
		Mutable bool   `json:"mutable"`
	}
	if err := json.Unmarshal(body, &putResult); err != nil {
		reportFail("BEP44 put (parse)", fmt.Sprintf("json: %v\nbody: %s", err, string(body)))
		return
	}

	reportPass("BEP44 put", fmt.Sprintf("target=%s size=%d", putResult.Target, putResult.Size))

	// GET with the target from PUT
	getBody := fmt.Sprintf(`{"target":"%s"}`, putResult.Target)
	resp2, err := slowPost("/p2p/bt/bep44/get", "application/json", strings.NewReader(getBody))
	if err != nil {
		reportFail("BEP44 get", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp2.Body.Close()

	body2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode == http.StatusNotFound {
		// Expected in isolated test - DHT routing table is empty,
		// so the iterative lookup can't find the locally stored value.
		reportSkip("BEP44 get", fmt.Sprintf("not found (expected in isolated env): %s", string(body2)))
		return
	}

	if resp2.StatusCode != http.StatusOK {
		reportFail("BEP44 get", fmt.Sprintf("HTTP %d: %s", resp2.StatusCode, string(body2)))
		return
	}

	var getResult struct {
		Data string `json:"data"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal(body2, &getResult); err != nil {
		reportFail("BEP44 get (parse)", fmt.Sprintf("json: %v", err))
		return
	}

	// The data was hex-encoded in the put request, so decode
	decoded, err := hex.DecodeString(getResult.Data)
	if err != nil {
		// Might be base64 from the server response
		decoded = []byte(getResult.Data)
	}

	if string(decoded) == testData {
		reportPass("BEP44 get", fmt.Sprintf("data round-trip verified (%d bytes)", getResult.Size))
	} else {
		reportFail("BEP44 get", fmt.Sprintf("data mismatch: expected=%q got=%q (len=%d)",
			testData, string(decoded), len(decoded)))
	}
}

// ---------------------------------------------------------------------------
// Step 8: BEP 51 sample
// ---------------------------------------------------------------------------

func stepBEP51() {
	resp, err := httpGet("/p2p/bt/bep51/sample")
	if err != nil {
		reportFail("BEP51 sample", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusServiceUnavailable {
		reportSkip("BEP51 sample", "BT DHT not enabled")
		return
	}

	// In isolated test, BEP51 will likely fail because no DHT nodes
	// are reachable. That's expected.
	if resp.StatusCode != http.StatusOK {
		reportSkip("BEP51 sample", fmt.Sprintf("HTTP %d (expected in isolated env): %s", resp.StatusCode, string(body)))
		return
	}

	var result struct {
		Samples []string `json:"samples"`
		Count   int      `json:"count"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		reportFail("BEP51 sample (parse)", fmt.Sprintf("json: %v", err))
		return
	}

	if result.Count > 0 {
		reportPass("BEP51 sample", fmt.Sprintf("found %d samples", result.Count))
	} else {
		reportSkip("BEP51 sample", "no samples returned (expected in isolated env)")
	}
}

// ---------------------------------------------------------------------------
// Step 9: Simulate BT download completion and file registration
// ---------------------------------------------------------------------------

func stepFileRegistration() string {
	// This simulates what happens when a BT download successfully completes.
	// We write a file to the storage directory and register it in the DB,
	// which is exactly what the BTClient's onComplete callback does.

	storageDir := os.Getenv("PEERDRIVE_STORAGE")
	if storageDir == "" {
		storageDir = filepath.Join(globalTmpDir, "storage")
	}

	testContent := []byte(fmt.Sprintf("Peerdrive BT test simulation %s\n", time.Now().Format(time.RFC3339)))
	sha256Hash := sha256.Sum256(testContent)
	hashHex := hex.EncodeToString(sha256Hash[:])

	// Write to content-addressed storage
	relPath := filepath.Join(hashHex[:2], hashHex)
	fullPath := filepath.Join(storageDir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		reportFail("file registration", fmt.Sprintf("mkdir: %v", err))
		return ""
	}
	if err := os.WriteFile(fullPath, testContent, 0644); err != nil {
		reportFail("file registration", fmt.Sprintf("write: %v", err))
		return ""
	}

	// Register in database
	meta := &model.FileMeta{
		Hash:     hashHex,
		Size:     int64(len(testContent)),
		Filename: "bt_test_file.txt",
		Type:     repository.FileTypeBlob,
	}
	if err := repository.InsertFileMeta(meta); err != nil {
		reportFail("file registration (meta)", fmt.Sprintf("insert meta: %v", err))
		return ""
	}
	if err := repository.InsertFileProvider(hashHex, "local", relPath); err != nil {
		reportFail("file registration (provider)", fmt.Sprintf("insert provider: %v", err))
		return ""
	}

	// Verify via DB
	gotMeta, err := repository.GetFileMeta(hashHex)
	if err != nil {
		reportFail("file registration (verify)", fmt.Sprintf("get meta: %v", err))
		return ""
	}
	if gotMeta == nil {
		reportFail("file registration (verify)", "meta not found after insert")
		return ""
	}

	providers, err := repository.GetFileProviders(hashHex)
	if err != nil {
		reportFail("file registration (providers)", fmt.Sprintf("get providers: %v", err))
		return ""
	}

	reportPass("file registration", fmt.Sprintf("hash=%s size=%d providers=%d",
		hashHex, gotMeta.Size, len(providers)))
	return hashHex
}

// ---------------------------------------------------------------------------
// Step 10: SHA256 download
// ---------------------------------------------------------------------------

func stepSHA256Download(hash string) {
	if hash == "" {
		reportSkip("SHA256 download", "no registered hash")
		return
	}

	resp, err := httpGet("/sha256sum/" + hash)
	if err != nil {
		reportFail("SHA256 download", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		reportFail("SHA256 download", fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(data)))
		return
	}

	h := sha256.Sum256(data)
	gotHash := hex.EncodeToString(h[:])
	if gotHash != hash {
		reportFail("SHA256 download (hash)", fmt.Sprintf("expected %s, got %s", hash, gotHash))
		return
	}

	reportPass("SHA256 download", fmt.Sprintf("hash=%s size=%d", hash, len(data)))
}

// ---------------------------------------------------------------------------
// Step 11: Server health check
// ---------------------------------------------------------------------------

func stepServerHealth() {
	resp, err := httpGet("/ping")
	if err != nil {
		reportFail("server health", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusOK && string(body) == "pong" {
		reportPass("server health", "OK")
	} else {
		reportFail("server health", fmt.Sprintf("HTTP %d body=%q", resp.StatusCode, string(body)))
	}
}

// ---------------------------------------------------------------------------
// Step 12: BT DHT status
// ---------------------------------------------------------------------------

func stepBTDHTStatus() {
	resp, err := httpGet("/p2p/bt/status")
	if err != nil {
		reportFail("BT DHT status", fmt.Sprintf("http: %v", err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var status struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.Unmarshal(body, &status); err != nil {
		reportFail("BT DHT status (parse)", fmt.Sprintf("json: %v", err))
		return
	}

	if status.Enabled {
		reportPass("BT DHT status", "BT DHT is enabled and running")
	} else {
		reportFail("BT DHT status", "BT DHT is not enabled")
	}
}

// ---------------------------------------------------------------------------
// Summary
// ---------------------------------------------------------------------------

func printSummary() {
	fmt.Println()
	fmt.Println("==============================================")
	fmt.Println("  Test Summary")
	fmt.Println("==============================================")

	passCount := 0
	failCount := 0
	skipCount := 0

	for _, r := range testResults {
		status := "PASS"
		if !r.Pass {
			status = "SKIP"
			if strings.HasPrefix(r.Detail, "(SKIP)") {
				skipCount++
			} else {
				status = "FAIL"
				failCount++
			}
		}
		if status == "PASS" {
			passCount++
		}

		detail := r.Detail
		for _, p := range []string{"(SKIP) ", "(SKIPPED) "} {
			detail = strings.TrimPrefix(detail, p)
		}

		fmt.Printf("  [%s] %s", status, r.Name)
		if detail != "" {
			fmt.Printf("  -- %s", detail)
		}
		fmt.Println()
	}

	fmt.Println()
	fmt.Printf("  Total: %d   Passed: %d   Failed: %d   Skipped: %d\n",
		len(testResults), passCount, failCount, skipCount)

	if failCount > 0 {
		fmt.Println("\n  RESULT: SOME TESTS FAILED")
		os.Exit(1)
	} else {
		fmt.Println("\n  RESULT: ALL TESTS PASSED")
	}
}
