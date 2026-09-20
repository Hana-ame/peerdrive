package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestHasReservedName(t *testing.T) {
	yes := []string{
		`C:\data\CON`, `C:\data\con.txt`, `C:\data\NUL`, `C:\data\COM1`,
		`C:\data\LPT9.log`, `C:/data/AUX`, "CON", `\\?\C:\data\COM1:stream`,
		`data\PRN\x.txt`,
	}
	// 注意：函数本身**不区分平台**（平台判断在 normalize 里）。`/data/CON`
	// 在 POSIX 上只是个普通文件名，但"含保留名"这件事是真的，由调用方决定
	// 要不要管——这样 Linux CI 才能单测 Windows 那一支。
	no := []string{
		`C:\data\file.txt`, `C:\data\concert.mp3`, `C:\data\null.txt`,
		`C:\data\com10.txt`, `C:\data\lpt0.txt`, `C:`,
		`C:\data\.`, `C:\data\..`, "",
	}
	for _, p := range yes {
		if !HasReservedName(p) {
			t.Errorf("HasReservedName(%q) = false, want true", p)
		}
	}
	for _, p := range no {
		if HasReservedName(p) {
			t.Errorf("HasReservedName(%q) = true, want false", p)
		}
	}
}

// TestWithin_ReservedDeviceName Windows 上"在根内"的文本判定会被设备名绕过去：
// `root\CON` 文本上在根内，打开拿到的却是控制台设备。必须判为不在根内。
func TestWithin_ReservedDeviceName(t *testing.T) {
	root := t.TempDir()
	old := foldCase
	defer func() { foldCase = old }()

	// foldCase 就是本包里"按 Windows 语义判定"的开关（见 path.go 注释），
	// 所以这条在 Linux CI 上也能验 Windows 那一支。
	foldCase = true
	for _, name := range []string{"CON", "nul.txt", "COM1", "lpt9"} {
		p := filepath.Join(root, name)
		if Within(root, p) {
			t.Errorf("Windows 语义下保留设备名不该算在根内：%s", p)
		}
	}
	// 普通文件名不受影响（CONCERT 只是以 CON 开头，不是设备名）
	foldCase = true
	if !Within(root, filepath.Join(root, "concert.mp3")) {
		t.Error("CONCERT.mp3 是普通文件名，不该被当成设备名")
	}

	foldCase = false // POSIX：CON 就是个普通文件名
	if runtime.GOOS != "windows" && !Within(root, filepath.Join(root, "CON")) {
		t.Error("POSIX 上 CON 是普通文件名，应放行")
	}
}
