package repository

import (
	"database/sql"
	"peerdrive-registration/internal/model"

	"golang.org/x/crypto/bcrypt"
)

type UserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) *UserRepository {
	return &UserRepository{db: db}
}

func (r *UserRepository) InitSchema() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'user',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	return err
}

func (r *UserRepository) Create(username, password string, role model.UserRole) (*model.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	result, err := r.db.Exec(
		"INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)",
		username, string(hash), string(role),
	)
	if err != nil {
		return nil, err
	}

	id, _ := result.LastInsertId()
	return r.GetByID(id)
}

func (r *UserRepository) GetByID(id int64) (*model.User, error) {
	u := &model.User{}
	err := r.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE id = ?", id,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *UserRepository) GetByUsername(username string) (*model.User, error) {
	u := &model.User{}
	err := r.db.QueryRow(
		"SELECT id, username, password_hash, role, created_at FROM users WHERE username = ?", username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func (r *UserRepository) VerifyPassword(user *model.User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

func (r *UserRepository) Exists(username string) (bool, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users WHERE username = ?", username).Scan(&count)
	return count > 0, err
}

func (r *UserRepository) ListAll() ([]model.User, error) {
	rows, err := r.db.Query("SELECT id, username, password_hash, role, created_at FROM users ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}
