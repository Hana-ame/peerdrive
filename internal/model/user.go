// User 及 RegisterRequest/LoginRequest/AuthResponse 定义用户认证相关的数据结构和 API 请求/响应格式。
package model

import "time"

type User struct {
	ID           int64     `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"-"`
	AuthKey      string    `json:"authkey,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type RegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type LoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AuthResponse struct {
	AuthKey  string `json:"authkey"`
	Username string `json:"username"`
}
