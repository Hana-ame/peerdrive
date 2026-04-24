package repository

import (
	"database/sql"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func InitDB(dbPath string) error {
	var err error
	DB, err = sql.Open("sqlite3", dbPath)
	if err != nil {
		return err
	}
	schema := `
	CREATE TABLE IF NOT EXISTS files (
		id INTEGER PRIMARY KEY,
		hash TEXT NOT NULL UNIQUE,
		provider_type TEXT NOT NULL,
		path TEXT NOT NULL,
		filename TEXT
	);
	CREATE INDEX IF NOT EXISTS idx_hash ON files(hash);
	`
	_, err = DB.Exec(schema)
	return err
}