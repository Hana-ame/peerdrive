// Package pathutil does one thing: determine "whether a path falls within a given root directory".
//
// Why a separate package: this check is used in three places, and the three checks must be
// **identical** -- file registration (`service.FileService`), outbound serving (`transport.FileIndexService`
// serveFile / `source.LocalSource`), and shared manifest filtering (`service.NodeShare`).
// Previously each wrote its own `filepath.Rel` + `HasPrefix`, which produced a classic composition error:
// registration allowed (inside the storage root), the shared manifest listed it too (ShareDirs prefix match),
// but the read check said "out of bounds" and fell back to a non-existent content-addressed copy --
// the peer saw "visible in the manifest, but read failed on fetch".
//
// Cross-platform notes (Linux/macOS/Windows must all be right):
//   - Separators: `filepath.Rel` carries platform semantics; don't hand-write `strings.HasPrefix(a+"/")`
//     (Windows uses `\`, and `/` is also accepted, so hand-writing is guaranteed to be wrong).
//   - Case: Windows (NTFS) defaults to case-insensitive, so `C:\Data` and `c:\data` are the same
//     directory; Linux/ext4 is case-sensitive. So Windows compares case-folded, other platforms don't.
//   - Drives/volumes: `filepath.Rel("C:\\a", "D:\\a")` errors directly, naturally blocking cross-drive;
//     but if drive letters differ in case (C: vs c:), Rel still returns a result, which the folding above handles.
//   - Symlinks: resolve then check (best-effort). Symlinks inside the directory pointing outside -> out of bounds,
//     by design (less is more; never give more than intended).
package pathutil

import (
	"path/filepath"
	"runtime"
	"strings"
)

// SplitList splits comma-separated directory configuration (like PEERDRIVE_SHARE_DIRS).
// Trims whitespace, drops empty items; does **not** make paths absolute (the caller knows its base directory).
func SplitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// resolveBestEffort resolves symlinks as far as possible: when the whole path can't be resolved,
// it resolves the **longest existing prefix** and concatenates the remainder as-is.
//
// Why it's needed (happened on darwin, CI only had the macOS cell red): `t.TempDir()` on
// macOS is `/var/folders/...`, and `/var` is a symlink to `/private/var`. When the
// **last segment of path does not yet exist** (write targets, symlink targets -- very common),
// `EvalSymlinks` fails outright and returns `/var/...` as-is; while root exists and was resolved
// to `/private/var/...`. Two paths in the same directory end up treated as two trees, and
// `filepath.Rel` returns a string of `..` -> classified as out of bounds (manifesting as
// "files in the shared directory can't be read").
//
// The fix is prefix fallback: resolve as deep as possible, keep the non-existent tail as-is.
// This way root and path always land on the same tree.
func resolveBestEffort(abs string) string {
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	vol := filepath.VolumeName(abs)
	sep := string(filepath.Separator)
	rest := strings.Trim(strings.TrimPrefix(abs, vol), sep)
	if rest == "" {
		return abs
	}
	segs := strings.Split(rest, sep)
	for i := len(segs) - 1; i >= 0; i-- {
		prefix := vol + sep + strings.Join(segs[:i], sep)
		resolved, err := filepath.EvalSymlinks(prefix)
		if err != nil {
			continue
		}
		tail := strings.Join(segs[i:], sep)
		if tail == "" {
			return resolved
		}
		return resolved + sep + tail
	}
	return abs
}

// Within determines whether path is within root (root itself counts as within).
// root or path empty -> false (no default fallback: an empty root equals "allow everything").
func Within(root, path string) bool {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	r, ok := normalize(root)
	if !ok {
		return false
	}
	p, ok := normalize(path)
	if !ok {
		return false
	}
	if r == p {
		return true
	}
	// Rel already handles separators and cross-drive (returns error for cross-drive)
	rel, err := filepath.Rel(r, p)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	// Defense: Rel theoretically should not return an absolute path; if it does, they're on different trees
	return !filepath.IsAbs(rel)
}

// WithinAny determines whether path falls within any of the roots.
func WithinAny(roots []string, path string) bool {
	for _, r := range roots {
		if Within(r, path) {
			return true
		}
	}
	return false
}

// foldCase whether to fold case: Windows (NTFS) defaults to case-insensitive, so `C:\Data` and
// `c:\data` are the same directory; Linux/ext4 is case-sensitive, and folding would incorrectly
// treat two different paths as the same.
//
// Made a **variable** rather than reading runtime.GOOS directly so that unit tests can verify
// the Windows branch on any platform -- otherwise CI running on Linux would always skip it,
// meaning Windows semantics are never tested.
var foldCase = runtime.GOOS == "windows"

// normalize turns a path into a comparable form: absolute path -> Clean -> resolve symlinks (best-effort) ->
// fold case as needed. If resolution fails (path does not exist), keep the Cleaned absolute path --
// very common when registering a file that hasn't been written to disk yet; we can't reject it as
// out of bounds just because stat failed.
func normalize(p string) (string, bool) {
	// NUL byte: Go's os.Open will reject it (`invalid argument`), so a pure syscall check alone can't
	// let it through; but string-level checks (Rel/Clean) don't recognize NUL, and `root/x\x00../../etc/passwd`
	// computes as "within root" at the string level. Explicitly reject it so that nobody later uses Within
	// to gate a path they assemble for commands/logging and gets bypassed.
	if strings.IndexByte(p, 0) >= 0 {
		return "", false
	}
	// Windows reserved device names (`CON`/`NUL`/`COM1`...): textually within root but actually
	// pointing to a device, so "within root" is meaningless. Only enabled on Windows -- on Linux
	// they are just ordinary filenames.
	if foldCase && hasReservedNameIn(p) {
		return "", false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	abs = filepath.Clean(resolveBestEffort(abs))
	if foldCase {
		// Windows: the same directory may have an 8.3 short name alias (e.g. `C:\PROGRA~1`).
		// Without expanding, a shared directory configured with the long name will judge the
		// short name as out of bounds (the file is actually inside but unreadable).
		// See shortname_windows.go.
		// Detail (measured 2026-09-20): Windows EvalSymlinks does expand short names for
		// **existing** paths, but is helpless when "the last segment doesn't exist yet" -- which
		// is exactly what all write operations and symlink targets look like. So we can't rely
		// on EvalSymlinks for this step.
		abs = filepath.Clean(ExpandShortNames(abs))
		abs = strings.ToLower(abs)
	}
	return abs, true
}
