package repository

import (
	"database/sql"
	"peerdrive-registration/internal/model"
)

type CommentRepository struct {
	db *sql.DB
}

func NewCommentRepository(db *sql.DB) *CommentRepository {
	return &CommentRepository{db: db}
}

func (r *CommentRepository) InitSchema() error {
	_, err := r.db.Exec(`
		CREATE TABLE IF NOT EXISTS comments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hash TEXT NOT NULL,
			username TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_comments_hash ON comments(hash)
	`)
	return err
}

// Create inserts a new comment.
func (r *CommentRepository) Create(hash, username, content string) (*model.Comment, error) {
	result, err := r.db.Exec(
		"INSERT INTO comments (hash, username, content) VALUES (?, ?, ?)",
		hash, username, content,
	)
	if err != nil {
		return nil, err
	}
	id, _ := result.LastInsertId()
	return r.GetByID(id)
}

// GetByID returns a single comment by its ID.
func (r *CommentRepository) GetByID(id int64) (*model.Comment, error) {
	c := &model.Comment{}
	err := r.db.QueryRow(
		"SELECT id, hash, username, content, created_at FROM comments WHERE id = ?", id,
	).Scan(&c.ID, &c.Hash, &c.Username, &c.Content, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ListByHash returns all comments for a given collection hash, ordered by creation time.
func (r *CommentRepository) ListByHash(hash string) ([]model.Comment, error) {
	rows, err := r.db.Query(
		"SELECT id, hash, username, content, created_at FROM comments WHERE hash = ? ORDER BY created_at ASC",
		hash,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var comments []model.Comment
	for rows.Next() {
		var c model.Comment
		if err := rows.Scan(&c.ID, &c.Hash, &c.Username, &c.Content, &c.CreatedAt); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, nil
}

// CountAll returns the total number of comments across all collections.
func (r *CommentRepository) CountAll() (int, error) {
	var count int
	err := r.db.QueryRow("SELECT COUNT(*) FROM comments").Scan(&count)
	return count, err
}
