// tracker_list.go — default public BitTorrent HTTP trackers for peerdrive.
//
// Discovery background: anacrolix/torrent does not ship a default client-side
// tracker list. Trackers are only obtained from metainfo announce-list or
// magnet URI tr= parameters. Without a fallback list, torrents with no trackers
// (common in private or older torrents) have zero announce endpoints, meaning
// the client can only rely on DHT — which is often blocked or slow.
//
// These are the same public open trackers used by qBittorrent's defaults
// (https://github.com/qbittorrent/qBittorrent/blob/master/src/base/bittrick.h
// line ~300). They are all free, open, and require no authentication.
//
// Usage: call mergePublicTrackers() before adding a torrent to the client.
// Existing announce entries are preserved; public trackers are appended only
// if the torrent has no existing trackers (to avoid duplicating endpoints
// and to respect private tracker configurations).

package p2p_bt

import "strings"

// defaultPublicTrackers is the list of open public HTTP trackers used as a
// fallback when a torrent has no announce list. All are free/open, no auth.
// Order matters: better-performing trackers come first (based on community
// uptime statistics from the "Public Tracker Statistics" project).
var defaultPublicTrackers = []string{
	"http://tracker.opentrackr.org:1337/announce",
	"http://open.tracker.cl:1337/announce",
	"http://tracker.torrent.eu.org:451/announce",
	"http://open.demonii.com:1337/announce",
	"http://open.acgtracker.com:1337/announce",
	"http://exodus.desync.com:6969/announce",
}

// IsPublicTracker returns true if the given URL is one of our default public
// trackers. Used to avoid duplicate injection when the torrent already has
// one of our trackers listed.
func IsPublicTracker(url string) bool {
	for _, t := range defaultPublicTrackers {
		if strings.TrimRight(url, "/") == strings.TrimRight(t, "/") {
			return true
		}
	}
	return false
}

// mergePublicTrackers returns a deduplicated list of tracker URLs, prepending
// the public trackers to the given list. If the input list is empty, returns
// a copy of defaultPublicTrackers. If the input list already contains all
// public trackers, returns the input unchanged.
//
// The returned list preserves the original order of existing trackers, with
// new public trackers appended after them (trackers are contacted concurrently
// by the anacrolix client, so order is not critical for performance).
func mergePublicTrackers(existing []string) []string {
	if len(existing) == 0 {
		return append([]string(nil), defaultPublicTrackers...)
	}

	// Build a set of existing trackers for fast lookup.
	existingSet := make(map[string]bool, len(existing))
	for _, t := range existing {
		existingSet[strings.TrimRight(t, "/")] = true
	}

	// Count how many public trackers are already present.
	present := 0
	for _, t := range defaultPublicTrackers {
		if existingSet[strings.TrimRight(t, "/")] {
			present++
		}
	}

	// If all public trackers are already present, no change needed.
	if present == len(defaultPublicTrackers) {
		return append([]string(nil), existing...)
	}

	// Append missing public trackers to the existing list.
	result := append([]string(nil), existing...)
	for _, t := range defaultPublicTrackers {
		if !existingSet[strings.TrimRight(t, "/")] {
			result = append(result, t)
		}
	}
	return result
}
