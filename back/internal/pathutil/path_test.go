package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// 用例全部用 t.TempDir() 现造目录，不依赖任何平台的固定路径
// （Linux 的 /tmp、Windows 的 C:\Users 都不是必然存在的）。

func TestWithin_Basic(t *testing.T) {
	root := t.TempDir()
	inner := filepath.Join(root, "movies", "a.mkv")

	if !Within(root, inner) {
		t.Fatalf("根内文件应判为在内：%s", inner)
	}
	if !Within(root, root) {
		t.Fatal("根目录自身应判为在内")
	}
	// 父子目录只差一个字符，前缀匹配写错就会误放行
	sibling := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-other", "x.txt")
	if Within(root, sibling) {
		t.Fatalf("同名前缀的兄弟目录不应误判为在内：%s", sibling)
	}
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if Within(root, outside) {
		t.Fatalf("根外文件不应判为在内：%s", outside)
	}
}

func TestWithin_EmptyIsRejected(t *testing.T) {
	// 空根等于"全放行"，必须拦住——配置没落地时要表现得"少给"而不是"多给"
	if Within("", "/anything") {
		t.Fatal("空 root 必须拒绝")
	}
	if Within(t.TempDir(), "") {
		t.Fatal("空 path 必须拒绝")
	}
}

func TestWithin_TraversalAndVolume(t *testing.T) {
	root := t.TempDir()
	// 相对路径里的 ".." 逃逸
	if Within(root, filepath.Join(root, "..", "..", "etc", "passwd")) {
		t.Fatal(".. 逃逸必须被拦住")
	}
	// 跨盘/跨卷：Windows 上 C: 与 D: 不可能是包含关系
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(root)
		other := "D:\\some\\file.txt"
		if vol != "" && strings.EqualFold(vol, "C:") && Within(root, other) {
			t.Fatal("跨盘路径不应判为在内")
		}
	}
}

// Windows 的大小写不敏感是**最容易写错又最难在 Linux CI 上发现**的一条：
// 之前写成 `if runtime.GOOS == "windows" { t.Skip() }`，于是 Windows 那一支
// 从头到尾没人跑过。改成显式开关 foldCase，两支在哪个平台都能验。
func TestWithin_CaseFolding(t *testing.T) {
	if foldCase != (runtime.GOOS == "windows") {
		t.Fatalf("foldCase 默认值与平台不符：foldCase=%v GOOS=%s", foldCase, runtime.GOOS)
	}

	old := foldCase
	defer func() { foldCase = old }()
	root := t.TempDir()
	swapped := filepath.Join(swapCaseASCII(root), "a.txt")

	if runtime.GOOS == "windows" {
		// Windows 上 **filepath.Rel 本身就折叠大小写**：path/filepath 的
		// sameWord 在 Windows 上是 strings.EqualFold，逐元素比较时 `C:\Temp`
		// 与 `c:\tEMP` 算同一个。所以 foldCase 取什么值都不改变结果——
		// 这与 NTFS 的语义一致（大小写不同就是同一个目录），是正确的。
		// 2026-09-20 在真机 Windows 上跑出来的：Linux CI 永远发现不了这条。
		if !Within(root, swapped) {
			t.Fatalf("Windows 上大小写不同仍是同一路径，应放行：%s vs %s", root, swapped)
		}
	} else {
		foldCase = false // Linux/ext4：大小写不同就是两个不同的路径
		if Within(root, swapped) {
			t.Fatalf("大小写敏感的文件系统上不应放行：%s vs %s", root, swapped)
		}
	}

	foldCase = true // Windows/NTFS：大小写不同仍是同一个目录
	if !Within(root, swapped) {
		t.Fatalf("大小写不敏感的文件系统上应放行：%s vs %s", root, swapped)
	}

	// 折叠只能让"同一个目录"相等，不能把根外的路径折进来
	foldCase = true
	outside := filepath.Join(filepath.Dir(root), "elsewhere.txt")
	if Within(root, swapCaseASCII(outside)) {
		t.Fatalf("折叠大小写不应把根外路径放进根内：%s", outside)
	}
}

func TestWithin_Symlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		// 非提权 shell 建不了符号链接（开发者模式未开），跳过即可
		t.Skip("Windows 上创建符号链接需要开发者模式/管理员，跳过")
	}
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("本环境不允许建软链：%v", err)
	}
	// 指向根外的软链：解析后判越权（宁可少给）
	if Within(root, link) {
		t.Fatal("指向根外的符号链接必须判为越权")
	}

	// 指向根内的软链：应当放行
	inTarget := filepath.Join(root, "real.txt")
	if err := os.WriteFile(inTarget, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	inLink := filepath.Join(root, "alias.txt")
	if err := os.Symlink(inTarget, inLink); err != nil {
		t.Skipf("本环境不允许建软链：%v", err)
	}
	if !Within(root, inLink) {
		t.Fatal("指向根内的符号链接应放行")
	}
}

func TestWithinAny(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	other := t.TempDir()

	if !WithinAny([]string{a, b}, filepath.Join(b, "x", "y.txt")) {
		t.Fatal("落在第二个根内应放行")
	}
	if WithinAny([]string{a, b}, filepath.Join(other, "x.txt")) {
		t.Fatal("都不在应拒绝")
	}
	if WithinAny(nil, filepath.Join(a, "x.txt")) {
		t.Fatal("没有根时应拒绝")
	}
}

func TestSplitList(t *testing.T) {
	got := SplitList(" /data/a , , /data/b ,, ")
	if len(got) != 2 || got[0] != "/data/a" || got[1] != "/data/b" {
		t.Fatalf("拆分结果不对：%#v", got)
	}
	if SplitList("   ") != nil {
		t.Fatal("空配置应返回 nil")
	}
}

func swapCaseASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'a' && b[i] <= 'z' {
			b[i] -= 32
		} else if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}
