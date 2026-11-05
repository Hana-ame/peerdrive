package metadata

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Hana-ame/peerdrive/h2f/interface"
)

// 发现背景：集合按路径回查时有两处最容易写错——顺序是否保留、缺路径是否静默
// 跳过。前者会让上层列表乱序，后者会让「导出成功」其实少文件却看不出来。
func TestCollectionKeepsInputOrder(t *testing.T) {
	s := newStore(t)
	root, a, b := traceTree(t)
	entries, err := s.Index(root)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	byPath := indexed(entries)

	got, err := s.Collection([]string{b, a})
	if err != nil {
		t.Fatalf("Collection: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Collection len = %d, want 2", len(got))
	}
	if got[0].FullPath != b || got[1].FullPath != a {
		t.Fatalf("Collection order = [%s, %s], want [%s, %s]", got[0].FullPath, got[1].FullPath, b, a)
	}
	// 与索引时写入的那一行逐字段对照，确认不是只把路径抄回来了。
	if !reflect.DeepEqual(got[0], byPath[b]) || !reflect.DeepEqual(got[1], byPath[a]) {
		t.Fatalf("Collection returned rows different from Index: got %+v, want %+v / %+v", got, byPath[b], byPath[a])
	}
}

// 发现背景：旧实现里集合少一条只是列表短一格，调用方无从判断；这里改为整体
// 失败并带上缺的那条路径，让「先 Index 再导出」的调用链能定位问题。
func TestCollectionMissingPathFails(t *testing.T) {
	s := newStore(t)
	root, a, _ := traceTree(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}
	missing := filepath.Join(root, "not-indexed.txt")

	_, err := s.Collection([]string{a, missing})
	if !errors.Is(err, iface.ErrNotFound) {
		t.Fatalf("Collection err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("Collection err = %v, want it to name %s", err, missing)
	}
}

// 发现背景：空集合是合法输入（比如刚建的空目录），必须回空切片而不是 nil，
// 否则调用方在 JSON 序列化时会得到 null 而非 []。
func TestCollectionEmpty(t *testing.T) {
	s := newStore(t)
	for _, paths := range [][]string{nil, {}} {
		got, err := s.Collection(paths)
		if err != nil {
			t.Fatalf("Collection(%v): %v", paths, err)
		}
		if got == nil || len(got) != 0 {
			t.Fatalf("Collection(%v) = %#v, want empty non-nil slice", paths, got)
		}
	}
}

// 发现背景：相对路径必须按调用时的工作目录解析成库里的绝对路径，否则同一份
// 集合在从不同目录调用时会一半命中一半 ErrNotFound。
func TestCollectionResolvesRelativePaths(t *testing.T) {
	s := newStore(t)
	root, a, _ := traceTree(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}
	t.Chdir(root)

	got, err := s.Collection([]string{"a.txt"})
	if err != nil {
		t.Fatalf("Collection: %v", err)
	}
	if got[0].FullPath != a {
		t.Fatalf("FullPath = %s, want %s", got[0].FullPath, a)
	}
}

// 发现背景：集合是 list，重复路径按重复返回；去重会悄悄改变顺序语义，上层
// 若靠下标对齐就会错位。
func TestCollectionKeepsDuplicates(t *testing.T) {
	s := newStore(t)
	root, a, _ := traceTree(t)
	if _, err := s.Index(root); err != nil {
		t.Fatalf("Index: %v", err)
	}

	got, err := s.Collection([]string{a, a})
	if err != nil {
		t.Fatalf("Collection: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("Collection len = %d, want 2", len(got))
	}
}
