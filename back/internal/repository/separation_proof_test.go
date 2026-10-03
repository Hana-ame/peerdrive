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

// Discovery background: 2026-08-20 while reviewing doc/TODO-SIMPLIFY.md M2
// (file_meta + file_providers not converged, doc self-labeled "highest risk"),
// this architectural fact was previously only inferred from grep counts. This
// test fixes "the two indexes don't fill each other" as executable evidence —
// otherwise any claim that "BT-downloaded files can be pulled by peers" would
// need to be re-proven.
func TestDataModelSeparationProof(t *testing.T) {
	dir := t.TempDir()
	initTestDB(t)

	// Create two real files, each with sha256 computed (same algorithm as the
	// real registration path)
	mkfile := func(name, content string) (path, hash string, size int64) {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
		sum := sha256.Sum256([]byte(content))
		return p, hex.EncodeToString(sum[:]), int64(len(content))
	}
	pathA, hashA, sizeA := mkfile("bt-download.bin", "from legacy path")
	pathB, hashB, sizeB := mkfile("new-index.bin", "from new path")

	t.Log("legacy path hash:", hashA)
	t.Log("new path hash:", hashB)

	// ── Legacy path: RegisterBTFile's registration triple, replicated as-is ──
	require.NoError(t, InsertFileMeta(&model.FileMeta{
		Hash: hashA, Size: sizeA, Filename: "bt-download.bin", Type: "binary",
	}))
	require.NoError(t, InsertFileProvider(hashA, "local", pathA))

	// ── New path: FileIndexService.Create's underlying write, replicated as-is ──
	_, err := UpsertFileIndex(hashB, pathB, "new-index.bin", sizeB, false)
	require.NoError(t, err, "new path write failed")

	// ══ Conclusion 1: legacy path files only go into the old tables ══
	assert.Equal(t, 1, mustCount(t, "file_meta", hashA), "legacy path file should have 1 row in file_meta")
	assert.Equal(t, 1, mustCount(t, "file_providers", hashA), "legacy path file should have 1 row in file_providers")

	// ══ Conclusion 2 (key): legacy path files don't enter the new index →
	// not discoverable by peers via hash ══
	fi, err := GetFileIndex(hashA)
	assert.Error(t, err, "legacy path file not found in file_index (not discoverable via P2P)")
	assert.Nil(t, fi)

	// ══ Conclusion 3: old tables themselves are readable, proving data isn't
	// lost, just the index is invisible ══
	metaA, err := GetFileMeta(hashA)
	require.NoError(t, err)
	assert.Equal(t, hashA, metaA.Hash, "old table can read back the file metadata")

	// ══ Conclusion 4: new path files don't write to old tables ══
	assert.Equal(t, 0, mustCount(t, "file_meta", hashB), "new path file should not write to file_meta")
	assert.Equal(t, 0, mustCount(t, "file_providers", hashB), "new path file should not write to file_providers")

	// ══ Conclusion 5: new path file exists in new table with correct path ══
	fi2, err := GetFileIndex(hashB)
	require.NoError(t, err)
	assert.Equal(t, pathB, fi2.Path, "new path file should be in file_index with correct path")

	// ══ Conclusion 6: two tables have symmetric row counts → no 1:1 mirror/
	// sync job exists ══
	totIndex, err := CountAll("file_index")
	require.NoError(t, err)
	totMeta, err := CountAll("file_meta")
	require.NoError(t, err)
	assert.Equal(t, 1, totIndex, "file_index only has the new path entry")
	assert.Equal(t, 1, totMeta, "file_meta only has the legacy path entry")
	t.Logf("separation proof: file_index=%d rows / file_meta=%d rows, no cross rows", totIndex, totMeta)
}

func mustCount(t *testing.T, table, hash string) int {
	t.Helper()
	n, err := CountByHash(table, "hash", hash)
	require.NoError(t, err)
	return n
}

// CountByHash Counts rows in a table by hash column (proof helper, not a
// general abstraction).
func CountByHash(table, col, hash string) (int, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+col+` = ?`, hash).Scan(&n)
	return n, err
}

// CountAll Counts total rows in a table (proof helper).
func CountAll(table string) (int, error) {
	var n int
	err := DB.QueryRow(`SELECT COUNT(*) FROM ` + table + ``).Scan(&n)
	return n, err
}
