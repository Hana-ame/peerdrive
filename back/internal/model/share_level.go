package model

// share_level.go: Share levels (doc/NETDISK.md §12.6).
//
// A share declaration (directory / single file / collection) needs to answer not just "share or not" but also "to whom":
//
//	· public   —— appears in the share list, anyone who can connect (passes PSK) can download
//	· unlisted —— does not appear in the list, but anyone who knows the hash can download ("link sharing")
//	· private  —— does not appear in the list, only the owner and friends can download
//
// One-liner memory aid: **public = listed and given; unlisted = not listed but given; private = only given to people you know**.
//
// Empty value is equivalent to public: historically stored share scopes only have id without level (written before this upgrade
// in share_scope.json), treating them as public prevents an upgrade from silently re-taking back content the operator
// already shared out — the "I didn't change anything but others say they can't access it" kind is the hardest to self-check.

import "strings"

const (
	LevelPublic   = "public"
	LevelUnlisted = "unlisted"
	LevelPrivate  = "private"
)

// IsValidLevel validates level values (empty string is treated as unset, valid, equivalent to public).
func IsValidLevel(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", LevelPublic, LevelUnlisted, LevelPrivate:
		return true
	}
	return false
}

// NormalizeLevel normalizes: empty → public; invalid value → empty string (caller uses this to return 400).
//
// Why not also treat invalid values as public: if a miswritten level (e.g. "pubilc" instead of "public") is silently
// treated as public, it's essentially exposing content that was meant to be restricted — better to reject the whole batch
// and let the operator fill it in again.
func NormalizeLevel(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return LevelPublic
	}
	if !IsValidLevel(v) {
		return ""
	}
	return v
}

// LevelRank looseness ranking (public is the loosest).
//
// Empty string/invalid values return **0**, do not interpret as public: LoosestLevel relies on 0 to express "this source
// has no opinion", otherwise "directory unlisted + file has no level" would be computed as public — listing content that
// shouldn't be listed.
//
// When the same content is matched by multiple sources (directory + single file checked), take the **loosest** one: if
// the directory is set to unlisted and a specific file inside is set to public, that file should be public. Taking the
// strictest would silently nullify "I specifically relaxed this one" — users see "I changed it, but nothing happened" in the UI.
func LevelRank(v string) int {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case LevelPublic:
		return 3
	case LevelUnlisted:
		return 2
	case LevelPrivate:
		return 1
	}
	return 0
}

// LoosestLevel returns the looser of two levels.
//
// Empty string = "this source has no opinion", so when both are empty, return empty (not public) — callers use
// empty string to determine "this content isn't shared at all". Use NormalizeLevel if you want the "default value".
func LoosestLevel(a, b string) string {
	ra, rb := LevelRank(a), LevelRank(b)
	if ra == 0 && rb == 0 {
		return ""
	}
	if ra >= rb {
		return NormalizeLevel(a)
	}
	return NormalizeLevel(b)
}
