package repository

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// initTestDB Creates a file-based test database in t.TempDir() and registers
// CloseDB.
//
// File-based rather than ":memory:": the former covers the "database file
// really lands on disk" path (and it's the only path that can cause problems
// on Windows). Must register CloseDB — on Windows an open .db cannot be
// deleted, and t.TempDir()'s RemoveAll will fail.
func initTestDB(t *testing.T) {
	t.Helper()
	require.NoError(t, InitDB(filepath.Join(t.TempDir(), "test.db")))
	t.Cleanup(func() { _ = CloseDB() })
}

// TestCloseDB After closing the database, DB should be nil, and repeated
// closing should not error (idempotent).
func TestCloseDB(t *testing.T) {
	initTestDB(t)
	require.NotNil(t, DB)
	require.NoError(t, CloseDB(), "close should succeed")
	require.Nil(t, DB, "DB should be nil after closing, to avoid leaving closed connections for continued use")
	require.NoError(t, CloseDB(), "repeated closing should be idempotent")
}
