package pathutil

// SafeOpen -- combines "verify path" and "open file" into one step, eliminating the TOCTOU window.
//
// Previously all read points were two-step:
//
//	Within(root, path) -> pass -> os.Open(path)
//
// In between there's an EvalSymlinks call, and an attacker can replace a path component with a symlink
// after verification but before opening (anyone who can write files in a shared directory can do this).
// To plug it, you can't just "check again"; the **resolution and open must be done atomically by the
// kernel** -- this is Go 1.24's os.Root: on Linux it uses openat2(RESOLVE_BENEATH), other platforms use
// equivalent directory handles + per-segment O_NOFOLLOW. Any symlink that would escape the root fails
// at open time, no window.
//
// Why not just Root.Open(rel) and be done with it: in practice (WSL2 / kernel 5.15), os.Root also
// rejects **in-root symlinks with absolute targets** (Go cannot verify an absolute target without
// escaping). That would break legitimate usage of "organizing a media library with absolute symlinks
// in a shared directory". So here we first normalize (Abs+Clean+EvalSymlinks) the path to a canonical
// form without symlinks, then hand it to os.Root -- the normalized rel has no symlink components, so
// Root accepts it; and normalization itself has already confirmed it's within root.
//
// The only remaining "window" is: an attacker replaces a directory component with a symlink after
// our normalize but before Root.Open. But Root.Open also does a per-segment check again, so the swap
// would cause it to fail to open, and we return an error -- we won't open some other file.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrOutsideRoot: path is not within the allowed root directory.
var ErrOutsideRoot = errors.New("path outside allowed root")

// SafeOpen opens path within root in read-only mode.
//
// The returned *os.File can be used directly: after os.Root is closed, the already-opened fd remains
// valid (verified readable in practice), so callers don't need to hold onto the Root.
func SafeOpen(root, path string) (*os.File, error) {
	cr, ok := normalize(root)
	if !ok {
		return nil, fmt.Errorf("%w: invalid root %q", ErrOutsideRoot, root)
	}
	cp, ok := normalize(path)
	if !ok {
		return nil, fmt.Errorf("%w: invalid path %q", ErrOutsideRoot, path)
	}
	rel, err := filepath.Rel(cr, cp)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	if rel == "" {
		rel = "."
	}

	ops, err := openScoped(cr)
	if err != nil {
		return nil, err
	}
	defer ops.Close()

	f, err := ops.open(rel)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return f, nil
}

// SafeOpenAny opens path within the **first** root that contains it.
// Semantics consistent with WithinAny (matches any one = pass).
func SafeOpenAny(roots []string, path string) (*os.File, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("%w: no allowed root configured", ErrOutsideRoot)
	}
	var last error
	for _, r := range roots {
		f, err := SafeOpen(r, path)
		if err == nil {
			return f, nil
		}
		if last == nil {
			last = err
		}
	}
	if last == nil {
		last = ErrOutsideRoot
	}
	return nil, fmt.Errorf("%w: %s (checked %d roots)", ErrOutsideRoot, path, len(roots))
}
