package tray

import (
	"runtime"
	"testing"
)

// TestAvailableMatchesPlatform 守住平台分流本身：Windows 上必须有托盘，
// 其他平台必须没有。写反了的 build tag 不会在任何单平台 CI 上暴露，
// 但会让「win 入口在 linux 上编出托盘代码 / linux 入口在 windows 上
// 少托盘」——正是这次平台分化要防的事。
func TestAvailableMatchesPlatform(t *testing.T) {
	switch {
	case runtime.GOOS == "windows" && !Available():
		t.Fatal("windows build must have a tray")
	case runtime.GOOS != "windows" && Available():
		t.Fatal("non-windows build must not have a tray")
	}
}

// TestRunReturnsOnNonWindows：非 Windows 上 Run 必须立刻返回而不是挂起。
func TestRunReturnsOnNonWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows tray loop blocks by design; covered by manual QA")
	}
	if err := Run(Config{Tooltip: "test"}); err != nil {
		t.Fatalf("Run on non-windows should be a no-op, got %v", err)
	}
}
