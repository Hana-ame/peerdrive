package repository

import (
	"fmt"
	"strings"
)

// createShaTagsTable initializes the sha_tags mapping table.
func createShaTagsTable() {
	if DB == nil {
		return
	}
	DB.Exec(`CREATE TABLE IF NOT EXISTS sha_tags (
		sha TEXT NOT NULL,
		tag TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY(sha, tag)
	)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_sha_tags_tag ON sha_tags(tag)`)
	DB.Exec(`CREATE INDEX IF NOT EXISTS idx_sha_tags_sha ON sha_tags(sha)`)
}

// GetShaTags retrieves all tags associated with a specific file SHA.
func GetShaTags(sha string) ([]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query(`SELECT tag FROM sha_tags WHERE sha = ? ORDER BY tag ASC`, strings.TrimSpace(sha))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var tag string
		if err := rows.Scan(&tag); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, rows.Err()
}

// SetShaTags replaces all tags for a file SHA with the provided list.
func SetShaTags(sha string, tags []string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	sha = strings.TrimSpace(sha)
	tx, err := DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM sha_tags WHERE sha = ?`, sha); err != nil {
		return err
	}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO sha_tags (sha, tag) VALUES (?, ?)`, sha, tag); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AddShaTag associates a single tag with a file SHA.
func AddShaTag(sha, tag string) error {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return nil
	}
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`INSERT OR IGNORE INTO sha_tags (sha, tag) VALUES (?, ?)`, strings.TrimSpace(sha), tag)
	return err
}

// RemoveShaTag removes a tag from a file SHA.
func RemoveShaTag(sha, tag string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := DB.Exec(`DELETE FROM sha_tags WHERE sha = ? AND tag = ?`, strings.TrimSpace(sha), strings.TrimSpace(tag))
	return err
}

// ListShasByTag finds all file SHAs with the given tag.
func ListShasByTag(tag string) ([]string, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query(`SELECT sha FROM sha_tags WHERE tag = ? ORDER BY created_at DESC`, strings.TrimSpace(tag))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shas []string
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		shas = append(shas, sha)
	}
	if shas == nil {
		shas = []string{}
	}
	return shas, rows.Err()
}

// SearchFilesByTag returns file_index records for files matching the given tag.
func SearchFilesByTag(tag string) ([]FileIndex, error) {
	if DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	rows, err := DB.Query(`
		SELECT f.hash, f.path, f.name, f.size, f.deleted, f.seq, f.created_at, f.updated_at
		FROM file_index f
		INNER JOIN sha_tags t ON f.hash = t.sha
		WHERE t.tag = ? AND f.deleted = 0
		ORDER BY f.seq DESC
		LIMIT 1000`, strings.TrimSpace(tag))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var records []FileIndex
	for rows.Next() {
		var r FileIndex
		var del int
		if err := rows.Scan(&r.Hash, &r.Path, &r.Name, &r.Size, &del, &r.Seq, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		r.Deleted = del != 0
		records = append(records, r)
	}
	if records == nil {
		records = []FileIndex{}
	}
	return records, rows.Err()
}
