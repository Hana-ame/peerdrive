// Share link repository — CRUD for share_links table: create links with random tokens, query by token (with expiry check), list all valid links.
package repository

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"

	"peerdrive/internal/model"
)

// CreateShare creates a new share link record with a random token and 30-day expiry.
func CreateShare(hash, shareType, filename string) (*model.ShareLink, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)

	now := time.Now().UTC()
	exp := now.Add(30 * 24 * time.Hour) // 30 days expiry

	_, err := db.Exec(`INSERT INTO share_links (token, hash, type, filename, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, token, hash, shareType, filename, now, exp)
	if err != nil {
		return nil, err
	}

	return &model.ShareLink{
		Token:     token,
		Hash:      hash,
		Type:      shareType,
		Filename:  filename,
		CreatedAt: now,
		ExpiresAt: &exp,
	}, nil
}

// GetShareByToken queries a share link by token that hasn't expired.
// L7: original implementation scanned into *any — driver return type is uncertain
// (time.Time or string), and type assertion failure silently left ExpiresAt empty.
// Use sql.NullTime to explicitly handle NULL/time.
func GetShareByToken(token string) (*model.ShareLink, error) {
	var s model.ShareLink
	var exp sql.NullTime
	err := db.QueryRow(`SELECT id, token, hash, type, COALESCE(filename,''), created_at, expires_at
		FROM share_links WHERE token = ? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		token).Scan(&s.ID, &s.Token, &s.Hash, &s.Type, &s.Filename, &s.CreatedAt, &exp)
	if err != nil {
		return nil, err
	}
	if exp.Valid {
		t := exp.Time
		s.ExpiresAt = &t
	}
	return &s, nil
}

// ListShares returns all unexpired share links, ordered by creation time descending, max 100.
func ListShares() ([]model.ShareLink, error) {
	rows, err := db.Query(`SELECT id, token, hash, type, COALESCE(filename,''), created_at, expires_at
		FROM share_links WHERE expires_at IS NULL OR expires_at > datetime('now')
		ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []model.ShareLink
	for rows.Next() {
		var s model.ShareLink
		var exp sql.NullTime
		if err := rows.Scan(&s.ID, &s.Token, &s.Hash, &s.Type, &s.Filename, &s.CreatedAt, &exp); err != nil {
			continue
		}
		if exp.Valid {
			t := exp.Time
			s.ExpiresAt = &t
		}
		shares = append(shares, s)
	}
	return shares, nil
}

// InitShareTable creates the share_links table (if it doesn't exist).
func InitShareTable() {
	InitShareTableOn(db)
}

func InitShareTableOn(d *sql.DB) {
	if d == nil {
		return
	}
	d.Exec(`CREATE TABLE IF NOT EXISTS share_links (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT UNIQUE NOT NULL,
		hash TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'file',
		filename TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME
	)`)
}
