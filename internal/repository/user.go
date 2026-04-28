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

// ──────────────────────────────
//  Group membership
// ──────────────────────────────

func (r *UserRepository) InitGroupSchema() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		CREATE TABLE IF NOT EXISTS user_groups (
			user_id INTEGER NOT NULL,
			group_id INTEGER NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			PRIMARY KEY (user_id, group_id),
			FOREIGN KEY (user_id) REFERENCES users(id),
			FOREIGN KEY (group_id) REFERENCES groups(id)
		)
	`)
	return err
}

// GetGroupsByUsername returns all groups a user belongs to.
func (r *UserRepository) GetGroupsByUsername(username string) ([]model.UserGroup, error) {
	rows, err := r.db.Query(`
		SELECT ug.user_id, ug.group_id, u.username, g.name, ug.created_at
		FROM user_groups ug
		JOIN users u ON u.id = ug.user_id
		JOIN groups g ON g.id = ug.group_id
		WHERE u.username = ?
		ORDER BY g.name
	`, username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var groups []model.UserGroup
	for rows.Next() {
		var g model.UserGroup
		if err := rows.Scan(&g.UserID, &g.GroupID, &g.Username, &g.GroupName, &g.CreatedAt); err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

// AddUserToGroup adds a user to a group by name. Creates the group if it doesn't exist.
func (r *UserRepository) AddUserToGroup(username, groupName string) error {
	// Find user
	user, err := r.GetByUsername(username)
	if err != nil {
		return err
	}

	// Upsert group
	result, err := r.db.Exec(`
		INSERT INTO groups (name) VALUES (?)
		ON CONFLICT(name) DO UPDATE SET name = name
	`, groupName)
	if err != nil {
		return err
	}
	groupID, _ := result.LastInsertId()
	if groupID == 0 {
		err = r.db.QueryRow("SELECT id FROM groups WHERE name = ?", groupName).Scan(&groupID)
		if err != nil {
			return err
		}
	}

	// Add membership
	_, err = r.db.Exec(`
		INSERT OR IGNORE INTO user_groups (user_id, group_id) VALUES (?, ?)
	`, user.ID, groupID)
	return err
}

// RemoveUserFromGroup removes a user from a group.
func (r *UserRepository) RemoveUserFromGroup(username, groupName string) error {
	_, err := r.db.Exec(`
		DELETE FROM user_groups
		WHERE user_id = (SELECT id FROM users WHERE username = ?)
		AND group_id = (SELECT id FROM groups WHERE name = ?)
	`, username, groupName)
	return err
}


// CountUsers returns the total number of registered users.
func (r *UserRepository) CountUsers() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	return count, err
}
