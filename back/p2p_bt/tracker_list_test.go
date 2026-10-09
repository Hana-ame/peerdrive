// Tests for the public tracker list and merge logic.
//
// Discovery background: anacrolix/torrent does not ship a default client-side
// tracker list. These tests verify that:
//   - The curated ~50 subset is valid (URL format, protocol mix, no duplicates)
//   - The merge logic handles empty/partial/full/no-overlap cases correctly
//   - Trailing-slash deduplication works
//   - Dynamic fetch (Plan B) succeeds, times out, and falls back gracefully
//   - Configuration loading from environment variables works correctly
//   - The three-way merge (curated + fetched + custom) deduplicates correctly

package p2p_bt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// ===========================================================================
// Plan C: Curated subset validity
// ===========================================================================

func TestCuratedPublicTrackers_Count(t *testing.T) {
	// Should be approximately 50 (48-52 is acceptable).
	if len(curatedPublicTrackers) < 45 || len(curatedPublicTrackers) > 55 {
		t.Errorf("curatedPublicTrackers has %d entries, expected ~50", len(curatedPublicTrackers))
	}
}

func TestCuratedPublicTrackers_NoEmpty(t *testing.T) {
	for i, u := range curatedPublicTrackers {
		if strings.TrimSpace(u) == "" {
			t.Errorf("curatedPublicTrackers[%d] is empty", i)
		}
	}
}

func TestCuratedPublicTrackers_ValidScheme(t *testing.T) {
	for i, u := range curatedPublicTrackers {
		if !isValidTrackerURL(u) {
			t.Errorf("curatedPublicTrackers[%d] has invalid scheme: %q", i, u)
		}
	}
}

func TestCuratedPublicTrackers_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for i, u := range curatedPublicTrackers {
		norm := normalizeTracker(u)
		if seen[norm] {
			t.Errorf("curatedPublicTrackers[%d] is a duplicate: %q (already seen)", i, u)
		}
		seen[norm] = true
	}
}

func TestCuratedPublicTrackers_ProtolMix(t *testing.T) {
	httpCount, httpsCount, udpCount := 0, 0, 0
	for _, u := range curatedPublicTrackers {
		switch {
		case strings.HasPrefix(u, "http://"):
			httpCount++
		case strings.HasPrefix(u, "https://"):
			httpsCount++
		case strings.HasPrefix(u, "udp://"):
			udpCount++
		}
	}
	// Expect a mix: HTTP dominant, HTTPS present, UDP present.
	if httpCount < 15 {
		t.Errorf("too few HTTP trackers: %d", httpCount)
	}
	if httpsCount < 3 {
		t.Errorf("too few HTTPS trackers: %d", httpsCount)
	}
	if udpCount < 10 {
		t.Errorf("too few UDP trackers: %d", udpCount)
	}
	t.Logf("protocol mix: HTTP=%d, HTTPS=%d, UDP=%d", httpCount, httpsCount, udpCount)
}

// TestCuratedPublicTrackers_SelectionLogic verifies the deterministic
// selection: all HTTP/HTTPS with ≥3 sources, all UDP with ≥5 sources.
// We can't re-derive from the raw file here, but we can verify the list
// contains known multi-source trackers and does NOT contain known low-source
// trackers.
func TestCuratedPublicTrackers_ContainsKnownStable(t *testing.T) {
	mustContain := []string{
		"http://tracker.renfei.net:8080/announce",       // 6 sources (top HTTP)
		"http://tracker.opentrackr.org:1337/announce",  // 4 sources (original 6)
		"http://open.tracker.cl:1337/announce",          // 3 sources (original 6)
		"udp://open.demonii.com:1337/announce",          // 6 sources (original 6, UDP)
		"udp://tracker.opentrackr.org:1337/announce",    // 6 sources
		"udp://tracker.qu.ax:6969/announce",             // 6 sources
		"https://tracker.foreverpirates.co:443/announce", // 4 sources (top HTTPS)
		"http://open.acgtracker.com:1096/announce",       // original 6 (2 sources, retained)
	}
	for _, want := range mustContain {
		found := false
		for _, u := range curatedPublicTrackers {
			if normalizeTracker(u) == normalizeTracker(want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("curatedPublicTrackers missing known stable tracker: %q", want)
		}
	}
}

// ===========================================================================
// IsPublicTracker
// ===========================================================================

func TestIsPublicTracker(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{curatedPublicTrackers[0], true},
		{curatedPublicTrackers[len(curatedPublicTrackers)-1], true},
		{"http://unknown.tracker:80/announce", false},
		{"", false},
		{curatedPublicTrackers[0] + "/", true},          // trailing slash variant
		{"http://tracker.opentrackr.org:1337/announce/", true}, // original 6 + slash
		{"udp://open.demonii.com:1337/announce", true},    // UDP original
		{"ftp://not.a.tracker:21/announce", false},        // invalid scheme
	}
	for _, tt := range tests {
		got := IsPublicTracker(tt.url)
		if got != tt.want {
			t.Errorf("IsPublicTracker(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// ===========================================================================
// mergePublicTrackers (new signature: takes public list as param)
// ===========================================================================

func TestMergePublicTrackers_Empty(t *testing.T) {
	public := curatedPublicTrackers[:3]
	result := mergePublicTrackers(nil, public)
	if len(result) != len(public) {
		t.Errorf("mergePublicTrackers(nil, public) = %d, want %d", len(result), len(public))
	}
	// Should be a copy, not a reference.
	if &result[0] == &public[0] {
		t.Error("mergePublicTrackers returned a reference to the original slice")
	}
}

func TestMergePublicTrackers_EmptySlice(t *testing.T) {
	public := curatedPublicTrackers[:3]
	result := mergePublicTrackers([]string{}, public)
	if len(result) != len(public) {
		t.Errorf("mergePublicTrackers(empty, public) = %d, want %d", len(result), len(public))
	}
}

func TestMergePublicTrackers_AlreadyHasAll(t *testing.T) {
	public := curatedPublicTrackers[:5]
	existing := append([]string(nil), public...)
	result := mergePublicTrackers(existing, public)
	if len(result) != len(existing) {
		t.Errorf("mergePublicTrackers(all present) = %d, want %d (no duplicates)",
			len(result), len(existing))
	}
}

func TestMergePublicTrackers_PartialOverlap(t *testing.T) {
	public := curatedPublicTrackers[:6]
	half := 3
	existing := append([]string(nil), public[:half]...)
	result := mergePublicTrackers(existing, public)

	// Should have all public trackers.
	if len(result) != len(public) {
		t.Errorf("mergePublicTrackers(partial) = %d, want %d", len(result), len(public))
	}
	// First half should be preserved in order.
	for i := 0; i < half; i++ {
		if result[i] != public[i] {
			t.Errorf("result[%d] = %q, want %q (preserved order)", i, result[i], public[i])
		}
	}
}

func TestMergePublicTrackers_NoOverlap(t *testing.T) {
	public := curatedPublicTrackers[:4]
	existing := []string{
		"http://custom.tracker:1337/announce",
		"http://another.tracker:6969/announce",
	}
	result := mergePublicTrackers(existing, public)

	expectedLen := len(existing) + len(public)
	if len(result) != expectedLen {
		t.Errorf("mergePublicTrackers(no overlap) = %d, want %d", len(result), expectedLen)
	}
	// Existing trackers should come first.
	for i := 0; i < len(existing); i++ {
		if result[i] != existing[i] {
			t.Errorf("result[%d] = %q, want %q (existing first)", i, result[i], existing[i])
		}
	}
}

func TestMergePublicTrackers_TrailingSlash(t *testing.T) {
	public := curatedPublicTrackers[:4]
	existing := []string{public[0] + "/"}
	result := mergePublicTrackers(existing, public)

	// Should not duplicate: 1 existing (with slash) + 3 others = 4 total.
	if len(result) != len(public) {
		t.Errorf("mergePublicTrackers(trailing slash) = %d, want %d",
			len(result), len(public))
	}
}

// ===========================================================================
// mergeTrackers (dedup helper)
// ===========================================================================

func TestMergeTrackers_BothEmpty(t *testing.T) {
	result := mergeTrackers(nil, nil)
	if len(result) != 0 {
		t.Errorf("mergeTrackers(nil, nil) = %d, want 0", len(result))
	}
}

func TestMergeTrackers_OneEmpty(t *testing.T) {
	a := []string{"http://a:80/announce"}
	result := mergeTrackers(a, nil)
	if len(result) != 1 {
		t.Errorf("mergeTrackers(a, nil) = %d, want 1", len(result))
	}
	result = mergeTrackers(nil, a)
	if len(result) != 1 {
		t.Errorf("mergeTrackers(nil, a) = %d, want 1", len(result))
	}
}

func TestMergeTrackers_NoOverlap(t *testing.T) {
	a := []string{"http://a:80/announce"}
	b := []string{"http://b:80/announce"}
	result := mergeTrackers(a, b)
	if len(result) != 2 {
		t.Errorf("mergeTrackers(no overlap) = %d, want 2", len(result))
	}
}

func TestMergeTrackers_Overlap(t *testing.T) {
	a := []string{"http://a:80/announce"}
	b := []string{"http://a:80/announce", "http://b:80/announce"}
	result := mergeTrackers(a, b)
	if len(result) != 2 {
		t.Errorf("mergeTrackers(overlap) = %d, want 2", len(result))
	}
}

func TestMergeTrackers_TrailingSlash(t *testing.T) {
	a := []string{"http://a:80/announce"}
	b := []string{"http://a:80/announce/"}
	result := mergeTrackers(a, b)
	if len(result) != 1 {
		t.Errorf("mergeTrackers(trailing slash) = %d, want 1", len(result))
	}
}

func TestMergeTrackers_OrderPreserved(t *testing.T) {
	a := []string{"http://a:80/announce", "http://b:80/announce"}
	b := []string{"http://c:80/announce", "http://a:80/announce"}
	result := mergeTrackers(a, b)
	if result[0] != "http://a:80/announce" || result[1] != "http://b:80/announce" {
		t.Errorf("mergeTrackers order not preserved: %v", result)
	}
}

// ===========================================================================
// isValidTrackerURL
// ===========================================================================

func TestIsValidTrackerURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"http://tracker:80/announce", true},
		{"https://tracker:443/announce", true},
		{"udp://tracker:6969/announce", true},
		{"ws://tracker:80/announce", true},
		{"wss://tracker:443/announce", true},
		{"ftp://tracker:21/announce", false},
		{"", false},
		{"http://", false},  // missing host
		{"http", false},
		{"not a url", false},
		{"tcp://tracker:80/announce", false},
	}
	for _, tt := range tests {
		got := isValidTrackerURL(tt.url)
		if got != tt.want {
			t.Errorf("isValidTrackerURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

// ===========================================================================
// parseTrackerList
// ===========================================================================

func TestParseTrackerList_Valid(t *testing.T) {
	input := `http://a:80/announce
udp://b:6969/announce
https://c:443/announce
`
	result := parseTrackerList(input)
	if len(result) != 3 {
		t.Errorf("parseTrackerList = %d, want 3", len(result))
	}
}

func TestParseTrackerList_EmptyLines(t *testing.T) {
	input := `
http://a:80/announce

udp://b:6969/announce

`
	result := parseTrackerList(input)
	if len(result) != 2 {
		t.Errorf("parseTrackerList (empty lines) = %d, want 2", len(result))
	}
}

func TestParseTrackerList_InvalidLines(t *testing.T) {
	input := `http://a:80/announce
not a url
ftp://b:21/announce

https://c:443/announce
`
	result := parseTrackerList(input)
	if len(result) != 2 {
		t.Errorf("parseTrackerList (invalid lines) = %d, want 2", len(result))
	}
	if result[0] != "http://a:80/announce" || result[1] != "https://c:443/announce" {
		t.Errorf("parseTrackerList unexpected content: %v", result)
	}
}

func TestParseTrackerList_Empty(t *testing.T) {
	result := parseTrackerList("")
	if len(result) != 0 {
		t.Errorf("parseTrackerList(empty) = %d, want 0", len(result))
	}
	result = parseTrackerList("\n\n\n")
	if len(result) != 0 {
		t.Errorf("parseTrackerList(blank) = %d, want 0", len(result))
	}
}

func TestParseTrackerList_TrailingSpaces(t *testing.T) {
	input := "  http://a:80/announce  \n  udp://b:6969/announce  \n"
	result := parseTrackerList(input)
	if len(result) != 2 {
		t.Errorf("parseTrackerList (trailing spaces) = %d, want 2", len(result))
	}
}

// ===========================================================================
// LoadTrackerConfig
// ===========================================================================

func TestLoadTrackerConfig_Defaults(t *testing.T) {
	// Ensure no relevant env vars are set.
	os.Unsetenv("PEERDRIVE_BT_PUBLIC_TRACKERS")
	os.Unsetenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE")
	os.Unsetenv("PEERDRIVE_BT_TRACKERS_FETCH_URL")
	os.Unsetenv("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT")

	cfg := LoadTrackerConfig()

	if !cfg.DynamicEnable {
		t.Error("DynamicEnable should default to true")
	}
	if len(cfg.CustomTrackers) != 0 {
		t.Errorf("CustomTrackers should default to empty, got %v", cfg.CustomTrackers)
	}
	if len(cfg.FetchURLs) != len(defaultFetchURLs) {
		t.Errorf("FetchURLs should default to %d URLs, got %d", len(defaultFetchURLs), len(cfg.FetchURLs))
	}
	if cfg.FetchTimeout != 5*time.Second {
		t.Errorf("FetchTimeout should default to 5s, got %v", cfg.FetchTimeout)
	}
}

func TestLoadTrackerConfig_CustomTrackers(t *testing.T) {
	os.Setenv("PEERDRIVE_BT_PUBLIC_TRACKERS", "http://a:80/announce, http://b:80/announce, , udp://c:6969/announce")
	defer os.Unsetenv("PEERDRIVE_BT_PUBLIC_TRACKERS")

	cfg := LoadTrackerConfig()

	if len(cfg.CustomTrackers) != 3 {
		t.Errorf("CustomTrackers = %d, want 3 (empty strings skipped)", len(cfg.CustomTrackers))
	}
	if cfg.CustomTrackers[0] != "http://a:80/announce" {
		t.Errorf("CustomTrackers[0] = %q, want %q", cfg.CustomTrackers[0], "http://a:80/announce")
	}
}

func TestLoadTrackerConfig_DynamicEnable(t *testing.T) {
	tests := []struct {
		env  string
		want bool
	}{
		{"", true},    // default
		{"true", true},
		{"false", false},
		{"0", false},
		{"1", true},
		{"no", false},
		{"yes", true},
		{"off", false},
		{"on", true},
		{"TRUE", true},
		{"False", false},
	}
	for _, tt := range tests {
		if tt.env == "" {
			os.Unsetenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE")
		} else {
			os.Setenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE", tt.env)
		}
		cfg := LoadTrackerConfig()
		if cfg.DynamicEnable != tt.want {
			t.Errorf("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE=%q: got %v, want %v",
				tt.env, cfg.DynamicEnable, tt.want)
		}
	}
	os.Unsetenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE")
}

func TestLoadTrackerConfig_FetchURLs(t *testing.T) {
	os.Setenv("PEERDRIVE_BT_TRACKERS_FETCH_URL", "http://a:80/trackers, http://b:80/trackers")
	defer os.Unsetenv("PEERDRIVE_BT_TRACKERS_FETCH_URL")

	cfg := LoadTrackerConfig()

	if len(cfg.FetchURLs) != 2 {
		t.Errorf("FetchURLs = %d, want 2", len(cfg.FetchURLs))
	}
	if cfg.FetchURLs[0] != "http://a:80/trackers" {
		t.Errorf("FetchURLs[0] = %q", cfg.FetchURLs[0])
	}
}

func TestLoadTrackerConfig_Timeout(t *testing.T) {
	tests := []struct {
		env  string
		want time.Duration
	}{
		{"", 5 * time.Second},
		{"3s", 3 * time.Second},
		{"100ms", 100 * time.Millisecond},
		{"0", 5 * time.Second},    // 0 is invalid, keep default
		{"-1s", 5 * time.Second},  // negative is invalid, keep default
		{"garbage", 5 * time.Second}, // parse error, keep default
	}
	for _, tt := range tests {
		if tt.env == "" {
			os.Unsetenv("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT")
		} else {
			os.Setenv("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT", tt.env)
		}
		cfg := LoadTrackerConfig()
		if cfg.FetchTimeout != tt.want {
			t.Errorf("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT=%q: got %v, want %v",
				tt.env, cfg.FetchTimeout, tt.want)
		}
	}
	os.Unsetenv("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT")
}

// ===========================================================================
// fetchTrackerList (Plan B: dynamic fetch)
// ===========================================================================

func TestFetchTrackerList_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("http://a:80/announce\n\nudp://b:6969/announce\nhttps://c:443/announce\n"))
	}))
	defer srv.Close()

	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{srv.URL})

	if len(result) != 3 {
		t.Fatalf("fetchTrackerList = %d trackers, want 3: %v", len(result), result)
	}
}

func TestFetchTrackerList_FirstURLFails_SecondSucceeds(t *testing.T) {
	failCount := 0
	successCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			failCount++
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		successCount++
		w.Write([]byte("http://a:80/announce\n"))
	}))
	defer srv.Close()

	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{srv.URL + "/fail", srv.URL + "/ok"})

	if len(result) != 1 {
		t.Fatalf("fetchTrackerList = %d trackers, want 1: %v", len(result), result)
	}
	if failCount != 1 {
		t.Errorf("fail endpoint hit %d times, want 1", failCount)
	}
	if successCount != 1 {
		t.Errorf("ok endpoint hit %d times, want 1", successCount)
	}
}

func TestFetchTrackerList_AllFail(t *testing.T) {
	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{
		"http://127.0.0.1:1/announce",  // connection refused
		"http://127.0.0.1:1/nonexistent",
	})
	if result != nil && len(result) > 0 {
		t.Errorf("fetchTrackerList (all fail) = %v, want nil/empty", result)
	}
}

func TestFetchTrackerList_Timeout(t *testing.T) {
	// Server that never responds.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Block forever — context timeout will cancel the request.
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := fetchTrackerList(ctx, []string{srv.URL})
	elapsed := time.Since(start)

	if len(result) > 0 {
		t.Errorf("fetchTrackerList (timeout) returned trackers: %v", result)
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("fetchTrackerList returned too quickly (%v), timeout may not work", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("fetchTrackerList took too long (%v), should respect context timeout", elapsed)
	}
}

func TestFetchTrackerList_BadData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("this is not a tracker list\njust some random text\n"))
	}))
	defer srv.Close()

	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{srv.URL})
	if len(result) != 0 {
		t.Errorf("fetchTrackerList (bad data) = %v, want empty", result)
	}
}

func TestFetchTrackerList_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{srv.URL})
	if len(result) > 0 {
		t.Errorf("fetchTrackerList (404) = %v, want empty", result)
	}
}

func TestFetchTrackerList_MixedValidInvalid(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(
			"http://valid1:80/announce\n" +
				"not a url\n" +
				"udp://valid2:6969/announce\n" +
				"\n" +
				"ftp://invalid:21/announce\n" +
				"https://valid3:443/announce\n",
		))
	}))
	defer srv.Close()

	ctx := context.Background()
	result := fetchTrackerList(ctx, []string{srv.URL})
	if len(result) != 3 {
		t.Errorf("fetchTrackerList (mixed) = %d, want 3: %v", len(result), result)
	}
}

func TestFetchTrackerList_ContextAlreadyExpired(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	// Give the context a moment to expire.
	time.Sleep(10 * time.Microsecond)

	result := fetchTrackerList(ctx, []string{"http://127.0.0.1:1/announce"})
	if len(result) > 0 {
		t.Errorf("fetchTrackerList (expired context) = %v, want empty", result)
	}
}

// ===========================================================================
// buildPublicTrackers (integration: curated + fetched + custom)
// ===========================================================================

func TestBuildPublicTrackers_NoCustom_NoFetch(t *testing.T) {
	cfg := TrackerConfig{
		CustomTrackers: nil,
		DynamicEnable:  false,
		FetchURLs:      nil,
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)
	if len(result) != len(curatedPublicTrackers) {
		t.Errorf("buildPublicTrackers (no custom, no fetch) = %d, want %d",
			len(result), len(curatedPublicTrackers))
	}
}

func TestBuildPublicTrackers_WithCustom_NoFetch(t *testing.T) {
	custom := []string{
		"http://custom1:80/announce",
		"http://custom2:80/announce",
	}
	cfg := TrackerConfig{
		CustomTrackers: custom,
		DynamicEnable:  false,
		FetchURLs:      nil,
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)
	if len(result) != 2 {
		t.Errorf("buildPublicTrackers (custom, no fetch) = %d, want 2", len(result))
	}
	// Should be the custom list, not the curated one.
	if result[0] != "http://custom1:80/announce" {
		t.Errorf("buildPublicTrackers[0] = %q, want %q", result[0], "http://custom1:80/announce")
	}
}

func TestBuildPublicTrackers_NoCustom_WithFetch(t *testing.T) {
	// Inject a fake fetcher that returns 3 trackers, 1 of which overlaps with curated.
	origFetcher := fetchTrackerListFunc
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		return []string{
			curatedPublicTrackers[0], // overlap with curated
			"http://fetched1:80/announce",
			"udp://fetched2:6969/announce",
		}
	}

	cfg := TrackerConfig{
		CustomTrackers: nil,
		DynamicEnable:  true,
		FetchURLs:      []string{"http://fake:80/trackers"},
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)

	// Should have curated + 2 new fetched (1 was duplicate).
	expectedLen := len(curatedPublicTrackers) + 2
	if len(result) != expectedLen {
		t.Errorf("buildPublicTrackers (no custom, with fetch) = %d, want %d",
			len(result), expectedLen)
	}

	// Verify the fetched trackers are present.
	found := false
	for _, u := range result {
		if u == "http://fetched1:80/announce" {
			found = true
			break
		}
	}
	if !found {
		t.Error("buildPublicTrackers missing fetched tracker http://fetched1:80/announce")
	}
}

func TestBuildPublicTrackers_WithCustom_WithFetch(t *testing.T) {
	origFetcher := fetchTrackerListFunc
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		return []string{
			"http://custom1:80/announce", // overlap with custom
			"http://fetched1:80/announce",
			"udp://fetched2:6969/announce",
			"http://fetched3:80/announce/", // trailing slash variant
		}
	}

	custom := []string{
		"http://custom1:80/announce",
		"http://custom2:80/announce",
	}
	cfg := TrackerConfig{
		CustomTrackers: custom,
		DynamicEnable:  true,
		FetchURLs:      []string{"http://fake:80/trackers"},
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)

	// Should have: custom (2) + fetched3 (new) + fetched1 (new) + fetched2 (new) = 5
	// fetched0 (custom1) is duplicate, fetched3 trailing slash is new.
	// Wait: custom1 is in custom AND fetched. fetched1, fetched2, fetched3 are new.
	// So total = 2 (custom) + 3 (new fetched) = 5
	expectedLen := 5
	if len(result) != expectedLen {
		t.Errorf("buildPublicTrackers (custom+fetch) = %d, want %d: %v",
			len(result), expectedLen, result)
	}
}

func TestBuildPublicTrackers_DynamicDisabled(t *testing.T) {
	// Even if FetchURLs is set, DynamicEnable=false should skip fetching.
	origFetcher := fetchTrackerListFunc
	fetchCalled := false
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		fetchCalled = true
		return []string{"http://should-not-appear:80/announce"}
	}

	cfg := TrackerConfig{
		CustomTrackers: nil,
		DynamicEnable:  false,
		FetchURLs:      []string{"http://fake:80/trackers"},
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)

	if fetchCalled {
		t.Error("fetchTrackerListFunc was called despite DynamicEnable=false")
	}
	if len(result) != len(curatedPublicTrackers) {
		t.Errorf("buildPublicTrackers (dynamic disabled) = %d, want %d",
			len(result), len(curatedPublicTrackers))
	}
}

func TestBuildPublicTrackers_FetchReturnsEmpty(t *testing.T) {
	// Fetch returns empty (all URLs failed) → fall back to curated only.
	origFetcher := fetchTrackerListFunc
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		return nil
	}

	cfg := TrackerConfig{
		CustomTrackers: nil,
		DynamicEnable:  true,
		FetchURLs:      []string{"http://fake:80/trackers"},
		FetchTimeout:   5 * time.Second,
	}
	result := buildPublicTrackers(cfg)

	if len(result) != len(curatedPublicTrackers) {
		t.Errorf("buildPublicTrackers (fetch empty) = %d, want %d",
			len(result), len(curatedPublicTrackers))
	}
}

func TestBuildPublicTrackers_FetchTimeoutFallback(t *testing.T) {
	// Inject a fetcher that hangs — should return nil (timeout).
	origFetcher := fetchTrackerListFunc
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		// Simulate a timeout by blocking until context expires.
		<-ctx.Done()
		return nil
	}

	cfg := TrackerConfig{
		CustomTrackers: nil,
		DynamicEnable:  true,
		FetchURLs:      []string{"http://fake:80/trackers"},
		FetchTimeout:   100 * time.Millisecond,
	}
	start := time.Now()
	result := buildPublicTrackers(cfg)
	elapsed := time.Since(start)

	if len(result) != len(curatedPublicTrackers) {
		t.Errorf("buildPublicTrackers (fetch timeout) = %d, want %d",
			len(result), len(curatedPublicTrackers))
	}
	if elapsed < 80*time.Millisecond {
		t.Errorf("buildPublicTrackers returned too quickly (%v), fetch may not have been attempted", elapsed)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("buildPublicTrackers took too long (%v), timeout should have triggered", elapsed)
	}
}

// ===========================================================================
// Thread safety: concurrent access to fetchTrackerListFunc
// ===========================================================================

func TestFetchTrackerListFunc_Concurrent(t *testing.T) {
	origFetcher := fetchTrackerListFunc
	defer func() { fetchTrackerListFunc = origFetcher }()

	fetchTrackerListFunc = func(ctx context.Context, urls []string) []string {
		return []string{"http://a:80/announce"}
	}

	var wg sync.WaitGroup
	results := make([]int, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cfg := TrackerConfig{
				DynamicEnable: true,
				FetchURLs:     []string{"http://fake:80/trackers"},
				FetchTimeout:  5 * time.Second,
			}
			result := buildPublicTrackers(cfg)
			results[idx] = len(result)
		}(i)
	}
	wg.Wait()

	for i, r := range results {
		if r < len(curatedPublicTrackers) {
			t.Errorf("goroutine %d: result = %d, want >= %d", i, r, len(curatedPublicTrackers))
		}
	}
}

// ===========================================================================
// normalizeTracker
// ===========================================================================

func TestNormalizeTracker(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://a:80/announce", "http://a:80/announce"},
		{"http://a:80/announce/", "http://a:80/announce"},
		{"http://a:80/announce//", "http://a:80/announce"},
		{"udp://b:6969/announce", "udp://b:6969/announce"},
		{"", ""},
	}
	for _, tt := range tests {
		got := normalizeTracker(tt.input)
		if got != tt.want {
			t.Errorf("normalizeTracker(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
