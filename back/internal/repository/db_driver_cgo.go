//go:build cgo

package repository

// When cgo is available, use mattn/go-sqlite3 (C bindings, smaller binary,
// consistent with CI/local development).
//
// Why split driver selection into two files: the release workflow
// (.github/workflows/release.yml) cross-compiles with CGO_ENABLED=0 for
// ALL platforms (configuring mingw on Windows/macOS is too cumbersome),
// while mattn/go-sqlite3 degrades to a stub driver without cgo —
// sql.Open does not error, the first actual SQL execution returns
// "go-sqlite3 requires cgo to work".
// Result: released binaries die immediately on table creation in InitDB
// (previously Windows CI only built, didn't test, so this never surfaced).
// Without cgo, use the pure Go driver — see db_driver_pure.go.

import _ "github.com/mattn/go-sqlite3"

const sqliteDriver = "sqlite3"

// dsnSuffix returns the connection string suffix (write lock wait + foreign key
// constraints). The two drivers have different syntax, so each file provides
// its own — the driver is chosen at compile time, putting it in the wrong file
// means that build silently breaks.
//
// Why busy_timeout is mandatory: SQLite returns "database is locked" IMMEDIATELY
// on write lock conflict by default. This process does concurrent writes (HTTP
// registration / upload commit / BT completion callback / file_index cursor sync
// all happen simultaneously); without it, it's a lottery: single-run never
// reproduces, concurrency causes sporadic 500s. 5s is enough to cover normal
// transaction durations.
//
// Why foreign_keys is explicitly enabled: SQLite DISABLES foreign keys by
// default, while the schema has ON DELETE CASCADE (collection_entries →
// collections, etc.). Without these cascades are just paper constraints —
// delete a collection and entries become orphans, showing rows pointing to
// nonexistent content.
// Issue #277: journal_mode=WAL enables Write-Ahead Logging to eliminate reader/writer blocking.
func dsnSuffix() string { return "?_busy_timeout=5000&_foreign_keys=1&_journal_mode=WAL" }
