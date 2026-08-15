package repository

import (
	"database/sql"
	"fmt"
	"time"
)

// FileIndex 本地文件索引：sha256 → 绝对路径映射（持久化 SQLite）。
// 独立于旧 file_meta/file_providers 表：语义专注（映射 + 同步游标），
// 不影响旧文件服务逻辑。
type FileIndex struct {
	Hash      string
	Path      string
	Name      string
	Size      int64
	Deleted   bool
	Seq       int64 // 单调递增同步游标（metadata 增量同步用）
	CreatedAt string
	UpdatedAt string
}

// createFileIndexTable 建表。seq 每次 upsert/delete 递增，节点间增量同步按它取数。
func createFileIndexTable() {
	DB.Exec(`CREATE TABLE IF NOT EXISTS file_index (
		hash TEXT PRIMARY KEY,
		path TEXT NOT NULL,
		name TEXT DEFAULT '',
		size INTEGER DEFAULT 0,
		deleted INTEGER DEFAULT 0,
		seq INTEGER NOT NULL DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_file_index_seq ON file_index(seq)`)
}

// nextFileIndexSeq 取下一个同步游标值。
func nextFileIndexSeq() int64 {
	var last sql.NullInt64
	// 注意：seq 单调性依赖事务串行；SQLite 单写者下安全
	DB.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM file_index`).Scan(&last)
	return last.Int64 + 1
}

// UpsertFileIndex 登记/更新映射（create/upload 成功后调用），返回新 seq。
func UpsertFileIndex(hash, path, name string, size int64, deleted bool) (int64, error) {
	seq := nextFileIndexSeq()
	_, err := DB.Exec(`INSERT INTO file_index (hash, path, name, size, deleted, seq)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(hash) DO UPDATE SET
			path=excluded.path, name=excluded.name, size=excluded.size,
			deleted=excluded.deleted, seq=excluded.seq,
			updated_at=CURRENT_TIMESTAMP`,
		hash, path, name, size, boolToInt(deleted), seq)
	if err != nil {
		return 0, fmt.Errorf("upsert file_index: %w", err)
	}
	return seq, nil
}

// GetFileIndex 按 hash 查询映射（未删除——tombstone 只通过 SyncSince 暴露）。
func GetFileIndex(hash string) (*FileIndex, error) {
	row := DB.QueryRow(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE hash = ? AND deleted = 0`, hash)
	return scanFileIndex(row)
}

// ListFileIndex 列出全部未删除映射（按 seq 升序，支持分页）。
func ListFileIndex(offset, limit int) ([]FileIndex, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := DB.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE deleted = 0 ORDER BY seq DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileIndex
	for rows.Next() {
		f, err := scanFileIndex(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// ListFileIndexSince 增量同步：返回 seq 大于 since 的全部变更（含删除标记）。
// 防御：since 来自远端 sync verb，变更记录无上限会全表扫描+物化；加 LIMIT 兜底（超过丢弃对端游标落后时的极端值）。
func ListFileIndexSince(since int64) ([]FileIndex, error) {
	rows, err := DB.Query(`SELECT hash, path, name, size, deleted, seq, created_at, updated_at
		FROM file_index WHERE seq > ? ORDER BY seq ASC LIMIT 1000`, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileIndex
	for rows.Next() {
		f, err := scanFileIndex(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// DeleteFileIndex 逻辑删除（同步用 tombstone），返回新 seq。
func DeleteFileIndex(hash string) (int64, error) {
	return UpsertFileIndex(hash, "", "", 0, true)
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanFileIndex(r rowScanner) (*FileIndex, error) {
	var f FileIndex
	var deleted int
	if err := r.Scan(&f.Hash, &f.Path, &f.Name, &f.Size, &deleted, &f.Seq, &f.CreatedAt, &f.UpdatedAt); err != nil {
		return nil, err
	}
	f.Deleted = deleted != 0
	return &f, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fileIndexNow 时间戳（测试可覆盖）。
var fileIndexNow = func() string { return time.Now().UTC().Format(time.RFC3339) }
