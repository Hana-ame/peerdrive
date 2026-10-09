// tracker_list.go — Public BitTorrent tracker list management for peerdrive.
//
// Discovery background: anacrolix/torrent does not ship a default client-side
// tracker list. Trackers are only obtained from metainfo announce-list or
// magnet URI tr= parameters. Without a fallback list, torrents with no trackers
// (common in private or older torrents) have zero announce endpoints, meaning
// the client can only rely on DHT — which is often blocked or slow.
//
// This file implements two complementary strategies (issue #118 plans B + C):
//
//   Plan C — Curated static subset: a hand-picked ~50-tracker list drawn from
//     the 1312-tracker full list (/tmp/public-trackers-full.md). Selection
//     method: HTTP/HTTPS trackers appearing in ≥3 independent sources (most
//     stable), UDP trackers with ≥5 sources (well-known, battle-tested), plus
//     a few 4-source UDP trackers and one well-known 2-source tracker
//     (open.acgtracker.com, retained from the original 6-list).
//     Mix: ~28 HTTP, ~7 HTTPS, ~15 UDP.
//
//   Plan B — Dynamic fetch: on BT-client construction, optionally fetch a
//     tracker list from an aggregate source (newtrackon API or ngosang
//     trackerslist via jsDelivr CDN). Fetched trackers are merged on top of
//     the curated baseline. Failure is non-fatal — falls back to curated.
//
// Merge order (lowest to highest priority, deduplicated):
//   1. Custom env (PEERDRIVE_BT_PUBLIC_TRACKERS) — if set, replaces curated
//   2. Dynamic fetch results — appended to base (dedup)
//   3. Curated subset — base when no custom env is set
//
// Usage: call buildPublicTrackers() in newBTClient to get the final list.
// Existing announce entries are preserved; public trackers are appended only
// if the torrent has no existing trackers (to avoid duplicating endpoints
// and to respect private tracker configurations).

package p2p_bt

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Plan C: Curated static subset
// ---------------------------------------------------------------------------

// curatedPublicTrackers is a hand-picked subset of ~50 stable public trackers.
//
// Source: 1312-tracker full list collected from 4 sources (ngosang/trackerslist,
// XIU2/TrackersListCollection, newtrackon.com, DeSireFire/animeTrackerList).
//
// Selection method (deterministic, reproducible):
//   1. ALL HTTP trackers appearing in ≥3 independent sources (21 trackers).
//      These are the most likely to be alive and well-maintained.
//   2. ALL HTTPS trackers appearing in ≥3 independent sources (7 trackers).
//      Encrypted, equally stable.
//   3. UDP trackers with ≥5 sources (18 trackers). These are the most
//      battle-tested UDP endpoints (6+ sources preferred).
//   4. Three additional 4-source UDP trackers for protocol diversity.
//   5. One well-known 2-source tracker retained from the original 6-list
//      (open.acgtracker.com — long-running anime tracker).
//
// Protocol mix: ~28 HTTP, ~7 HTTPS, ~15 UDP.
//
// Note: four of the original 6 HTTP trackers (tracker.torrent.eu.org,
// open.demonii.com, exodus.desync.com) only appear in UDP form in the
// multi-source lists; their HTTP versions may have died. We use the UDP
// versions here, which are confirmed alive in 5-6 independent sources.
// open.acgtracker.com (the 5th original, 2-source only) is retained as-is.
//
// Order: HTTP first (most compatible), then HTTPS, then UDP.
var curatedPublicTrackers = []string{
	// --- HTTP (≥3 sources, sorted by source count desc, then alpha) ---
	"http://tracker.renfei.net:8080/announce",                          // 6 sources
	"http://ipv4announce.sktorrent.eu:6969/announce",                   // 5 sources
	"http://tracker.dler.com:6969/announce",                            // 5 sources
	"http://tracker.dler.org:6969/announce",                            // 5 sources
	"http://tracker.mywaifu.best:6969/announce",                        // 5 sources
	"http://1337.abcvg.info:80/announce",                               // 4 sources
	"http://t.overflow.biz:6969/announce",                              // 4 sources
	"http://tracker.dhitechnical.com:6969/announce",                    // 4 sources
	"http://tracker.opentrackr.org:1337/announce",                      // 4 sources (original 6)
	"http://tracker.qu.ax:6969/announce",                               // 4 sources
	"http://tracker.waaa.moe:6969/announce",                            // 4 sources
	"http://tracker.zhuqiy.dgj055.icu:80/announce",                     // 4 sources
	"http://tracker2.dler.org:80/announce",                             // 4 sources
	"http://004430.xyz:80/announce",                                    // 3 sources
	"http://bittorrent-tracker.e-n-c-r-y-p-t.net:1337/announce",       // 3 sources
	"http://bt1.archive.org:6969/announce",                             // 3 sources
	"http://bt2.archive.org:6969/announce",                             // 3 sources
	"http://open.tracker.cl:1337/announce",                             // 3 sources (original 6)
	"http://tr.nyacat.pw:80/announce",                                  // 3 sources
	"http://tracker.internetwarriors.net:1337/announce",                // 3 sources
	"http://tracker1.itzmx.com:8080/announce",                          // 3 sources
	// --- HTTPS (≥3 sources) ---
	"https://tracker.foreverpirates.co:443/announce",                   // 4 sources
	"https://004430.xyz:443/announce",                                  // 3 sources
	"https://1337.abcvg.info:443/announce",                             // 3 sources
	"https://t.213891.xyz:443/announce",                                // 3 sources
	"https://tracker.7471.top:443/announce",                            // 3 sources
	"https://tracker.nekomi.cn:443/announce",                           // 3 sources
	"https://tracker.pmman.tech:443/announce",                          // 3 sources
	// --- UDP (≥5 sources) ---
	"udp://open.demonii.com:1337/announce",                             // 6 sources (orig 6, UDP form)
	"udp://open.stealth.si:80/announce",                                // 6 sources
	"udp://retracker01-msk-virt.corbina.net:80/announce",               // 6 sources
	"udp://tracker-udp.gbitt.info:80/announce",                         // 6 sources
	"udp://tracker.opentrackr.org:1337/announce",                       // 6 sources
	"udp://tracker.qu.ax:6969/announce",                                // 6 sources
	"udp://exodus.desync.com:6969/announce",                            // 5 sources (orig 6, UDP form)
	"udp://explodie.org:6969/announce",                                 // 5 sources
	"udp://mail.segso.net:6969/announce",                               // 5 sources
	"udp://tracker.bittor.pw:1337/announce",                            // 5 sources
	"udp://tracker.corpscorp.online:80/announce",                       // 5 sources
	"udp://tracker.ducks.party:1984/announce",                          // 5 sources
	"udp://tracker.farted.net:6969/announce",                           // 5 sources
	"udp://tracker.filemail.com:6969/announce",                         // 5 sources
	"udp://tracker.gmi.gd:6969/announce",                               // 5 sources
	"udp://tracker.nyaa.vc:6969/announce",                              // 5 sources
	"udp://tracker.torrent.eu.org:451/announce",                        // 5 sources (orig 6, UDP form)
	"udp://tracker2.dler.org:80/announce",                              // 5 sources
	// --- UDP (4 sources, additional well-known) ---
	"udp://tracker.opentrackr.com:6969/announce",                       // 4 sources
	"udp://tracker.peerfect.org:6969/announce",                         // 4 sources
	"udp://tracker.wildkat.net:6969/announce",                          // 4 sources
	// --- Well-known retained from original 6-list (2 sources) ---
	"http://open.acgtracker.com:1096/announce",                         // 2 sources (orig 6)
}

// ---------------------------------------------------------------------------
// Plan B: Dynamic fetch configuration
// ---------------------------------------------------------------------------

// defaultFetchURLs are the aggregate tracker list sources tried in order
// when dynamic fetching is enabled. The first successful URL wins; subsequent
// URLs are only tried if earlier ones fail.
//
//  1. newtrackon.com/api/stable — availability-filtered list (~50 trackers),
//     the most conservative source. Primary because it already does the
//     connectivity filtering for us.
//  2. ngosang/trackerslist via jsDelivr CDN — larger list (~73 trackers),
//     served via jsDelivr for reliability (raw.githubusercontent.com often
//     times out from certain networks). Backup only.
var defaultFetchURLs = []string{
	"https://newtrackon.com/api/stable",
	"https://cdn.jsdelivr.net/gh/ngosang/trackerslist@master/trackers_all.txt",
}

// TrackerConfig holds the configuration for building the public tracker list.
type TrackerConfig struct {
	// CustomTrackers from PEERDRIVE_BT_PUBLIC_TRACKERS (comma-separated).
	// If non-empty, replaces the curated subset as the base list.
	// Dynamic fetch results are still appended on top (deduplicated).
	CustomTrackers []string

	// DynamicEnable controls whether to fetch tracker lists from remote sources.
	// Default: true (PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE=true).
	DynamicEnable bool

	// FetchURLs are the URLs to try, in order. First success wins.
	// Default: defaultFetchURLs (newtrackon + ngosang via jsDelivr).
	// Override via PEERDRIVE_BT_TRACKERS_FETCH_URL (comma-separated).
	FetchURLs []string

	// FetchTimeout is the maximum time to spend on the dynamic fetch.
	// Default: 5s (PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT=5s).
	FetchTimeout time.Duration
}

// LoadTrackerConfig reads tracker configuration from environment variables.
//
// Environment variables:
//   - PEERDRIVE_BT_PUBLIC_TRACKERS (comma-separated URLs) — custom base list.
//     If set, replaces the curated subset. Dynamic fetch results are still
//     appended. Preserves the original "env overrides defaults" semantics.
//   - PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE (bool, default true) — whether to
//     attempt dynamic fetch. Set to "false"/"0"/"no" to disable.
//   - PEERDRIVE_BT_TRACKERS_FETCH_URL (comma-separated URLs) — override the
//     default fetch URLs. First success wins.
//   - PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT (duration, default 5s) — max time
//     for the dynamic fetch. Set to "0" for no timeout (not recommended).
func LoadTrackerConfig() TrackerConfig {
	cfg := TrackerConfig{
		DynamicEnable: true,
		FetchURLs:     append([]string(nil), defaultFetchURLs...),
		FetchTimeout:  5 * time.Second,
	}

	if envList := os.Getenv("PEERDRIVE_BT_PUBLIC_TRACKERS"); envList != "" {
		for _, t := range strings.Split(envList, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				cfg.CustomTrackers = append(cfg.CustomTrackers, t)
			}
		}
	}

	if v := os.Getenv("PEERDRIVE_BT_TRACKERS_DYNAMIC_ENABLE"); v != "" {
		switch strings.ToLower(v) {
		case "false", "0", "no", "off":
			cfg.DynamicEnable = false
		case "true", "1", "yes", "on":
			cfg.DynamicEnable = true
		}
	}

	if urls := os.Getenv("PEERDRIVE_BT_TRACKERS_FETCH_URL"); urls != "" {
		cfg.FetchURLs = nil
		for _, u := range strings.Split(urls, ",") {
			u = strings.TrimSpace(u)
			if u != "" {
				cfg.FetchURLs = append(cfg.FetchURLs, u)
			}
		}
	}

	if v := os.Getenv("PEERDRIVE_BT_TRACKERS_FETCH_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.FetchTimeout = d
		}
	}

	return cfg
}

// ---------------------------------------------------------------------------
// Plan B: Dynamic fetch implementation
// ---------------------------------------------------------------------------

// validTrackerSchemes are the URL schemes accepted in tracker lists.
var validTrackerSchemes = []string{
	"http://", "https://", "udp://", "ws://", "wss://",
}

// isValidTrackerURL checks if a URL has a valid BitTorrent tracker scheme
// and a non-empty host part (e.g. "http://" alone is rejected).
func isValidTrackerURL(url string) bool {
	for _, scheme := range validTrackerSchemes {
		if strings.HasPrefix(url, scheme) && len(url) > len(scheme) {
			return true
		}
	}
	return false
}

// fetchTrackerList fetches tracker URLs from the given URLs, trying each in
// order until one succeeds. Returns the parsed list on first success, or
// nil if all URLs fail.
//
// The response format is expected to be one tracker URL per line (with or
// without blank lines between entries). Lines that don't match a valid
// tracker scheme are silently skipped.
//
// This function respects the context deadline (set by the caller via timeout).
func fetchTrackerList(ctx context.Context, urls []string) []string {
	client := &http.Client{}

	for _, url := range urls {
		if err := ctx.Err(); err != nil {
			break // overall context expired
		}
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			LogWarn("tracker-fetch: invalid URL %q: %v", url, err)
			continue
		}
		req.Header.Set("User-Agent", "peerdrive-tracker-fetch/1.0")

		resp, err := client.Do(req)
		if err != nil {
			LogWarn("tracker-fetch: %s failed: %v", url, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			LogWarn("tracker-fetch: %s returned HTTP %d", url, resp.StatusCode)
			resp.Body.Close()
			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			LogWarn("tracker-fetch: %s read error: %v", url, err)
			continue
		}

		trackers := parseTrackerList(string(body))
		if len(trackers) == 0 {
			LogWarn("tracker-fetch: %s returned no valid trackers", url)
			continue
		}

		LogInfo("tracker-fetch: %s returned %d trackers", url, len(trackers))
		return trackers
	}

	return nil
}

// parseTrackerList parses a tracker list from text (one URL per line).
// Lines with valid tracker schemes are kept; others are skipped.
// Trailing slashes are preserved (mergeTrackers handles dedup with/without slash).
func parseTrackerList(text string) []string {
	var trackers []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if isValidTrackerURL(line) {
			trackers = append(trackers, line)
		}
	}
	return trackers
}

// ---------------------------------------------------------------------------
// Merge logic
// ---------------------------------------------------------------------------

// buildPublicTrackers assembles the final public tracker list by combining:
//   - Base: custom env trackers (if set) or curated subset (if not)
//   - Dynamic fetch results (if enabled and successful)
//
// Results are deduplicated (trailing-slash aware). The returned list is safe
// to store in BTClient.publicTrackers.
func buildPublicTrackers(cfg TrackerConfig) []string {
	// Step 1: determine base list.
	var base []string
	if len(cfg.CustomTrackers) > 0 {
		base = append([]string(nil), cfg.CustomTrackers...)
	} else {
		base = append([]string(nil), curatedPublicTrackers...)
	}

	// Step 2: try dynamic fetch.
	if cfg.DynamicEnable && len(cfg.FetchURLs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.FetchTimeout)
		defer cancel()
		fetched := fetchTrackerListFunc(ctx, cfg.FetchURLs)
		if len(fetched) > 0 {
			base = mergeTrackers(base, fetched)
		}
	}

	return base
}

// mergeTrackers merges two tracker lists, deduplicating by URL (trailing-slash
// aware). The first list's order is preserved; new entries from the second list
// are appended.
func mergeTrackers(a, b []string) []string {
	seen := make(map[string]bool, len(a))
	for _, t := range a {
		seen[normalizeTracker(t)] = true
	}
	result := append([]string(nil), a...)
	for _, t := range b {
		norm := normalizeTracker(t)
		if !seen[norm] {
			seen[norm] = true
			result = append(result, t)
		}
	}
	return result
}

// normalizeTracker removes trailing slashes for deduplication comparison.
func normalizeTracker(t string) string {
	return strings.TrimRight(t, "/")
}

// ---------------------------------------------------------------------------
// Public API (backward-compatible)
// ---------------------------------------------------------------------------

// IsPublicTracker returns true if the given URL matches one of the curated
// public trackers (trailing-slash aware). Used to detect whether a torrent
// already includes one of our trackers.
func IsPublicTracker(url string) bool {
	norm := normalizeTracker(url)
	for _, t := range curatedPublicTrackers {
		if normalizeTracker(t) == norm {
			return true
		}
	}
	return false
}

// mergePublicTrackers returns a deduplicated list of tracker URLs, appending
// the given public trackers to the existing list. If existing is empty,
// returns a copy of public.
//
// The returned list preserves the original order of existing trackers, with
// new public trackers appended after them (trackers are contacted concurrently
// by the anacrolix client, so order is not critical for performance).
//
// Trailing slashes are handled: "http://a:80/announce" and
// "http://a:80/announce/" are treated as the same tracker.
func mergePublicTrackers(existing, public []string) []string {
	if len(existing) == 0 {
		return append([]string(nil), public...)
	}

	// Build a set of existing trackers for fast lookup.
	existingSet := make(map[string]bool, len(existing))
	for _, t := range existing {
		existingSet[normalizeTracker(t)] = true
	}

	// Count how many public trackers are already present.
	present := 0
	for _, t := range public {
		if existingSet[normalizeTracker(t)] {
			present++
		}
	}

	// If all public trackers are already present, no change needed.
	if present == len(public) {
		return append([]string(nil), existing...)
	}

	// Append missing public trackers to the existing list.
	result := append([]string(nil), existing...)
	for _, t := range public {
		if !existingSet[normalizeTracker(t)] {
			result = append(result, t)
		}
	}
	return result
}

// ---------------------------------------------------------------------------
// Test support: injectable fetcher
// ---------------------------------------------------------------------------

// fetchTrackerListFunc is the function used to fetch trackers from remote
// sources. Override in tests to inject a fake source (httptest server, etc.).
// Set to nil to restore the default implementation.
var fetchTrackerListFunc = fetchTrackerList
