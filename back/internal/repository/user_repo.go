// Package repository 提供 users 表的 CRUD 操作，包括创建用户、按用户名或 authkey 查询、更新/清除 authkey。
package repository

import (
	"database/sql"
	"errors"
	"peerdrive/internal/model"
)

var ErrUserNotFound = errors.New("user not found")
var ErrUserExists = errors.New("user already exists")

type UserRepository struct{}

// NewUserRepository 创建一个新的用户仓库实例。
func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

// CreateUser 在 users 表中插入新用户记录，并设置返回的 ID。
func (r *UserRepository) CreateUser(user *model.User) error {
	res, err := DB.Exec(
		"INSERT INTO users (username, password_hash) VALUES (?, ?)",
		user.Username, user.PasswordHash,
	)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	user.ID = id
	return nil
}

// GetByUsername 按用户名查询用户；未找到时返回 ErrUserNotFound。
func (r *UserRepository) GetByUsername(username string) (*model.User, error) {
	user := &model.User{}
	err := DB.QueryRow(
		"SELECT id, username, password_hash, authkey, created_at, updated_at FROM users WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.AuthKey, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	return user, err
}

// GetByAuthKey 按 authkey 查询用户；未找到时返回 ErrUserNotFound。
func (r *UserRepository) GetByAuthKey(authKey string) (*model.User, error) {
	user := &model.User{}
	err := DB.QueryRow(
		"SELECT id, username, password_hash, authkey, created_at, updated_at FROM users WHERE authkey = ?",
		authKey,
	).Scan(&user.ID, &user.Username, &user.PasswordHash, &user.AuthKey, &user.CreatedAt, &user.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrUserNotFound
	}
	return user, err
}

// UpdateAuthKey 更新指定用户的 authkey。
func (r *UserRepository) UpdateAuthKey(userID int64, authKey string) error {
	_, err := DB.Exec("UPDATE users SET authkey = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", authKey, userID)
	return err
}

// ClearAuthKey 清除指定用户的 authkey（设为 NULL）。
func (r *UserRepository) ClearAuthKey(userID int64) error {
	_, err := DB.Exec("UPDATE users SET authkey = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", userID)
	return err
}
