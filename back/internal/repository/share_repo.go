// 分享链接仓库 — share_links 表的 CRUD 操作，创建带随机 token 的链接、按 token 查询（检查过期）、列出所有有效链接。
package repository

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"peerdrive/internal/model"
)

// CreateShare 创建一条新的分享链接记录，生成随机 token 并设置 30 天过期时间。
func CreateShare(hash, shareType, filename string) (*model.ShareLink, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(b)

	now := time.Now().UTC()
	exp := now.Add(30 * 24 * time.Hour) // 30 days expiry

	_, err := DB.Exec(`INSERT INTO share_links (token, hash, type, filename, created_at, expires_at)
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

// GetShareByToken 按 token 查询未过期的分享链接。
func GetShareByToken(token string) (*model.ShareLink, error) {
	var s model.ShareLink
	var exp any
	err := DB.QueryRow(`SELECT id, token, hash, type, COALESCE(filename,''), created_at, expires_at
		FROM share_links WHERE token = ? AND (expires_at IS NULL OR expires_at > datetime('now'))`,
		token).Scan(&s.ID, &s.Token, &s.Hash, &s.Type, &s.Filename, &s.CreatedAt, &exp)
	if err != nil {
		return nil, err
	}
	if t, ok := exp.(time.Time); ok {
		s.ExpiresAt = &t
	}
	return &s, nil
}

// ListShares 返回所有未过期的分享链接，按创建时间倒序排列，最多 100 条。
func ListShares() ([]model.ShareLink, error) {
	rows, err := DB.Query(`SELECT id, token, hash, type, COALESCE(filename,''), created_at, expires_at
		FROM share_links WHERE expires_at IS NULL OR expires_at > datetime('now')
		ORDER BY created_at DESC LIMIT 100`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var shares []model.ShareLink
	for rows.Next() {
		var s model.ShareLink
		var exp any
		if err := rows.Scan(&s.ID, &s.Token, &s.Hash, &s.Type, &s.Filename, &s.CreatedAt, &exp); err != nil {
			continue
		}
		if t, ok := exp.(time.Time); ok {
			s.ExpiresAt = &t
		}
		shares = append(shares, s)
	}
	return shares, nil
}

// InitShareTable 创建 share_links 表（如不存在）。
func InitShareTable() {
	DB.Exec(`CREATE TABLE IF NOT EXISTS share_links (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		token TEXT UNIQUE NOT NULL,
		hash TEXT NOT NULL,
		type TEXT NOT NULL DEFAULT 'file',
		filename TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		expires_at DATETIME
	)`)
}
