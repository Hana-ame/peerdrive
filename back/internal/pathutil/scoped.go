package pathutil

// scopedOps wraps "perform file operations within an allowed root" into a unified interface: use os.Root
// when available, fall back to "absolute path + os.*" when not (and the operator has explicitly
// enabled the escape hatch).
//
// Why this layer: previously each call site had its own if-else, and when E1 was updated, E2 was
// forgotten. Degradation logic lives in one place, correctness is owned by this single type, and
// call sites always write `ops.WriteFile(rel, ...)`.

import (
	"os"
	"path/filepath"
)

type scopedOps struct {
	root *os.Root // nil = degraded to path-based operations
	base string   // used in degraded mode: base/rel is the full path
}

// openScoped establishes an operation handle on an allowed root.
func openScoped(root string) (scopedOps, error) {
	r, err := openRootOrFallback(root)
	if err != nil {
		return scopedOps{}, err
	}
	return scopedOps{root: r, base: root}, nil
}

func (o scopedOps) degraded() bool { return o.root == nil }

// full returns the full path in degraded mode (WriteFile/Remove etc. take a path, not a handle, as the last argument).
func (o scopedOps) full(rel string) string {
	if rel == "." {
		return o.base
	}
	return filepath.Join(o.base, rel)
}

func (o scopedOps) Close() {
	if o.root != nil {
		_ = o.root.Close()
	}
}

func (o scopedOps) open(rel string) (*os.File, error) {
	if o.root != nil {
		return o.root.Open(rel)
	}
	return os.Open(o.full(rel))
}

func (o scopedOps) openFile(rel string, flag int, perm os.FileMode) (*os.File, error) {
	if o.root != nil {
		return o.root.OpenFile(rel, flag, perm)
	}
	return os.OpenFile(o.full(rel), flag, perm)
}

func (o scopedOps) writeFile(rel string, data []byte, perm os.FileMode) error {
	if o.root != nil {
		return o.root.WriteFile(rel, data, perm)
	}
	return os.WriteFile(o.full(rel), data, perm)
}

func (o scopedOps) mkdirAll(rel string, perm os.FileMode) error {
	if o.root != nil {
		return o.root.MkdirAll(rel, perm)
	}
	return os.MkdirAll(o.full(rel), perm)
}

func (o scopedOps) remove(rel string) error {
	if o.root != nil {
		return o.root.Remove(rel)
	}
	return os.Remove(o.full(rel))
}

func (o scopedOps) removeAll(rel string) error {
	if o.root != nil {
		return o.root.RemoveAll(rel)
	}
	return os.RemoveAll(o.full(rel))
}

// Degraded returns whether in degraded mode: these operations **have no** TOCTOU protection
// (paths are resolved a second time). Use this to explain status in logs or reject certain
// high-risk actions.
func (o scopedOps) Degraded() bool { return o.degraded() }
