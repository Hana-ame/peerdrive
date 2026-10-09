// Tests for the public tracker list and merge logic.
//
// Discovery background: anacrolix/torrent does not ship a default client-side
// tracker list. These tests verify that mergePublicTrackers correctly handles
// empty lists, partial overlaps, and full deduplication — so that torrents
// with no announce list get public trackers, and those with existing trackers
// are not duplicated.

package p2p_bt

import (
	"testing"
)

func TestDefaultPublicTrackers(t *testing.T) {
	if len(defaultPublicTrackers) == 0 {
		t.Fatal("defaultPublicTrackers must not be empty")
	}
	// All entries should be HTTP or HTTPS URLs.
	for i, u := range defaultPublicTrackers {
		if len(u) < 8 {
			t.Errorf("tracker %d too short: %q", i, u)
		}
		if u[:7] != "http://" && u[:8] != "https://" {
			t.Errorf("tracker %d is not HTTP/HTTPS: %q", i, u)
		}
	}
}

func TestIsPublicTracker(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{defaultPublicTrackers[0], true},
		{defaultPublicTrackers[len(defaultPublicTrackers)-1], true},
		{"http://unknown.tracker:80/announce", false},
		{"", false},
		{"http://tracker.opentrackr.org:1337/announce/", true}, // trailing slash variant
	}
	for _, tt := range tests {
		got := IsPublicTracker(tt.url)
		if got != tt.want {
			t.Errorf("IsPublicTracker(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}

func TestMergePublicTrackers_Empty(t *testing.T) {
	result := mergePublicTrackers(nil)
	if len(result) != len(defaultPublicTrackers) {
		t.Errorf("mergePublicTrackers(nil) = %d trackers, want %d", len(result), len(defaultPublicTrackers))
	}
	// Should be a copy, not a reference to the original slice.
	if &result[0] == &defaultPublicTrackers[0] {
		t.Error("mergePublicTrackers returned a reference to the original slice")
	}
}

func TestMergePublicTrackers_EmptySlice(t *testing.T) {
	result := mergePublicTrackers([]string{})
	if len(result) != len(defaultPublicTrackers) {
		t.Errorf("mergePublicTrackers(empty) = %d trackers, want %d", len(result), len(defaultPublicTrackers))
	}
}

func TestMergePublicTrackers_AlreadyHasAll(t *testing.T) {
	// If all public trackers are already present, the result should be
	// identical to the input.
	existing := append([]string(nil), defaultPublicTrackers...)
	result := mergePublicTrackers(existing)
	if len(result) != len(existing) {
		t.Errorf("mergePublicTrackers(all) = %d trackers, want %d (no duplicates)",
			len(result), len(existing))
	}
}

func TestMergePublicTrackers_PartialOverlap(t *testing.T) {
	// If some public trackers are already present, only the missing ones
	// should be appended.
	half := len(defaultPublicTrackers) / 2
	existing := defaultPublicTrackers[:half]
	result := mergePublicTrackers(existing)

	expectedLen := len(defaultPublicTrackers) // all public trackers
	if len(result) != expectedLen {
		t.Errorf("mergePublicTrackers(partial) = %d trackers, want %d",
			len(result), expectedLen)
	}

	// First half should be preserved in order.
	for i := 0; i < half; i++ {
		if result[i] != defaultPublicTrackers[i] {
			t.Errorf("result[%d] = %q, want %q (preserved order)",
				i, result[i], defaultPublicTrackers[i])
		}
	}
}

func TestMergePublicTrackers_NoOverlap(t *testing.T) {
	// If existing trackers are completely different, all public trackers
	// should be appended.
	existing := []string{
		"http://custom.tracker:1337/announce",
		"http://another.tracker:6969/announce",
	}
	result := mergePublicTrackers(existing)

	expectedLen := len(existing) + len(defaultPublicTrackers)
	if len(result) != expectedLen {
		t.Errorf("mergePublicTrackers(no overlap) = %d trackers, want %d",
			len(result), expectedLen)
	}

	// Existing trackers should come first.
	for i := 0; i < len(existing); i++ {
		if result[i] != existing[i] {
			t.Errorf("result[%d] = %q, want %q (existing first)",
				i, result[i], existing[i])
		}
	}
}

func TestMergePublicTrackers_TrailingSlash(t *testing.T) {
	// Trackers with trailing slashes should still be deduplicated.
	existing := []string{defaultPublicTrackers[0] + "/"}
	result := mergePublicTrackers(existing)

	// Should not add a duplicate of the first tracker (just with/without slash).
	// The result should have the original + (N-1) others = N total.
	if len(result) != len(defaultPublicTrackers) {
		t.Errorf("mergePublicTrackers(trailing slash) = %d trackers, want %d",
			len(result), len(defaultPublicTrackers))
	}
}
