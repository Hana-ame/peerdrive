package main

// 启动期"卷根配置"拦截的测试（doc/NETDISK.md §11.3）。
//
// 为什么要有这份测试：这类配置**过得了**所有运行时边界判定——root 配成 `/`
// 时 `/etc/passwd` 确实"在根内"，pathutil.Within 判 true 是正确行为。
// 唯一能拦住它的地方就是启动期，而启动期逻辑最容易在重构中被删掉。
// 这里把它当 API 测，而不是只测 pathutil.IsUnsafeRoot（后者已有单测）。

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"peerdrive/internal/config"
)

func cfgWith(storage, download, shareDirs string) *config.Config {
	cfg := config.Load()
	cfg.StorageDir = storage
	cfg.DownloadDir = download
	cfg.ShareDirs = shareDirs
	return cfg
}

func TestCheckUnsafeRoots_DefaultConfigIsFine(t *testing.T) {
	// 默认配置（./storage、./downloads、无共享目录）绝不该被拦，
	// 否则 everybody's node 起不来。
	if err := checkUnsafeRoots(cfgWith("./storage", "./downloads", "")); err != nil {
		t.Fatalf("默认配置不该被拒: %v", err)
	}
	if err := checkUnsafeRoots(cfgWith("/data/peerdrive/storage", "/data/peerdrive/dl", "/data/pub,/mnt/media")); err != nil {
		t.Fatalf("正常子目录不该被拒: %v", err)
	}
}

func TestCheckUnsafeRoots_RejectsFilesystemRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("该用例用 POSIX 根 '/'；Windows 分支见 TestCheckUnsafeRoots_WindowsDriveRoot")
	}
	cases := []struct {
		name string
		cfg  *config.Config
		want string // 错误信息里必须点名的配置项
	}{
		{"storage=/", cfgWith("/", "/downloads", ""), "PEERDRIVE_STORAGE"},
		{"download=/", cfgWith("/storage", "/", ""), "PEERDRIVE_DOWNLOAD_DIR"},
		{"share 里混入 /", cfgWith("/storage", "/downloads", "/data/pub,/"), "PEERDRIVE_SHARE_DIRS[1]"},
		{"多个都错", cfgWith("/", "/", "/"), "PEERDRIVE_STORAGE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkUnsafeRoots(tc.cfg)
			if err == nil {
				t.Fatalf("配置 %s 应该被拒，实际放行", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("错误信息该点名 %s，实际: %v", tc.want, err)
			}
		})
	}
}

func TestCheckUnsafeRoots_EscapeHatch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX 根 '/'")
	}
	t.Setenv("PEERDRIVE_ALLOW_UNSAFE_ROOT", "1")
	if err := checkUnsafeRoots(cfgWith("/", "/", "")); err != nil {
		t.Fatalf("设了逃生阀之后不该再拒: %v", err)
	}
}

// Windows 的卷根是盘符：`C:\`、`d:`、`\\?\C:\`。Linux 上 filepath.Abs 不认
// 盘符（`C:\` 会被当成当前目录下名为 `C:\` 的普通子目录），所以这一支
// 只能在 Windows 上真跑——CI 里由 windows 矩阵负责（见 .github/workflows）。
func TestCheckUnsafeRoots_WindowsDriveRoot(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("盘符语义只在 Windows 上成立")
	}
	p := filepath.Join("C:", string(filepath.Separator))
	if err := checkUnsafeRoots(cfgWith(p, filepath.Join("C:", string(filepath.Separator), "storage"), "")); err == nil {
		t.Fatal("storage 配成 C:\\ 应该被拒")
	}
	// 盘符 + 子目录是正常的
	if err := checkUnsafeRoots(cfgWith(filepath.Join("C:", string(filepath.Separator), "data", "storage"), "", "")); err != nil {
		t.Fatalf("C:\\data\\storage 不该被拒: %v", err)
	}
}

// 逃生阀读的是环境变量，测试之间不能互相污染。
func TestMain(m *testing.M) {
	_ = os.Unsetenv("PEERDRIVE_ALLOW_UNSAFE_ROOT")
	os.Exit(m.Run())
}
