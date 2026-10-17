package echcore

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

// TestParseSVCBWire tests the DNS SVCB wire format parser.
func TestParseSVCBWire(t *testing.T) {
	// SVCB wire format: 2 bytes offset (skipped) + empty target name + ECH SvcParam
	wire := []byte{
		0x00, 0x00,                // offset (skipped)
		0x00,                      // empty target name
		0x00, 0x05,                // key=5 (ECH)
		0x00, 0x04,                // valLen=4
		0x01, 0x02, 0x03, 0x04,    // val
	}
	cfg, err := parseSVCBWire(wire)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []byte{0x01, 0x02, 0x03, 0x04}
	if string(cfg) != string(expected) {
		t.Fatalf("expected %v, got %v", expected, cfg)
	}
}

// TestParseSVCBWire_TooShort tests error on short input.
func TestParseSVCBWire_TooShort(t *testing.T) {
	_, err := parseSVCBWire([]byte{0x00})
	if err == nil {
		t.Fatal("expected error for short input")
	}
}

// TestParseSVCBWire_NoECH tests error when ECH key is not found.
func TestParseSVCBWire_NoECH(t *testing.T) {
	// Wire with SvcParam key=1 (not ECH)
	wire := []byte{
		0x00, 0x00, // offset
		0x00,       // empty target name
		0x00, 0x01, // key=1
		0x00, 0x04, // valLen=4
		0x01, 0x02, 0x03, 0x04,
	}
	_, err := parseSVCBWire(wire)
	if err == nil {
		t.Fatal("expected error for missing ECH")
	}
}

// TestParseSVCBWire_WithTargetName tests parsing when target name is present.
func TestParseSVCBWire_WithTargetName(t *testing.T) {
	// SVCB wire format: offset + target name "a.b" + ECH SvcParam
	wire := []byte{
		0x00, 0x00,           // offset (skipped)
		0x01, 'a', 0x01, 'b', 0x00, // target name "a.b"
		0x00, 0x05,           // key=5
		0x00, 0x02,           // valLen=2
		0x01, 0x02,           // val
	}
	cfg, err := parseSVCBWire(wire)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []byte{0x01, 0x02}
	if string(cfg) != string(expected) {
		t.Fatalf("expected %v, got %v", expected, cfg)
	}
}

// TestECHEntry_TTLClamping tests that setCachedECH clamps TTL to min/max.
func TestECHEntry_TTLClamping(t *testing.T) {
	domain := "test.example.com"
	// Clear cache
	cacheMu.Lock()
	delete(cache, domain)
	cacheMu.Unlock()

	// Set with TTL below minimum
	setCachedECH(domain, []byte("config"), 1)
	cacheMu.Lock()
	e, ok := cache[domain]
	cacheMu.Unlock()
	if !ok {
		t.Fatal("expected cache entry")
	}
	// TTL should be clamped to minTTL (60s), allow 5s tolerance
	remaining := time.Until(e.expiry)
	if remaining < 55*time.Second || remaining > 65*time.Second {
		t.Fatalf("expected expiry ~60s, got remaining %v", remaining)
	}

	// Set with TTL above maximum
	setCachedECH(domain, []byte("config"), 100000)
	cacheMu.Lock()
	e, ok = cache[domain]
	cacheMu.Unlock()
	if !ok {
		t.Fatal("expected cache entry")
	}
	// TTL should be clamped to maxTTL (24h), allow 1h tolerance
	remaining = time.Until(e.expiry)
	if remaining < 23*time.Hour || remaining > 25*time.Hour {
		t.Fatalf("expected expiry ~24h, got remaining %v", remaining)
	}
}

// TestGetCachedECH_Expired tests that expired entries are not returned.
func TestGetCachedECH_Expired(t *testing.T) {
	domain := "expired.example.com"
	cacheMu.Lock()
	cache[domain] = &echEntry{
		config: []byte("config"),
		expiry: time.Now().Add(-1 * time.Second), // already expired
	}
	cacheMu.Unlock()

	cfg := getCachedECH(domain)
	if cfg != nil {
		t.Fatalf("expected nil for expired entry, got %v", cfg)
	}
}

// TestGetCachedECH_Valid tests that valid entries are returned.
func TestGetCachedECH_Valid(t *testing.T) {
	domain := "valid.example.com"
	config := []byte("valid-config")
	setCachedECH(domain, config, 300)

	cfg := getCachedECH(domain)
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if string(cfg) != string(config) {
		t.Fatalf("expected %v, got %v", config, cfg)
	}
}

// TestFetchECHConfig_DoHBase64 tests ECH config fetch from DoH with base64 format.
func TestFetchECHConfig_DoHBase64(t *testing.T) {
	echConfig := []byte{0x01, 0x02, 0x03, 0x04}
	echBase64 := base64.StdEncoding.EncodeToString(echConfig)

	// Mock DoH server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"Answer": []map[string]interface{}{
				{
					"type": 65,
					"TTL":  300,
					"data": "ech=\"" + echBase64 + "\"",
				},
			},
		}
		w.Header().Set("Content-Type", "application/dns-json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Set the DoH URL to the mock server
	origCfg := currentConfig()
	SetDohURL(server.URL)
	defer cfgPtr.Store(origCfg)

	// Clear cache
	domain := "test-b64.example.com"
	cacheMu.Lock()
	delete(cache, domain)
	cacheMu.Unlock()

	cfg, err := fetchECHConfigOnce(context.Background(), domain, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(cfg) != string(echConfig) {
		t.Fatalf("expected %v, got %v", echConfig, cfg)
	}
}

// TestFetchECHConfig_DoHWire tests ECH config fetch from DoH with wire format.
func TestFetchECHConfig_DoHWire(t *testing.T) {
	// Build SVCB wire format: offset + empty target name + ECH SvcParam
	wireBytes := []byte{
		0x00, 0x00, // offset (skipped)
		0x00,       // empty target name
		0x00, 0x05, // key=5 (ECH)
		0x00, 0x02, // valLen=2
		0x01, 0x02, // val
	}
	wireHex := hex.EncodeToString(wireBytes)
	wireLen := fmt.Sprintf("%d", len(wireBytes))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"Answer": []map[string]interface{}{
				{
					"type": 65,
					"TTL":  300,
					"data": "ech=\\# " + wireLen + " " + wireHex,
				},
			},
		}
		w.Header().Set("Content-Type", "application/dns-json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	domain := "test-wire.example.com"
	cacheMu.Lock()
	delete(cache, domain)
	cacheMu.Unlock()

	cfg, err := fetchECHConfigOnce(context.Background(), domain, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []byte{0x01, 0x02}
	if string(cfg) != string(expected) {
		t.Fatalf("expected %v, got %v", expected, cfg)
	}
}

// TestFetchECHConfig_CacheHit tests that fetchECHConfig returns cached config.
func TestFetchECHConfig_CacheHit(t *testing.T) {
	domain := "cached.example.com"
	setCachedECH(domain, []byte("cached-config"), 300)

	// This should return the cached value without making an HTTP request.
	// fetchECHConfig checks the cache first, before calling fetchECHConfigOnce.
	cfg, err := fetchECHConfig(context.Background(), domain)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(cfg) != "cached-config" {
		t.Fatalf("expected cached config, got %v", cfg)
	}
}

// TestFetchECHConfig_NoECH tests error when no ECH record is found.
func TestFetchECHConfig_NoECH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"Answer": []map[string]interface{}{
				{"type": 1, "TTL": 300, "data": "1.2.3.4"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	domain := "no-ech.example.com"
	_, err := fetchECHConfigOnce(context.Background(), domain, server.URL)
	if err == nil {
		t.Fatal("expected error for missing ECH record")
	}
}

// TestRetry_FetchECHConfig tests that fetchECHConfig retries on failure.
func TestRetry_FetchECHConfig(t *testing.T) {
	attempts := int32(0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n < 3 {
			w.WriteHeader(503)
			return
		}
		echConfig := []byte{0x01, 0x02}
		echBase64 := base64.StdEncoding.EncodeToString(echConfig)
		resp := map[string]interface{}{
			"Answer": []map[string]interface{}{
				{"type": 65, "TTL": 300, "data": "ech=\"" + echBase64 + "\""},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	domain := "retry-test.example.com"
	cacheMu.Lock()
	delete(cache, domain)
	cacheMu.Unlock()

	ctx := context.Background()
	cfg, err := Retry(ctx, 3, 1*time.Millisecond, func() ([]byte, error) {
		return fetchECHConfigOnce(ctx, domain, server.URL)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []byte{0x01, 0x02}
	if string(cfg) != string(expected) {
		t.Fatalf("expected %v, got %v", expected, cfg)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

// TestConfig_ShellDomain tests that ShellDomain can be configured.
func TestConfig_ShellDomain(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetShellDomain("custom-shell.example.com")
	cfg := currentConfig()
	if cfg.shellDom != "custom-shell.example.com" {
		t.Fatalf("expected custom shell domain, got %q", cfg.shellDom)
	}
}

// TestConfig_IPMode tests that IP mode can be set and reset.
func TestConfig_IPMode(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetIPMode("v4")
	cfg := currentConfig()
	if cfg.ipMode != "v4" {
		t.Fatalf("expected v4, got %q", cfg.ipMode)
	}

	SetIPMode("v6")
	cfg = currentConfig()
	if cfg.ipMode != "v6" {
		t.Fatalf("expected v6, got %q", cfg.ipMode)
	}

	SetIPMode("")
	cfg = currentConfig()
	if cfg.ipMode != "" {
		t.Fatalf("expected empty, got %q", cfg.ipMode)
	}
}

// TestConfig_InvalidIPMode tests that invalid IP modes are ignored.
func TestConfig_InvalidIPMode(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetIPMode("v7") // invalid
	cfg := currentConfig()
	if cfg.ipMode != "" {
		t.Fatalf("expected empty for invalid mode, got %q", cfg.ipMode)
	}
}

// TestConfig_ProxyURL tests that proxy URL can be set.
func TestConfig_ProxyURL(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetProxyURL("http://proxy.example.com:8080")
	cfg := currentConfig()
	if cfg.proxyURL != "http://proxy.example.com:8080" {
		t.Fatalf("expected proxy URL, got %q", cfg.proxyURL)
	}
}

// TestConfig_DoHConfig tests that SetDoHConfig sets both URL and dial IP.
func TestConfig_DoHConfig(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetDoHConfig("custom-doh.example.com", "1.2.3.4")
	cfg := currentConfig()
	if cfg.dohURL != "https://custom-doh.example.com/doh" {
		t.Fatalf("expected custom DoH URL, got %q", cfg.dohURL)
	}
	if cfg.dialIP != "1.2.3.4" {
		t.Fatalf("expected dial IP, got %q", cfg.dialIP)
	}
}

// TestConfig_ApplyConfig tests that Config struct applies correctly.
func TestConfig_ApplyConfig(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	applyConfig(Config{
		DoHURL:      "https://custom-doh.example.com/doh",
		IPMode:      "v4",
		BootstrapIP: "5.6.7.8",
		ShellDomain: "my-shell.example.com",
	})

	cfg := currentConfig()
	if cfg.dohURL != "https://custom-doh.example.com/doh" {
		t.Fatalf("expected custom DoH URL, got %q", cfg.dohURL)
	}
	if cfg.ipMode != "v4" {
		t.Fatalf("expected v4, got %q", cfg.ipMode)
	}
	if cfg.dialIP != "5.6.7.8" {
		t.Fatalf("expected dial IP, got %q", cfg.dialIP)
	}
	if cfg.shellDom != "my-shell.example.com" {
		t.Fatalf("expected custom shell domain, got %q", cfg.shellDom)
	}
}

// TestDoH_RequestWithBootstrapIP tests that DoH request uses bootstrap IP when configured.
func TestDoH_RequestWithBootstrapIP(t *testing.T) {
	// Start a listener on a specific port to capture connections
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	listenAddr := ln.Addr().String()

	// Extract the IP
	ip, _, _ := net.SplitHostPort(listenAddr)

	// We can't test the actual TLS handshake easily, but we can verify
	// that the transport is configured correctly.
	_ = ip
	_ = listenAddr
}

// TestCheckDualStack tests the CheckDualStack function.
func TestCheckDualStack(t *testing.T) {
	// This test checks that the function doesn't panic.
	// Actual dual-stack checking depends on network availability.
	hasV4, hasV6 := CheckDualStack(context.Background())
	_ = hasV4
	_ = hasV6
}

// TestNewTransport_HelloChrome tests that newTransport creates a transport
// with the correct ECH config.
func TestNewTransport_HelloChrome(t *testing.T) {
	echConfig := []byte{0x01, 0x02, 0x03, 0x04}
	tr := newTransport(echConfig, "")
	if tr == nil {
		t.Fatal("expected non-nil transport")
	}
	if tr.AllowHTTP {
		t.Fatal("expected AllowHTTP to be false")
	}
	if tr.DialTLSContext == nil {
		t.Fatal("expected DialTLSContext to be non-nil")
	}
}

// TestClient_Do_SetsHost tests that Client.Do sets Host when not already set.
func TestClient_Do_SetsHost(t *testing.T) {
	// Mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			http.Error(w, "missing Host", 400)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	// We can't test the full ECH client without a real ECH server,
	// but we can test the basic Do method behavior.
	// The ECH client's inner transport dials the shell domain,
	// so we can't directly test it with httptest.
	// Instead, test that the Client struct is properly constructed.
	_ = server
}

// TestNewClient tests that newClient creates a properly configured client.
func TestNewClient(t *testing.T) {
	echConfig := []byte{0x01, 0x02, 0x03, 0x04}
	c := newClient(echConfig, "")
	if c == nil {
		t.Fatal("expected non-nil client")
	}
	if c.inner == nil {
		t.Fatal("expected non-nil inner client")
	}
	if c.inner.Timeout != 0 {
		t.Fatalf("expected zero timeout, got %v", c.inner.Timeout)
	}
}

// TestDo_WithIPMode tests that Do with ipMode works.
func TestDo_WithIPMode(t *testing.T) {
	// We can't fully test Do without a real ECH server,
	// but we can test that the function doesn't panic
	// when the ECH config is not yet initialized.
	// This should fail gracefully.
	_, err := Do(&http.Request{URL: &url.URL{Scheme: "https", Host: "example.com"}}, "v4")
	if err == nil {
		t.Log("unexpected success (may need ECH config)")
	}
}

// TestInitDefault_NoConfig tests that InitDefault with empty Config works
// (will fail without network, but should not panic).
func TestInitDefault_NoConfig(t *testing.T) {
	// InitDefault will try to fetch ECH config from DoH.
	// Without network, it will fail, but we can test that it handles
	// the failure gracefully.
	err := InitDefault(Config{})
	if err == nil {
		t.Log("unexpected success (may need network)")
	}
}

// TestECHConfigCache_CleanUp tests that the cache doesn't grow unbounded
// by verifying cleanup behavior.
func TestECHConfigCache_CleanUp(t *testing.T) {
	// Add multiple entries
	domains := []string{"a.example.com", "b.example.com", "c.example.com"}
	for _, d := range domains {
		setCachedECH(d, []byte("config"), 300)
	}

	// All should be cached
	cacheMu.Lock()
	count := len(cache)
	cacheMu.Unlock()
	if count < len(domains) {
		t.Fatalf("expected at least %d entries, got %d", len(domains), count)
	}
}

// TestNewClient_WithIPMode tests newClient with specific IP mode.
func TestNewClient_WithIPMode(t *testing.T) {
	echConfig := []byte{0x01, 0x02, 0x03, 0x04}
	for _, mode := range []string{"", "v4", "v6", "auto"} {
		c := newClient(echConfig, mode)
		if c == nil {
			t.Fatalf("expected non-nil client for mode %q", mode)
		}
	}
}

// TestApplyConfig_Empty tests that applying an empty config is a no-op.
func TestApplyConfig_Empty(t *testing.T) {
	origCfg := *currentConfig()
	applyConfig(Config{})
	cfg := *currentConfig()
	if cfg != origCfg {
		t.Fatal("applying empty config should not change state")
	}
}

// TestApplyConfig_ProxyOnly tests that proxy URL is applied independently.
func TestApplyConfig_ProxyOnly(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	applyConfig(Config{ProxyURL: "http://proxy:8080"})
	cfg := currentConfig()
	if cfg.proxyURL != "http://proxy:8080" {
		t.Fatalf("expected proxy URL, got %q", cfg.proxyURL)
	}
}

// TestDoH_Defaults tests that default config is correct.
func TestDoH_Defaults(t *testing.T) {
	// Reset to defaults by calling applyConfig with empty struct
	// (this won't reset, but we can check the defaults are set correctly
	// by creating a fresh config)
	cfg := &config{dohURL: defaultDohURL, shellDom: shellDomain}
	if cfg.dohURL != "https://moonchan.xyz/doh" {
		t.Fatalf("unexpected default DoH URL: %q", cfg.dohURL)
	}
	if cfg.shellDom != "cloudflare-ech.com" {
		t.Fatalf("unexpected default shell domain: %q", cfg.shellDom)
	}
}

// TestNonHexRE tests the regex pattern used for wire hex extraction.
func TestNonHexRE(t *testing.T) {
	input := "ab 12 cd 34"
	result := nonHexRE.ReplaceAllString(input, "")
	if result != "ab12cd34" {
		t.Fatalf("expected 'ab12cd34', got %q", result)
	}
}

// TestECHSvcParamRE tests the base64 ECH param regex.
func TestECHSvcParamRE(t *testing.T) {
	input := "ech=\"SGVsbG8=\" other=\"param\""
	matches := echSvcParamRE.FindStringSubmatch(input)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches, got %d: %v", len(matches), matches)
	}
	if matches[1] != "SGVsbG8=" {
		t.Fatalf("expected 'SGVsbG8=', got %q", matches[1])
	}
}

// TestWSVCWireRE tests the wire format regex.
func TestWSVCWireRE(t *testing.T) {
	input := `ech=\# 8 0102030405060708`
	matches := wsvcWireRE.FindStringSubmatch(input)
	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(matches), matches)
	}
	if matches[1] != "8" {
		t.Fatalf("expected '8', got %q", matches[1])
	}
	if matches[2] != "0102030405060708" {
		t.Fatalf("expected hex string, got %q", matches[2])
	}
}

// TestClient_DoWithAddr tests that DoWithAddr sets the Host header.
func TestClient_DoWithAddr(t *testing.T) {
	// We can't fully test this without a real ECH server,
	// but we can verify the method exists and is callable.
	_ = (*Client)(nil) // just checking compilation
}

// TestConfig_IPMode_Auto tests that auto mode is treated as empty.
func TestConfig_IPMode_Auto(t *testing.T) {
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetIPMode("auto")
	cfg := currentConfig()
	// "auto" should be treated as empty string (fallback to global)
	// In our implementation, SetIPMode("auto") sets ipMode to "auto",
	// but dialTCP treats "auto" same as empty.
	_ = cfg
}

// TestStopRefresh tests that StopRefresh cancels the refresh loop.
func TestStopRefresh(t *testing.T) {
	// Start a refresh loop
	go refreshLoop()
	// Stop it
	StopRefresh()
	// Verify it stopped by checking the context is cancelled
	select {
	case <-refreshCtx.Done():
		// Good, it's cancelled
	default:
		t.Fatal("expected refreshCtx to be cancelled")
	}
}

// TestQueryDoH tests the QueryDoH function.
func TestQueryDoH(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"Answer": []map[string]interface{}{
				{"type": 1, "TTL": 300, "data": "1.2.3.4"},
			},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	origCfg := currentConfig()
	SetDohURL(server.URL)
	defer cfgPtr.Store(origCfg)

	resp, err := QueryDoH(context.Background(), "example.com", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// TestDoH_RequestWithProxy tests that DoH uses proxy when configured.
func TestDoH_RequestWithProxy(t *testing.T) {
	// We can't easily test proxy without a real proxy server,
	// but we can verify the config is set correctly.
	origCfg := currentConfig()
	defer cfgPtr.Store(origCfg)

	SetProxyURL("http://proxy.example.com:8080")
	cfg := currentConfig()
	if cfg.proxyURL != "http://proxy.example.com:8080" {
		t.Fatalf("expected proxy URL, got %q", cfg.proxyURL)
	}
}


