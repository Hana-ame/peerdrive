package repository

import (
	"fmt"
	"strings"
)

// createShaTagsTable creates the sha_tags table for content-addressed file tags (Issue #91).
// Tags are stored outside collection JSON documents so modifying tags does not alter
// the content-addressed SHA-256 hash or invalidate links.
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

// AddShaTag associates a tag with a content hash. Normalizes sha and tag to lowercase trimmed strings.
func AddShaTag(sha, tag string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	tag = strings.ToLower(strings.TrimSpace(tag))
	if sha == "" || tag == "" {
		return fmt.Errorf("sha and tag must not be empty")
	}
	_, err := DB.Exec(`INSERT INTO sha_tags (sha, tag) VALUES (?, ?) ON CONFLICT(sha, tag) DO NOTHING`, sha, tag)
	return err
}

// RemoveShaTag removes a tag associated with a content hash.
func RemoveShaTag(sha, tag string) error {
	if DB == nil {
		return fmt.Errorf("database not initialized")
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	tag = strings.ToLower(strings.TrimSpace(tag))
	_, err := DB.Exec(`DELETE FROM sha_tags WHERE sha = ? AND tag = ?`, sha, tag)
	return err
}

// GetShaTags retrieves all tags associated with a specific content hash.
func GetShaTags(sha string) ([]string, error) {
	if DB == nil {
		return nil, nil
	}
	sha = strings.ToLower(strings.TrimSpace(sha))
	rows, err := DB.Query(`SELECT tag FROM sha_tags WHERE sha = ? ORDER BY tag ASC`, sha)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags, nil
}

// GetShasByTag retrieves all content hashes associated with a tag.
func GetShasByTag(tag string) ([]string, error) {
	if DB == nil {
		return nil, nil
	}
	tag = strings.ToLower(strings.TrimSpace(tag))
	rows, err := DB.Query(`SELECT sha FROM sha_tags WHERE tag = ? ORDER BY created_at DESC`, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		shas = append(shas, s)
	}
	if shas == nil {
		shas = []string{}
	}
	return shas, nil
}
