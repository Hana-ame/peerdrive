package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// initTestDB 在 t.TempDir() 里建一个文件型测试库，并注册 CloseDB。
//
// 用文件型而不是 ":memory:"：前者能覆盖"库文件真的落盘"这条路径（也是唯一
// 会在 Windows 上出问题的路径）。必须注册 CloseDB——Windows 上打开着的 .db
// 删不掉，t.TempDir() 的 RemoveAll 会失败。
func initTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, InitDB(filepath.Join(t.TempDir(), "test.db")))
	t.Cleanup(func() { _ = CloseDB() })
}

// TestCloseDB 关库后 DB 应为空、重复关闭不报错（幂等）。
func TestCloseDB(t *testing.T) {
	initTestDB(t)
	require.NotNil(t, DB)
	require.NoError(t, CloseDB(), "关闭应成功")
	require.Nil(t, DB, "关库后 DB 应置空，避免留下已关闭的连接被人继续用")
	require.NoError(t, CloseDB(), "重复关闭应幂等")
}
