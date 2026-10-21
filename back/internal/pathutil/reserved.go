package pathutil

// Windows reserved device names: `CON`, `PRN`, `AUX`, `NUL`, `COM1`...`LPT9`.
//
// These names are interpreted as devices by Win32 in **any directory**, not as a file in that directory:
// `C:\data\CON` opens the console, `C:\data\NUL` is the null device -- so the "path is within root"
// check is bypassed on Windows by device names: the check says "within root" (textually it is),
// but what you actually get from opening is not that file at all. Best case for registering/serving
// such a path is an error, worst case is the request hangs (reading `CON` waits for input).
//
// With extensions it still counts (`CON.txt` is also a device) -- Win32 comparison is "stem name + optional extension".
//
// Why a separate file: the semantics only apply on Windows, but the function itself must be
// unit-testable on Linux CI (doesn't depend on runtime.GOOS branching; the caller decides when to use it).
//
// The filename **must not** be xxx_windows.go: that's Go's implicit GOOS constraint, which would
// exclude it entirely from Linux builds, making the function disappear from Linux CI (hence reserved.go).

import (
	"path/filepath"
	"strings"
)

var reservedDeviceNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// HasReservedName checks if the path contains Windows reserved device names (checks stem names segment by segment, ignoring extensions).
// On non-Windows platforms "it's just a regular filename", but the function is still usable -- cross-platform
// decisions are left to the caller.
func HasReservedName(p string) bool {
	if p == "" {
		return false
	}
	// Recognize both separator types: `C:\data\CON` and `C:/data/CON` are the same thing on Windows
	cleaned := strings.NewReplacer("\\", "/").Replace(p)
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			continue
		}
		// Volume name `C:` is not a device name; `COM1:` (with colon) is, but needs separate handling
		if len(seg) == 2 && seg[1] == ':' {
			continue
		}
		stem := seg
		if i := strings.IndexByte(seg, ':'); i >= 0 {
			stem = seg[:i] // "COM1:stream" -> "COM1"
		}
		if i := strings.IndexByte(stem, '.'); i >= 0 {
			stem = stem[:i] // "CON.txt" -> "CON"
		}
		if reservedDeviceNames[strings.ToUpper(stem)] {
			return true
		}
	}
	return false
}

// hasReservedNameIn for use by normalize: only enabled on Windows.
// Checks the cleaned path (after Clean).
func hasReservedNameIn(p string) bool {
	return HasReservedName(filepath.Clean(p))
}
