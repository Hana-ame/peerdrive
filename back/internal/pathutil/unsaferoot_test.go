package pathutil

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestIsUnsafeRoot(t *testing.T) {
	cases := []struct {
		root string
		want bool
	}{
		{"/", true},
		{"//", true},
		{"/tmp", false},
		{"/tmp/", false},
		{"", false},         // 空串由别的校验负责，这里不判
		{"relative", false}, // 相对 cwd，不是卷根
		{".", false},
		{"/data/media", false},
	}
	if runtime.GOOS == "windows" {
		cases = []struct {
			root string
			want bool
		}{
			{`C:\`, true},
			{`c:\`, true},
			{`C:/`, true},
			// "D:" 是**盘符相对**写法（Windows 语义 = D 盘的当前目录，不是 D 盘根）。
			// Go 的 filepath.Abs 拿不到"D 盘当前目录"（进程 cwd 在别的盘），会把它
			// 拼成 <cwd>\D: ——配置意图和实际落点不是一回事，按卷根同等处理。
			{`D:`, true},
			{`C:\data`, false},
			{`\\?\C:\`, true},
			// UNC 共享根：VolumeName 吃掉整段（`\\server\share`），rest 为空 →
			// 判为卷根。语义上也对：这等于把整个网络共享盘共享出去。
			{`\\server\share`, true},
			{`\\server\share\media`, false},
			{"", false},
		}
	}
	for _, c := range cases {
		t.Run(c.root, func(t *testing.T) {
			// 非 Windows 上 Windows 形态的路径没有卷名，跳过（由平台分支自己选表）
			if runtime.GOOS != "windows" && (filepath.VolumeName(c.root) != "" || len(c.root) > 1 && c.root[1] == ':') {
				t.Skip("非 Windows 平台跳过 Windows 形态")
			}
			if got := IsUnsafeRoot(c.root); got != c.want {
				t.Fatalf("IsUnsafeRoot(%q) = %v, want %v", c.root, got, c.want)
			}
		})
	}
}

func TestUnsafeRoots(t *testing.T) {
	got := UnsafeRoots("", "   ", "/data/media", "/")
	if len(got) != 1 || got[0] != "/" {
		t.Fatalf("只该挑出卷根，got=%#v", got)
	}
	if len(UnsafeRoots()) != 0 {
		t.Fatal("没有候选时不应返回东西")
	}
	if len(UnsafeRoots("/tmp", "/var")) != 0 {
		t.Fatal("普通目录不应被挑出来")
	}
}
