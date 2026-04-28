// ShareLink 和 CreateShareRequest 定义文件/合集分享链接的数据库映射与 API 请求结构。
package model

import "time"

type ShareLink struct {
	ID        int64     `json:"id"`
	Token     string    `json:"token"`
	Hash      string    `json:"hash"`
	Type      string    `json:"type"` // "file" or "collection"
	Filename  string    `json:"filename,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

type CreateShareRequest struct {
	Hash     string `json:"hash" binding:"required"`
	Type     string `json:"type" binding:"required"` // file | collection
	Filename string `json:"filename,omitempty"`
}
