package metadata

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	peererr "github.com/Hana-ame/peerdrive/errors"
	"github.com/Hana-ame/peerdrive/interface"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func indexed(metas []iface.FileMetadata) map[string]iface.FileMetadata {
	out := make(map[string]iface.FileMetadata, len(metas))
	for _, m := range metas {
		out[m.FullPath] = m
	}
	return out
}

// traceTree builds a small tree and returns root plus the paths inside it.
func traceTree(t *testing.T) (root, a, b string) {
	t.Helper()
	root = t.TempDir()
	a = filepath.Join(root, "a.txt")
	b = filepath.Join(root, "sub", "b.txt")
	writeFile(t, a, "hello")
	writeFile(t, b, "world")
	return root, a, b
}

func TestIndexRegistersTree(t *testing.T) {
	root, a, b := traceTree(t)
	s := newStore(t)

	got, err := s.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("Index returned %d entries, want 4 (a.txt, sub/b.txt, sub, root): %v", len(got), got)
	}
	byName := indexed(got)
	for _, path := range []string{root, a, b, filepath.Join(root, "sub")} {
		if _, ok := byName[path]; !ok {
			t.Errorf("missing %s from Index result", path)
		}
	}
	for _, m := range got {
		if len(m.SHA256) != 64 {
			t.Errorf("%s: sha %q is not a sha256", m.FullPath, m.SHA256)
		}
		if !filepath.IsAbs(m.FullPath) {
			t.Errorf("%s: FullPath is not absolute", m.FullPath)
		}
		if m.Filename != filepath.Base(m.FullPath) {
			t.Errorf("%s: Filename %q does not match path", m.FullPath, m.Filename)
		}
	}

	// Ordering is part of the contract: two scans must return the same slice.
	for i := 1; i < len(got); i++ {
		if got[i-1].FullPath >= got[i].FullPath {
			t.Fatalf("Index result not sorted by path: %q then %q", got[i-1].FullPath, got[i].FullPath)
		}
	}

	sum := sha256.Sum256([]byte("hello"))
	m, err := s.Stat(hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if m.FullPath != a {
		t.Errorf("Stat returned %s, want %s", m.FullPath, a)
	}
	if m.Size != 5 {
		t.Errorf("Size = %d, want 5", m.Size)
	}
	if !strings.HasPrefix(m.MimeType, "text/plain") {
		t.Errorf("MimeType = %q, want text/plain", m.MimeType)
	}

	// A directory's hash must be reproducible from what List returns: that is
	// the whole point of encoding the children into the parent.
	children, err := s.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	manifest, err := json.Marshal(children)
	if err != nil {
		t.Fatalf("marshal children: %v", err)
	}
	recomputed := sha256.Sum256(manifest)
	if hex.EncodeToString(recomputed[:]) != byName[root].SHA256 {
		t.Errorf("directory hash does not match its manifest:\n stored %s\n listed %s",
			byName[root].SHA256, hex.EncodeToString(recomputed[:]))
	}
	if byName[root].MimeType != DirMimeType {
		t.Errorf("directory MimeType = %q, want %q", byName[root].MimeType, DirMimeType)
	}
	if !byName[root].IsDir {
		t.Error("root is not marked IsDir")
	}
	if !byName[filepath.Join(root, "sub")].IsDir {
		t.Error("sub is not marked IsDir")
	}
	if byName[a].IsDir {
		t.Error("a.txt is marked IsDir")
	}
	if byName[root].Size != int64(len(manifest)) {
		t.Errorf("directory Size = %d, want manifest length %d", byName[root].Size, len(manifest))
	}
}

func TestIndexIsIdempotent(t *testing.T) {
	root, _, _ := traceTree(t)
	s := newStore(t)

	first, err := s.Index(root)
	if err != nil {
		t.Fatalf("first Index: %v", err)
	}
	second, err := s.Index(root)
	if err != nil {
		t.Fatalf("second Index: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("re-indexing changed the result:\nfirst  %+v\nsecond %+v", first, second)
	}

	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM files`).Scan(&rows); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if rows != len(first) {
		t.Errorf("database holds %d rows, want %d", rows, len(first))
	}
}

func TestListReadsOnlyTheDatabase(t *testing.T) {
	root, _, _ := traceTree(t)
	s := newStore(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	// Wipe the tree: List must still answer from SQLite alone.
	if err := os.RemoveAll(root); err != nil {
		t.Fatalf("remove tree: %v", err)
	}
	children, err := s.List(root)
	if err != nil {
		t.Fatalf("List after removal: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("List returned %d children, want 2 (from the database): %v", len(children), children)
	}
}

func TestIndexDropsVanishedFiles(t *testing.T) {
	root, a, _ := traceTree(t)
	s := newStore(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	if err := os.Remove(a); err != nil {
		t.Fatalf("remove %s: %v", a, err)
	}
	if _, err := s.Index(root); err != nil {
		t.Fatalf("re-Index: %v", err)
	}

	children, err := s.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(children) != 1 || children[0].Filename != "sub" {
		t.Fatalf("List = %+v, want only the sub directory", children)
	}
}

func TestListOnAFileRootReturnsIt(t *testing.T) {
	_, a, _ := traceTree(t)
	s := newStore(t)
	if _, err := s.Index(a); err != nil {
		t.Fatalf("Index: %v", err)
	}

	got, err := s.List(a)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].FullPath != a {
		t.Fatalf("List(file) = %+v, want the file itself", got)
	}
	if got[0].IsDir {
		t.Error("List(file) returned an entry marked IsDir")
	}
}

func TestListBeforeIndexingIsEmpty(t *testing.T) {
	s := newStore(t)
	got, err := s.List(t.TempDir())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List before Index = %+v, want empty", got)
	}
}

func TestStatErrors(t *testing.T) {
	s := newStore(t)

	_, err := s.Stat(strings.Repeat("a", 64))
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Stat(unknown) = %v, want ErrNotFound", err)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("z", 64), strings.Repeat("a", 63)} {
		if _, err := s.Stat(bad); !errors.Is(err, ErrInvalidHash) {
			t.Errorf("Stat(%q) = %v, want ErrInvalidHash", bad, err)
		}
	}
}

func TestStatAcceptsUppercaseAndFindsNewestCopy(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one.txt")
	two := filepath.Join(root, "two.txt")
	writeFile(t, one, "same")
	writeFile(t, two, "same")
	s := newStore(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	sum := sha256.Sum256([]byte("same"))
	lower := hex.EncodeToString(sum[:])
	got, err := s.Stat(strings.ToUpper(lower))
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got.SHA256 != lower {
		t.Errorf("Stat returned sha %q, want %q", got.SHA256, lower)
	}
	if got.FullPath != one && got.FullPath != two {
		t.Errorf("Stat returned %s, want one of the two copies", got.FullPath)
	}
}

func TestEmptyDirectoryHashesToEmptyManifest(t *testing.T) {
	root := t.TempDir()
	s := newStore(t)
	got, err := s.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Index returned %d entries, want 1", len(got))
	}
	sum := sha256.Sum256([]byte("[]"))
	if got[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("empty directory sha = %s, want sha256(\"[]\")", got[0].SHA256)
	}
	children, err := s.List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(children) != 0 {
		t.Errorf("List(empty dir) = %+v, want empty", children)
	}
}

func TestContentChangeMovesTheHash(t *testing.T) {
	root, a, _ := traceTree(t)
	s := newStore(t)
	before, err := s.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	writeFile(t, a, "hello again")
	after, err := s.Index(root)
	if err != nil {
		t.Fatalf("re-Index: %v", err)
	}

	beforeByPath, afterByPath := indexed(before), indexed(after)
	if beforeByPath[a].SHA256 == afterByPath[a].SHA256 {
		t.Error("file hash did not change with its content")
	}
	if beforeByPath[root].SHA256 == afterByPath[root].SHA256 {
		t.Error("directory hash did not follow its child's content")
	}
}

func TestIndexSkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks on Windows needs privileges")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "real.txt"), "real")
	if err := os.Symlink(filepath.Join(root, "real.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlink: %v", err)
	}
	s := newStore(t)
	got, err := s.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	for _, m := range got {
		if m.Filename == "link.txt" {
			t.Fatalf("symlink was registered: %+v", m)
		}
	}

	// 以链接本身为 root 时不该硬着头皮跟进去，而是明确报"不支持的类型"。
	_, err = s.Index(filepath.Join(root, "link.txt"))
	if !errors.Is(err, peererr.ErrUnsupportedType) {
		t.Fatalf("Index(symlink) = %v, want errors.ErrUnsupportedType", err)
	}
}

func TestNormalizePathIsAbsolute(t *testing.T) {
	got, err := normalizePath(".")
	if err != nil {
		t.Fatalf("normalizePath: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("normalizePath(.) = %q, want absolute", got)
	}
	if _, err := normalizePath("   "); err == nil {
		t.Fatal("normalizePath(blank) should fail")
	}
}

func TestManifestOrdersByCtimeDescending(t *testing.T) {
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	children := []iface.FileMetadata{
		{Filename: "old.txt", FullPath: "/t/old.txt", Ctime: base},
		{Filename: "new.txt", FullPath: "/t/new.txt", Ctime: base.Add(2 * time.Hour)},
		{Filename: "mid.txt", FullPath: "/t/mid.txt", Ctime: base.Add(time.Hour)},
		// Same instant as mid.txt: filename breaks the tie.
		{Filename: "aaa.txt", FullPath: "/t/aaa.txt", Ctime: base.Add(time.Hour)},
	}

	manifest, err := encodeManifest(children)
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}
	var decoded []iface.FileMetadata
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	want := []string{"new.txt", "aaa.txt", "mid.txt", "old.txt"}
	for i, name := range want {
		if decoded[i].Filename != name {
			t.Fatalf("manifest order = %v, want %v", names(decoded), want)
		}
	}
}

func TestManifestIsEmptyArrayNotNull(t *testing.T) {
	manifest, err := encodeManifest(nil)
	if err != nil {
		t.Fatalf("encodeManifest: %v", err)
	}
	if string(manifest) != "[]" {
		t.Fatalf("empty manifest = %s, want []", manifest)
	}
}

func names(metas []iface.FileMetadata) []string {
	out := make([]string, len(metas))
	for i, m := range metas {
		out[i] = m.Filename
	}
	return out
}

// 错误本体在 errors 包：调用方只 import 那个包也能判断，不必拖进 metadata
// （以及它背后的 SQLite 驱动）。这里钉住"实现返回的错真的能被那个包认出来"。
func TestErrorsMatchSharedSentinels(t *testing.T) {
	s := newStore(t)

	if _, err := s.Stat(strings.Repeat("b", 64)); !errors.Is(err, peererr.ErrNotFound) {
		t.Errorf("Stat(unknown) = %v, want errors.ErrNotFound", err)
	}
	if _, err := s.Stat("nope"); !errors.Is(err, peererr.ErrInvalidHash) {
		t.Errorf("Stat(bad hash) = %v, want errors.ErrInvalidHash", err)
	}
	if _, err := s.Index("   "); !errors.Is(err, peererr.ErrInvalidPath) {
		t.Errorf("Index(blank path) = %v, want errors.ErrInvalidPath", err)
	}
	if _, err := Open("  "); !errors.Is(err, peererr.ErrInvalidPath) {
		t.Errorf("Open(blank path) = %v, want errors.ErrInvalidPath", err)
	}
}
