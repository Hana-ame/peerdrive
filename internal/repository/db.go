package repository

import (
	"database/sql"
	"os"
	"path/filepath"

	_ "github.com/mattn/go-sqlite3"
	"peerdrive/internal/model"
)

var DB *sql.DB
var AnonStorageDir string

func InitDB(dbPath string) error {
	os.MkdirAll(filepath.Dir(dbPath), 0755)
	var err error
	DB, err = sql.Open("sqlite3", dbPath+"?cache=shared&_journal_mode=WAL")
	if err != nil {
		return err
	}
	DB.SetMaxOpenConns(1)
	return migrate(DB)
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS file_meta (
			hash TEXT PRIMARY KEY,
			filename TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			mime_type TEXT NOT NULL DEFAULT '',
			provider_type TEXT NOT NULL DEFAULT 'local',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);
		CREATE TABLE IF NOT EXISTS file_providers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			hash TEXT NOT NULL,
			provider_type TEXT NOT NULL DEFAULT 'local',
			provider_path TEXT NOT NULL,
			available INTEGER NOT NULL DEFAULT 1,
			FOREIGN KEY (hash) REFERENCES file_meta(hash)
		);
		CREATE INDEX IF NOT EXISTS idx_file_providers_hash ON file_providers(hash);
	`)
	return err
}

func SetAnonStorageDir(dir string) { AnonStorageDir = dir }

// --- FileMeta CRUD ---

func InsertFileMeta(f *model.FileMeta) error {
	_, err := DB.Exec(
		`INSERT OR IGNORE INTO file_meta (hash, filename, size, mime_type, provider_type) VALUES (?, ?, ?, ?, ?)`,
		f.Hash, f.Filename, f.Size, f.MimeType, f.ProviderType,
	)
	return err
}

func GetFileMeta(hash string) (*model.FileMeta, error) {
	var f model.FileMeta
	err := DB.QueryRow(
		`SELECT hash, filename, size, mime_type, provider_type, COALESCE(created_at, '') FROM file_meta WHERE hash = ?`,
		hash,
	).Scan(&f.Hash, &f.Filename, &f.Size, &f.MimeType, &f.ProviderType, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &f, err
}

func FileMetaExists(hash string) bool {
	var c int
	DB.QueryRow(`SELECT COUNT(*) FROM file_meta WHERE hash = ?`, hash).Scan(&c)
	return c > 0
}

// --- FileProvider CRUD ---

func InsertFileProvider(p *model.FileProvider) error {
	_, err := DB.Exec(
		`INSERT OR IGNORE INTO file_providers (hash, provider_type, provider_path, available) VALUES (?, ?, ?, ?)`,
		p.Hash, p.ProviderType, p.ProviderPath, 1,
	)
	return err
}

func GetFileProviders(hash string) ([]model.FileProvider, error) {
	rows, err := DB.Query(
		`SELECT hash, provider_type, provider_path, available FROM file_providers WHERE hash = ?`,
		hash,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FileProvider
	for rows.Next() {
		var p model.FileProvider
		rows.Scan(&p.Hash, &p.ProviderType, &p.ProviderPath, &p.Available)
		out = append(out, p)
	}
	return out, nil
}
