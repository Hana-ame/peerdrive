//go:build !cgo

package repository

// Without cgo (CGO_ENABLED=0, cross-compilation / Windows CI / release builds),
// use modernc.org/sqlite — it translates SQLite's C source into Go, requiring
// no C compiler.
//
// Trade-off: larger binary, slower compilation, slightly slower first query.
// Benefit: released Windows/macOS binaries can actually create databases
// (previously a stub that crashed on start), and Windows CI can really run
// tests — the Windows-specific path-semantic branches are no longer just
// passing via t.Skip.
//
// Note on driver name: modernc registers "sqlite", mattn registers "sqlite3".
// So db.go must not hardcode the driver name — use the constant here.

import _ "modernc.org/sqlite"

const sqliteDriver = "sqlite"

// dsnSuffix — see the same-named function in db_driver_cgo.go for the full
// rationale (why busy_timeout / foreign_keys / journal_mode=WAL /
// synchronous=NORMAL are needed, and why each driver has its own version).
//
// Syntax difference: modernc uses `_pragma=name(value)`, mattn uses
// `_name=value`. This file also keeps the `PRAGMA journal_mode=WAL` guarantee
// in the DSN — modernc, unlike mattn, does NOT auto-promote synchronous to
// NORMAL when journal_mode=WAL is set, so the explicit `_synchronous=NORMAL`
// is required here for both drivers to be equivalent.
func dsnSuffix() string {
	return "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
}
