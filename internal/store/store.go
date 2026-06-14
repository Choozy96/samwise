// Package store owns the SQLite database: connection setup, migrations, and
// (in sibling files) the typed data-access layer. SQLite is embedded — a single
// file on a named volume, no separate DB server (spec §6 DB-hosting note).
//
// Every data-access method that touches user-owned rows takes a user_id from
// the run context and filters by it; model-supplied IDs are never trusted
// (spec §6, §9).
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, no cgo; supports FTS5
)

// DB wraps the *sql.DB handle plus the resolved file path.
type DB struct {
	*sql.DB
	Path string
}

// Open opens (creating if needed) the SQLite database at path with pragmas
// suited to a long-running concurrent service: WAL journaling, enforced foreign
// keys, and a busy timeout so the orchestrator and the short-lived MCP
// subprocess can write concurrently without "database is locked" errors.
func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("creating db dir %s: %w", dir, err)
		}
	}

	// modernc accepts pragmas as query params on the DSN; set the essentials
	// here so every connection in the pool gets them.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	sqlDB, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite %s: %w", path, err)
	}
	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("pinging sqlite %s: %w", path, err)
	}

	return &DB{DB: sqlDB, Path: path}, nil
}
