package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// open 测试辅助：硬链接判定改收 *os.File 之后，用例必须真开句柄。
func openRO(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// TestRejectHardlink 硬链接判定的最小闭环：真造一个 inode 两个名字。
//
// 为什么不在单测里 mock FileInfo：判定的输入就是 nlink，mock 出来的数字等于
// 把被测逻辑重写一遍，什么也证明不了。
//
// 这个用例在 Windows 上也必须跑（不能 t.Skip）：Windows 的这条防线以前是空的，
// 就是因为没人真在 Windows 上验证过。
func TestRejectHardlink(t *testing.T) {
	if !NlinkSupported() {
		t.Fatal("本平台必须能拿到链接数")
	}
	base := t.TempDir()
	inside := filepath.Join(base, "inside.txt")
	alias := filepath.Join(base, "alias.txt")
	if err := os.WriteFile(inside, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 单名字：放行
	if err := RejectHardlink(inside, openRO(t, inside)); err != nil {
		t.Fatalf("单名字的普通文件不该被拒: %v", err)
	}

	if err := os.Link(inside, alias); err != nil {
		t.Skipf("本环境不能建硬链接: %v", err)
	}
	f2 := openRO(t, alias)
	if n := NlinkOf(f2); n < 2 {
		t.Fatalf("前置条件失败：链接数应为 2+，实际 %d", n)
	}
	if err := RejectHardlink(alias, f2); err == nil {
		t.Fatal("有两个名字的文件必须被拒")
	}

	// 逃生阀
	t.Setenv("PEERDRIVE_ALLOW_HARDLINKS", "1")
	if err := RejectHardlink(alias, f2); err != nil {
		t.Fatalf("设了逃生阀就不该再拒: %v", err)
	}
}

// TestNlinkOf_TracksBothNames 句柄版的取数必须随链接数变化：同一个 inode，
// 多建一个名字句柄上的数字就得跟着涨——这条断了，判定就成了摆设。
func TestNlinkOf_TracksBothNames(t *testing.T) {
	base := t.TempDir()
	a := filepath.Join(base, "a.txt")
	if err := os.WriteFile(a, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	f := openRO(t, a)
	before := NlinkOf(f)
	if before != 1 {
		t.Fatalf("新文件链接数应为 1，实际 %d", before)
	}
	b := filepath.Join(base, "b.txt")
	if err := os.Link(a, b); err != nil {
		t.Skipf("本环境不能建硬链接: %v", err)
	}
	if after := NlinkOf(f); after <= before {
		t.Fatalf("多了一个名字后链接数应变大：before=%d after=%d", before, after)
	}
}

// TestRejectHardlink_IgnoresNonRegular 目录/设备文件不参与：目录天然 nlink>1，
// 判进来的话整个目录树都登记不了。
//
// 目录不能直接 os.Open 出来当句柄（Windows 上不给开），所以这里改用"拿不到句柄"
// 的路径走 nil 分支，另外用一个**打开着的目录句柄**覆盖非普通文件的判定。
// 目录不能直接保证能 os.Open 出来当句柄（取决于平台有没有给 CreateFile 传
// FILE_FLAG_BACKUP_SEMANTICS），拿不到就退一步只验 nil 句柄那条路。
func TestRejectHardlink_IgnoresNonRegular(t *testing.T) {
	dir := t.TempDir()
	if df, err := os.Open(dir); err == nil {
		t.Cleanup(func() { _ = df.Close() })
		if err := RejectHardlink(dir, df); err != nil {
			t.Fatalf("目录不该被硬链接判定拦: %v", err)
		}
	} else if runtime.GOOS != "windows" {
		t.Fatal(err)
	}
	if err := RejectHardlink(dir, nil); err != nil {
		t.Fatalf("没拿到句柄时按放行处理: %v", err)
	}
}
