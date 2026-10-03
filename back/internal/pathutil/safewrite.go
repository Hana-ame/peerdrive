package pathutil

// Write paths also need Root-based ops -- previously only the read side (SafeOpen) eliminated TOCTOU;
// copy/delete/upload to disk were still the classic two-step:
//
//	Within(root, path) -> pass -> os.WriteFile(path) / os.Remove(path)
//
// Between verification and disk write there's a path resolution: someone who can write to a shared
// directory can replace a **directory component** in the path with a symlink (or pre-place a symlink
// pointing outside) between the two steps, steering the write/delete outside the root. Both os.WriteFile
// and os.Remove will faithfully follow symlinks.
//
// Here we use the same approach: open an os.Root on the **allowed root** first, then let it resolve rel
// and write to disk in one go. os.Root uses openat2(RESOLVE_BENEATH) on Linux, directory handles +
// per-segment confirmation on other platforms. Any symlink pointing outside the root fails at resolution time.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pickRoot finds the first root that actually contains path, returning that root and rel of path relative to it.
//
// Unlike normalize, this **intentionally does NOT resolve symlinks or fold case**:
//   - Resolution is left to os.Root itself as the TOCTOU-immune layer; doing another stat here
//     re-opens a window (and the target file may not exist yet, so EvalSymlinks would always fail);
//   - Case folding would turn Upper.Foo into upper.foo on disk, which is a silent rename on Windows.
//
// String-level defense in depth is still needed (NUL, reserved device names, .. escapes, absolute paths).
func pickRoot(roots []string, path string) (string, string, error) {
	if strings.IndexByte(path, 0) >= 0 {
		return "", "", fmt.Errorf("%w: invalid path (NUL byte)", ErrOutsideRoot)
	}
	// Windows reserved device names (`CON`/`NUL`/`COM1`...): writing to them goes straight to the device.
	if foldCase && hasReservedNameIn(path) {
		return "", "", fmt.Errorf("%w: reserved device name: %s", ErrOutsideRoot, path)
	}
	if len(roots) == 0 {
		return "", "", fmt.Errorf("%w: no allowed root configured", ErrOutsideRoot)
	}
	cp, err := filepath.Abs(path)
	if err != nil {
		return "", "", fmt.Errorf("%w: %s", ErrOutsideRoot, path)
	}
	cp = filepath.Clean(cp)
	if foldCase {
		// Consistent with normalize: on Windows expand 8.3 short names first. Without expanding here,
		// Within (which compares long names) and here would produce different rels for the same directory.
		cp = filepath.Clean(ExpandShortNames(cp))
	}

	sep := string(filepath.Separator)
	for _, r := range roots {
		if strings.TrimSpace(r) == "" || strings.IndexByte(r, 0) >= 0 {
			continue
		}
		cr, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		cr = filepath.Clean(cr)
		if foldCase {
			// Use the same convention as cp: the allowed root itself may also be an 8.3 short name
			// (GitHub's Windows runner t.TempDir() is `C:\Users\RUNNER~1\...`). Expanding only path
			// but not root means they're computed as two trees -> a write that is actually within root
			// is judged out of bounds (the first real Windows CI run had three red tests).
			cr = filepath.Clean(ExpandShortNames(cr))
		}
		rel, err := filepath.Rel(cr, cp)
		if err != nil {
			continue // Cross-drive / can't compute relative path: try next root
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+sep) {
			continue
		}
		if filepath.IsAbs(rel) || rel == "" {
			continue
		}
		return cr, rel, nil
	}
	return "", "", fmt.Errorf("%w: %s (checked %d roots)", ErrOutsideRoot, path, len(roots))
}

// withRoot executes fn on a valid allowed root (rel is guaranteed to have no .. escapes).
//
// Uses os.Root when supported; when the filesystem doesn't support it and the operator has explicitly
// enabled the escape hatch, falls back to path-based operations (see rootprobe.go). Both modes go through
// scopedOps for every operation, so call sites don't need branching.
func withRoot(roots []string, path string, fn func(ops scopedOps, rel string) error) error {
	cr, rel, err := pickRoot(roots, path)
	if err != nil {
		return err
	}
	ops, err := openScoped(cr)
	if err != nil {
		return err
	}
	defer ops.Close()
	if err := fn(ops, rel); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// mkdirParent creates the parent directories for rel.
func mkdirParent(ops scopedOps, rel string, perm os.FileMode) error {
	parent := filepath.Dir(rel)
	if parent == "." || parent == string(filepath.Separator) {
		return nil
	}
	return ops.mkdirAll(parent, perm)
}

// rejectSelf refuses to operate on "the root directory itself": rel == "." means the target is the
// allowed root itself. Deleting/overwriting it has no legitimate use, and it must be an explicit
// rule rather than relying on callers to remember.
func rejectSelf(rel string) error {
	if rel == "." {
		return fmt.Errorf("%w: refusing to operate on the root directory itself", ErrOutsideRoot)
	}
	return nil
}

// SafeWriteFileAny writes a file within an allowed root (parent directories auto-created, 0644 etc. from caller).
// Do not replace with os.WriteFile: it follows symlinks outside the root.
func SafeWriteFileAny(roots []string, path string, data []byte, perm os.FileMode) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		if err := mkdirParent(ops, rel, 0o755); err != nil {
			return err
		}
		return ops.writeFile(rel, data, perm)
	})
}

// SafeOpenFileAny opens a file within an allowed root with arbitrary flags (write/append/resume chunked upload).
// With O_CREATE, parent directories are auto-created -- one fewer MkdirAll call is one fewer window.
//
// The returned *os.File remains valid after Root is closed (the open operation is already done).
func SafeOpenFileAny(roots []string, path string, flag int, perm os.FileMode) (*os.File, error) {
	var f *os.File
	err := withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		if flag&os.O_CREATE != 0 {
			if err := mkdirParent(ops, rel, 0o755); err != nil {
				return err
			}
		}
		var err error
		f, err = ops.openFile(rel, flag, perm)
		return err
	})
	if err != nil {
		return nil, err
	}
	return f, nil
}

// SafeMkdirAllAny creates directories within an allowed root.
func SafeMkdirAllAny(roots []string, path string, perm os.FileMode) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.mkdirAll(rel, perm)
	})
}

// SafeRemoveAny removes a single file (or empty directory) within an allowed root.
//
// Why it matters: os.Remove follows symlinks on the **parent directory**, turning "allowed to edit
// my own directory" into "can delete any file". With Root, path resolution and unlink are done in one step.
func SafeRemoveAny(roots []string, path string) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.remove(rel)
	})
}

// SafeRemoveAllAny recursively deletes within an allowed root, with the same boundary as SafeRemoveAny.
func SafeRemoveAllAny(roots []string, path string) error {
	return withRoot(roots, path, func(ops scopedOps, rel string) error {
		if err := rejectSelf(rel); err != nil {
			return err
		}
		return ops.removeAll(rel)
	})
}
