//go:build !windows

package pathutil

// On non-Windows there are no 8.3 short names, so these functions are identity/empty.
//
// Why not just "reference them only from the Windows side": normalize / pickRoot must compile on
// both platforms, and if the call sites are written out, the definitions must exist. Leaving
// stub implementations is better than splitting normalize with build tags into two files --
// that would be a real maintenance disaster (change one, forget the other).

// ExpandShortNames: no short names to expand on non-Windows: returns as-is.
func ExpandShortNames(p string) string { return p }

// ShortNameOf: no short names on non-Windows.
func ShortNameOf(string) string { return "" }
