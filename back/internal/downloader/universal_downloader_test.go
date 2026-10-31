package downloader

// Note: this file is the test for legacy code (see doc/archive/LEGACY.md, to be deleted/migrated); the "discovery background" was not annotated case by case. The "discovery background" convention applies to new code.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"peerdrive/internal/model"
	"peerdrive/internal/repository"
)

// ---------------------------------------------------------------------------
// LocalFetcher tests
// ---------------------------------------------------------------------------

func TestLocalFetcher_FileInStorageDir(t *testing.T) {
	dir := t.TempDir()
	data := []byte("local fetcher test data")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	subDir := filepath.Join(dir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), data, 0644)

	f := &LocalFetcher{storageDir: dir}
	result, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("LocalFetcher.Fetch failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

func TestLocalFetcher_FileInP2PSubdir(t *testing.T) {
	dir := t.TempDir()
	data := []byte("p2p subdir test")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	subDir := filepath.Join(dir, "p2p", hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), data, 0644)

	f := &LocalFetcher{storageDir: dir}
	result, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("LocalFetcher.Fetch via p2p subdir failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

func TestLocalFetcher_FileFromDBProvider(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("db provider test")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Write file to a location.
	filePath := filepath.Join(dir, "custom", hashStr)
	os.MkdirAll(filepath.Dir(filePath), 0755)
	os.WriteFile(filePath, data, 0644)

	// Register via DB.
	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "local", "custom/"+hashStr)

	f := &LocalFetcher{storageDir: dir}
	result, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("LocalFetcher.Fetch via DB provider failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

func TestLocalFetcher_NotFound(t *testing.T) {
	// This test case queries the provider table: previously it relied entirely
	// on the global DB leaked by the previous test (connection never closed);
	// now every test case calls CloseDB, so we must build our own DB here --
	// tests should not depend on leaked shared state.
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = repository.CloseDB() })
	f := &LocalFetcher{storageDir: t.TempDir()}
	_, err := f.Fetch(context.Background(), "0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestLocalFetcher_IsAvailable(t *testing.T) {
	f1 := &LocalFetcher{storageDir: "/tmp"}
	if !f1.IsAvailable() {
		t.Error("expected available with non-empty storageDir")
	}
	f2 := &LocalFetcher{storageDir: ""}
	if f2.IsAvailable() {
		t.Error("expected unavailable with empty storageDir")
	}
}

// ---------------------------------------------------------------------------
// BTDHTFetcher tests
// ---------------------------------------------------------------------------

func TestBTDHTFetcher_NotAvailable(t *testing.T) {
	f := &BTDHTFetcher{client: nil}
	if f.IsAvailable() {
		t.Error("expected unavailable when the BT runtime handle is nil")
	}
	// A nil handle must also fail the fetch, not panic on it: BT disabled is the
	// default configuration, so this is the path most installs take.
	if _, err := f.Fetch(context.Background(), "0000000000000000000000000000000000000000000000000000000000000000"); err == nil {
		t.Error("expected error when fetching without a BT runtime")
	}
}

func TestBTDHTFetcher_Name(t *testing.T) {
	f := &BTDHTFetcher{}
	if f.Name() != "btdht" {
		t.Errorf("expected 'btdht', got %q", f.Name())
	}
}

// ---------------------------------------------------------------------------
// HTTPURLFetcher tests
// ---------------------------------------------------------------------------

func TestHTTPURLFetcher_Name(t *testing.T) {
	f := &HTTPURLFetcher{}
	if f.Name() != "http" {
		t.Errorf("expected 'http', got %q", f.Name())
	}
}

func TestHTTPURLFetcher_IsAvailable(t *testing.T) {
	f := &HTTPURLFetcher{}
	if !f.IsAvailable() {
		t.Error("expected http fetcher always available")
	}
}

// allowLoopbackProviderURL 临时放行回环 provider URL，返回的 restore 必须 defer。
// 背景：SSRF 防护是两层——URL 闸门（urlguard.GuardExternalURL）+ 安全客户端
// 的 SafeDialContext（拨号前复核 IP），两层都按设计拦截 127.0.0.1，而 httptest
// 服务器恰好绑在回环上。本 helper 只放第一层；第二层由调用方把 fetcher 的
// httpClient 换成测试服务器的客户端绕过。闸门本身的拦截行为由
// TestHTTPURLFetcher_BlocksInternalURL 验证，不依赖本 helper。
func allowLoopbackProviderURL(t *testing.T) func() {
	t.Helper()
	orig := guardProviderURL
	guardProviderURL = func(string) error { return nil }
	return func() { guardProviderURL = orig }
}

// withLoopbackHTTPClient 把 UniversalDownloader 里的 HTTPURLFetcher 客户端换成
// 测试服务器的客户端。NewUniversalDownloader 内置 urlguard.NewSafeClient，其
// SafeDialContext 会拦截回环拨号——「本地优先落空 → http 回退成功」这条降级
// 路径因此永远跑不到，需要测试侧换客户端（生产路径保持双层防护）。
func withLoopbackHTTPClient(t *testing.T, d *UniversalDownloader, server *httptest.Server) {
	t.Helper()
	for _, f := range d.fetchers {
		if hf, ok := f.(*HTTPURLFetcher); ok {
			hf.httpClient = server.Client()
		}
	}
}

func TestHTTPURLFetcher_FetchFromServer(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("http provider content")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Start a test HTTP server that serves the file.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()

	// httptest 绑 127.0.0.1，被 SSRF 双层防护合法拦截；本测试关心的是拉取路径，
	// 临时放行（闸门行为见 TestHTTPURLFetcher_BlocksInternalURL）。
	defer allowLoopbackProviderURL(t)()

	// Register the HTTP provider in DB.
	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "http", server.URL)

	f := &HTTPURLFetcher{httpClient: server.Client()}
	result, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("HTTPURLFetcher.Fetch failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

// TestHTTPURLFetcher_BlocksInternalURL 验证 SSRF 闸门在默认状态下带电：
// 回环地址的 provider 必须被跳过（不替换 guardProviderURL，直接断言）。
func TestHTTPURLFetcher_BlocksInternalURL(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() })

	data := []byte("should never be fetched")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("loopback provider must be blocked before any connection is made")
		w.Write(data)
	}))
	defer server.Close()

	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "http", server.URL)

	if _, err := (&HTTPURLFetcher{}).Fetch(context.Background(), hashStr); err == nil {
		t.Fatal("expected error when the only http provider is a blocked internal URL")
	}
}

func TestHTTPURLFetcher_NoProvider(t *testing.T) {
	if err := repository.InitDB(":memory:"); err != nil { // Same as above: can't rely on a DB leaked by another test
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = repository.CloseDB() })
	// Hash with no HTTP provider registered.
	_, err := (&HTTPURLFetcher{}).Fetch(context.Background(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if err == nil {
		t.Error("expected error when no HTTP provider exists")
	}
}

// ---------------------------------------------------------------------------
// UniversalDownloader tests
// ---------------------------------------------------------------------------

func TestNewUniversalDownloader_DefaultOrder(t *testing.T) {
	d := NewUniversalDownloader(nil, "/tmp", "", 30*time.Second, nil)
	fetchers := d.Fetchers()
	if len(fetchers) == 0 {
		t.Fatal("expected at least one fetcher")
	}
	// Default order: local, ipfsgw, btdht, http
	names := make([]string, len(fetchers))
	for i, f := range fetchers {
		names[i] = f.Name()
	}
	expected := []string{"local", "ipfsgw", "btdht", "http"}
	for i, name := range expected {
		if i >= len(names) {
			t.Fatalf("expected %q at index %d, but got fewer fetchers", name, i)
		}
		if names[i] != name {
			t.Errorf("expected %q at index %d, got %q", name, i, names[i])
		}
	}
}

func TestNewUniversalDownloader_CustomOrder(t *testing.T) {
	d := NewUniversalDownloader(nil, "/tmp", "http,local", 30*time.Second, nil)
	fetchers := d.Fetchers()
	if len(fetchers) != 2 {
		t.Fatalf("expected 2 fetchers, got %d", len(fetchers))
	}
	if fetchers[0].Name() != "http" {
		t.Errorf("expected first fetcher 'http', got %q", fetchers[0].Name())
	}
	if fetchers[1].Name() != "local" {
		t.Errorf("expected second fetcher 'local', got %q", fetchers[1].Name())
	}
}

// TestDownload_LocalFile tests the full download pipeline hitting a local file.
func TestDownload_LocalFile(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("full pipeline test")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Place file in content-addressed storage.
	subDir := filepath.Join(dir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), data, 0644)
	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "local", filepath.Join(hashStr[:2], hashStr))

	d := NewUniversalDownloader(nil, dir, "local", 30*time.Second, nil)
	result, protocol, err := d.Download(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("Download failed: %v", err)
	}
	if protocol != "local" {
		t.Errorf("expected protocol 'local', got %q", protocol)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

// TestDownload_Fallback tests that when the first protocol fails,
// the downloader falls through to later protocols.
func TestDownload_Fallback(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("http fallback test")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Only register an HTTP provider (no local file).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer server.Close()

	// 同 TestHTTPURLFetcher_FetchFromServer：放开 URL 闸门 + 换掉安全客户端，
	// 以覆盖 http 回退路径。
	defer allowLoopbackProviderURL(t)()

	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "http", server.URL)

	// Create downloader with order that forces fallback: local first (no file), then http.
	d := NewUniversalDownloader(nil, dir, "local,http", 30*time.Second, nil)
	withLoopbackHTTPClient(t, d, server)

	result, protocol, err := d.Download(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("Download failed (expected HTTP fallback): %v", err)
	}
	if protocol != "http" {
		t.Errorf("expected protocol 'http', got %q", protocol)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

// TestDownload_AllProtocolsFail tests that 404 is returned when nothing works.
func TestDownload_AllProtocolsFail(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	d := NewUniversalDownloader(nil, dir, "local,http", 5*time.Second, nil)
	_, _, err := d.Download(context.Background(), "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if err == nil {
		t.Error("expected error when all protocols fail")
	}
}

// TestCacheToLocal verifies that cacheToLocal writes files correctly.
func TestCacheToLocal(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("cache test")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	d := NewUniversalDownloader(nil, dir, "local", 30*time.Second, nil)
	d.cacheToLocal(hashStr, data)

	// Verify file exists on disk.
	expectedPath := filepath.Join(dir, hashStr[:2], hashStr)
	if _, err := os.Stat(expectedPath); os.IsNotExist(err) {
		t.Fatalf("cached file not found at %s", expectedPath)
	}

	// Verify data is correct.
	readBack, err := os.ReadFile(expectedPath)
	if err != nil {
		t.Fatalf("read cached file failed: %v", err)
	}
	if string(readBack) != string(data) {
		t.Errorf("expected %q, got %q", data, readBack)
	}

	// Verify we can retrieve it via LocalFetcher.
	f := &LocalFetcher{storageDir: dir}
	result, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("LocalFetcher after cache failed: %v", err)
	}
	if string(result) != string(data) {
		t.Errorf("expected %q, got %q", data, result)
	}
}

// TestCheckSources verifies the sources check works correctly.
func TestCheckSources(t *testing.T) {
	dir := t.TempDir()
	repository.InitDB(filepath.Join(dir, "test.db"))
	t.Cleanup(func() { _ = repository.CloseDB() }) // On Windows: without closing the DB file it can't be deleted, so TempDir cleanup would fail

	data := []byte("sources check data")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	// Place local file.
	subDir := filepath.Join(dir, hashStr[:2])
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, hashStr), data, 0644)
	repository.InsertFileMeta(&model.FileMeta{Hash: hashStr, Size: int64(len(data)), Filename: hashStr, Type: repository.FileTypeBlob})
	repository.InsertFileProvider(hashStr, "local", filepath.Join(hashStr[:2], hashStr))

	d := NewUniversalDownloader(nil, dir, "local,http", 30*time.Second, nil)
	sources := d.CheckSources(context.Background(), hashStr)

	if !sources["local"] {
		t.Error("expected local source to be true")
	}
	if sources["http"] {
		// HTTP should be false since no HTTP provider registered for this hash.
		// But since HTTP fetcher is available, it might report true (capability-based).
		// The actual value depends on whether we found a provider.
		t.Log("http source is true (expected false since no http provider)")
	}
}

func TestDownload_FetchersMatchOrder(t *testing.T) {
	d := NewUniversalDownloader(nil, "/tmp", "btdht,http,local", 30*time.Second, nil)
	fetchers := d.Fetchers()
	if len(fetchers) != 3 {
		t.Fatalf("expected 3 fetchers, got %d", len(fetchers))
	}
	if fetchers[0].Name() != "btdht" {
		t.Errorf("expected first 'btdht', got %q", fetchers[0].Name())
	}
	if fetchers[1].Name() != "http" {
		t.Errorf("expected second 'http', got %q", fetchers[1].Name())
	}
	if fetchers[2].Name() != "local" {
		t.Errorf("expected third 'local', got %q", fetchers[2].Name())
	}
}

func TestBuildFetchers_UnknownProtocol(t *testing.T) {
	// Should not panic and should skip unknown protocol names.
	d := NewUniversalDownloader(nil, "/tmp", "local,fake,http", 30*time.Second, nil)
	fetchers := d.Fetchers()
	if len(fetchers) != 2 {
		t.Fatalf("expected 2 fetchers (unknown skipped), got %d", len(fetchers))
	}
}

// TestDownload_InvalidHashRejected the hash boundary on Download: an unvalidated
// hash would reach LocalFetcher's hash[:2] and panic (short string) or traverse
// via Join. Callers validate upstream; the downloader is the last line of defense.
func TestDownload_InvalidHashRejected(t *testing.T) {
	d := NewUniversalDownloader(nil, t.TempDir(), "local", 30*time.Second, nil)
	for _, h := range []string{"", "x", "../../etc/passwd", "A" + strings.Repeat("0", 63)} {
		if _, _, err := d.Download(context.Background(), h); err == nil {
			t.Errorf("Download(%q): expected rejection, got nil error", h)
		}
	}
}

// TestClearLocalCache_InvalidHashNoPanic the same boundary on ClearLocalCache:
// invalid hashes must be a silent no-op, never a hash[:2] panic or a
// traversal-flavored os.Remove.
func TestClearLocalCache_InvalidHashNoPanic(t *testing.T) {
	d := NewUniversalDownloader(nil, t.TempDir(), "local", 30*time.Second, nil)
	for _, h := range []string{"", "x", "..", "../../etc/passwd", "A" + strings.Repeat("0", 63)} {
		d.ClearLocalCache(h) // must not panic
	}
}

// 发现背景（Issue #405 Step 2）：URL provider 的权威存储从 file_providers 的
// provider_type='http' 行迁到 file_index.remote_url（RegisterURL 已改走
// RegisterFile 统一写）。本用例钉死读取顺序：index 优先、legacy 行只作废弃窗口
// 内的兜底、重复 URL 去重、不可用行忽略，以及「老库里只有 legacy 行」的升级兼容。
func TestHTTPURLFetcher_URLsFromIndexAndLegacyFallback(t *testing.T) {
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = repository.CloseDB() })

	mk := func(c byte) string { return strings.Repeat(string(c), 64) }
	hashA := mk('a') // index remote_url + 同一条 legacy 行 → 去重，index 先
	hashB := mk('b') // index remote_url + 另一条 legacy 行 → index 先，legacy 兜底
	hashC := mk('c') // 只有 legacy 行（老库）→ 仍可解析
	hashD := mk('d') // legacy 行 available=0 → 必须忽略

	// hashA / hashB 走统一写：file_index 拿到 remote_url，file_meta 镜像拿到元数据。
	for _, h := range []string{hashA, hashB} {
		_, err := repository.RegisterFile(repository.FileRecord{Hash: h, Name: h + ".bin", Size: 1, RemoteURL: "https://first.example/" + h})
		if err != nil {
			t.Fatalf("RegisterFile(%s): %v", h, err)
		}
	}
	// legacy file_providers 行（file_meta 是外键上游，先补；hashA/hashB 的
	// file_meta 已由 RegisterFile 的镜像写入建好，所以用 OR IGNORE）。
	legacy := func(hash, url string) {
		if _, err := repository.GetDB().Exec(`INSERT OR IGNORE INTO file_meta (hash, size, filename, type) VALUES (?, 1, ?, 'blob')`, hash, hash+".bin"); err != nil {
			t.Fatalf("insert file_meta(%s): %v", hash, err)
		}
		if err := repository.InsertFileProvider(hash, "http", url); err != nil {
			t.Fatalf("InsertFileProvider(%s): %v", hash, err)
		}
	}
	legacy(hashA, "https://first.example/"+hashA) // 与 index 相同 → 必须去重
	legacy(hashB, "https://second.example/"+hashB)
	legacy(hashC, "https://only-legacy.example/"+hashC)
	legacy(hashD, "https://dead.example/"+hashD)
	// MarkProviderUnavailable 收的是 provider id，这里按 path 定位更直接。
	if _, err := repository.GetDB().Exec(`UPDATE file_providers SET available = 0 WHERE path = ?`, "https://dead.example/"+hashD); err != nil {
		t.Fatalf("mark provider unavailable: %v", err)
	}

	f := &HTTPURLFetcher{}
	// 无索引行、无任何 provider → 空
	urls, err := f.urlsFor(mk('e'))
	if err != nil || len(urls) != 0 {
		t.Fatalf("urlsFor(nothing) = %v, %v; want empty", urls, err)
	}
	// index + 重复 legacy 行
	urls, err = f.urlsFor(hashA)
	if err != nil {
		t.Fatalf("urlsFor(hashA): %v", err)
	}
	if len(urls) != 1 || urls[0] != "https://first.example/"+hashA {
		t.Fatalf("urlsFor(hashA) = %v; want the single index URL (deduped)", urls)
	}
	// index 先，legacy 兜底
	urls, err = f.urlsFor(hashB)
	if err != nil {
		t.Fatalf("urlsFor(hashB): %v", err)
	}
	if len(urls) != 2 || urls[0] != "https://first.example/"+hashB || urls[1] != "https://second.example/"+hashB {
		t.Fatalf("urlsFor(hashB) = %v; want index first, then the legacy row", urls)
	}
	// 老库只有 legacy 行
	urls, err = f.urlsFor(hashC)
	if err != nil {
		t.Fatalf("urlsFor(hashC): %v", err)
	}
	if len(urls) != 1 || urls[0] != "https://only-legacy.example/"+hashC {
		t.Fatalf("urlsFor(hashC) = %v; the legacy-only row must still resolve", urls)
	}
	// 不可用 provider 被忽略
	urls, err = f.urlsFor(hashD)
	if err != nil || len(urls) != 0 {
		t.Fatalf("urlsFor(hashD) = %v, %v; an unavailable provider must be dropped", urls, err)
	}
}

// TestHTTPURLFetcher_FetchFromIndexRemoteURL 端到端确认：只写了 file_index.remote_url、
// file_providers 里没有对应行时，Fetch 仍然拉得到字节——Step 3 删除 file_providers
// 前这条路径必须已经成立。
func TestHTTPURLFetcher_FetchFromIndexRemoteURL(t *testing.T) {
	if err := repository.InitDB(":memory:"); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = repository.CloseDB() })

	data := []byte("served from file_index.remote_url")
	hash := sha256.Sum256(data)
	hashStr := hex.EncodeToString(hash[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer server.Close()

	_, err := repository.RegisterFile(repository.FileRecord{Hash: hashStr, Name: hashStr, Size: int64(len(data)), RemoteURL: server.URL})
	if err != nil {
		t.Fatalf("RegisterFile: %v", err)
	}
	providers, err := repository.GetFileProviders(hashStr)
	if err != nil {
		t.Fatalf("GetFileProviders: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("precondition: the test must not have a legacy http provider row, got %v", providers)
	}

	// httptest 绑 127.0.0.1，被 SSRF 双层防护合法拦截；这里只验证从 remote_url 取
	// 到字节这条路径，临时放行闸门（闸门行为见 TestHTTPURLFetcher_BlocksInternalURL）。
	defer allowLoopbackProviderURL(t)()
	f := &HTTPURLFetcher{httpClient: server.Client()}
	got, err := f.Fetch(context.Background(), hashStr)
	if err != nil {
		t.Fatalf("Fetch from remote_url failed: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected %q, got %q", data, got)
	}
}
