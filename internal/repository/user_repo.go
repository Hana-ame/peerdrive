package repository

import (
	"database/sql"
	"errors"
	"peerdrive/internal/model"
)

var ErrUserNotFound = errors.New("user not found")
var ErrUserExists = errors.New("user already exists")

type UserRepository struct{}

func NewUserRepository() *UserRepository {
	return &UserRepository{}
}

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

func (r *UserRepository) UpdateAuthKey(userID int64, authKey string) error {
	_, err := DB.Exec("UPDATE users SET authkey = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?", authKey, userID)
	return err
}

func (r *UserRepository) ClearAuthKey(userID int64) error {
	_, err := DB.Exec("UPDATE users SET authkey = NULL, updated_at = CURRENT_TIMESTAMP WHERE id = ?", userID)
	return err
}
