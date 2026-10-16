package pathutil

// Startup check: "is the config set to share the entire drive?"
//
// Within(root, path) is a **pure inclusion check**: if root is configured as `/` (or `C:\` on Windows),
// it of course allows `/etc/passwd`, and that's not a bug -- it's the literal intent of the config.
// But almost nobody actually wants to share an entire drive; such values are basically misconfigurations
// (e.g. `PEERDRIVE_STORAGE=/` written as an unexpanded env var). Rather than letting the node
// "silently share everything", reject it at startup.

import (
	"path/filepath"
	"strings"
)

// IsUnsafeRoot determines if root is a volume root / filesystem root.
//
//	Linux:   "/"                      -> true
//	Windows: "C:\" "c:\" "D:"         -> true
//	         "\\?\C:\"                -> true (no path remainder after the volume name)
func IsUnsafeRoot(root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	// The "only a drive letter / share name, no path" form (Windows `D:`, UNC `\\server\share`):
	// `D:` isn't even an absolute path (it means "current directory on D:"), and Go's filepath.Abs
	// can't get the current directory of another drive, so it appends it to `<cwd>\D:` -- the operator
	// thinks they're sharing D:, but the actual landing point is a weird directory on C:.
	// This kind of silent semantic drift is treated the same as volume roots (Clean turns `D:` into `D:.`).
	if clean := filepath.Clean(root); clean != "" {
		if vol := filepath.VolumeName(clean); vol != "" {
			if rest := strings.TrimPrefix(clean, vol); rest == "" || rest == "." {
				return true
			}
		}
	}
	r, ok := normalize(root)
	if !ok {
		// Paths that can't be resolved are handled by other checks; not classified as "volume root" here
		return false
	}
	rest := r[len(filepath.VolumeName(r)):]
	rest = strings.TrimPrefix(rest, string(filepath.Separator))
	return rest == ""
}

// UnsafeRoots picks out those configured as volume roots from a batch of candidate root directories.
// Empty strings and invalid paths are skipped (they're handled by other checks).
func UnsafeRoots(roots ...string) []string {
	var bad []string
	for _, r := range roots {
		if strings.TrimSpace(r) == "" {
			continue
		}
		if IsUnsafeRoot(r) {
			bad = append(bad, r)
		}
	}
	return bad
}
