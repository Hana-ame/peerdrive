package repository

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"peerdrive/internal/model"
)

// 发现背景：2026-08-20 审查 doc/TODO-SIMPLIFY.md M2（file_meta + file_providers
// 未收口，文档自标「最高风险」）时，该架构事实此前只靠 grep 计数推断。本测试把
// 「两套索引互不填充」固定为可执行证据——否则任何声称"BT 下载的文件对端能拉到"
// 的说法都需要重新证明。
func TestDataModelSeparationProof(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, InitDB(filepath.Join(dir, "proof.db")), "初始化独立库")

	// 造两个真实文件，各自算出 sha256（与真实登记路径同一算法）
	mkfile := func(name, content string) (path, hash string, size int64) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		sum := sha256.Sum256([]byte(content))
		return p, hex.EncodeToString(sum[:]), int64(len(content))
	}
	pathA, hashA, sizeA := mkfile("bt-download.bin", "from legacy path")
	pathB, hashB, sizeB := mkfile("new-index.bin", "from new path")

	t.Log("旧路径 hash:", hashA)
	t.Log("新路径 hash:", hashB)

	// ── 旧路径：RegisterBTFile 的登记三连，原样复刻 ──
	require.NoError(t, InsertFileMeta(&model.FileMeta{
		Hash: hashA, Size: sizeA, Filename: "bt-download.bin", Type: "binary",
	}))
	require.NoError(t, InsertFileProvider(hashA, "local", pathA))

	// ── 新路径：FileIndexService.Create 的底层写入，原样复刻 ──
	_, err := UpsertFileIndex(hashB, pathB, "new-index.bin", sizeB, false)
	require.NoError(t, err, "新路径写入失败")

	// ══ 结论 1：旧路径文件只进旧表 ══
	assert.Equal(t, 1, mustCount(t, "file_meta", hashA), "旧路径文件应在 file_meta 有 1 行")
	assert.Equal(t, 1, mustCount(t, "file_providers", hashA), "旧路径文件应在 file_providers 有 1 行")

	// ══ 结论 2（关键）：旧路径文件不进新索引 → 对端按 hash 不可发现 ══
	fi, err := GetFileIndex(hashA)
	assert.Error(t, err, "旧路径文件在 file_index 查不到（P2P 不可发现）")
	assert.Nil(t, fi)

	// ══ 结论 3：旧表本身可读回，证明数据没丢，只是索引不可见 ══
	metaA, err := GetFileMeta(hashA)
	require.NoError(t, err)
	assert.Equal(t, hashA, metaA.Hash, "旧表可读回该文件元数据")

	// ══ 结论 4：新路径文件不写旧表 ══
	assert.Equal(t, 0, mustCount(t, "file_meta", hashB), "新路径文件不应写 file_meta")
	assert.Equal(t, 0, mustCount(t, "file_providers", hashB), "新路径文件不应写 file_providers")

	// ══ 结论 5：新路径文件在新表且路径正确 ══
	fi2, err := GetFileIndex(hashB)
	require.NoError(t, err)
	assert.Equal(t, pathB, fi2.Path, "新路径文件应在 file_index 且路径正确")

	// ══ 结论 6：两表行数对称 → 不存在 1:1 镜像/同步 job ══
	totIndex, err := CountAll("file_index")
	require.NoError(t, err)
	totMeta, err := CountAll("file_meta")
	require.NoError(t, err)
	assert.Equal(t, 1, totIndex, "file_index 只有新路径那一条")
	assert.Equal(t, 1, totMeta, "file_meta 只有旧路径那一条")
	t.Logf("分离证明：file_index=%d 行 / file_meta=%d 行，无交叉行", totIndex, totMeta)
}

func mustCount(t *testing.T, table, hash string) int {
	t.Helper()
	n, err := CountByHash(table, "hash", hash)
	require.NoError(t, err)
	return n
}

// CountByHash 按 hash 列计数某表行数（证明用辅助，不做通用抽象）。
func CountByHash(table, col, hash string) (int, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, hash).Scan(&n)
	return n, err
}

// CountAll 统计某表总行数（证明用辅助）。
func CountAll(table string) (int, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM ` + table + ``).Scan(&n)
	return n, err
}
