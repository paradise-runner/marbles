package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Default paths
var (
	DefaultStoreDir  = filepath.Join(os.Getenv("HOME"), ".marbles")
	DefaultDBPath    = filepath.Join(DefaultStoreDir, "db.sqlite")
	DefaultConfigPath = filepath.Join(DefaultStoreDir, "config.toml")
	IdentitiesDir    = filepath.Join(DefaultStoreDir, "identities")
)

// storePath returns the effective store path, considering globalStorePath override.
func storePath() string {
	if globalStorePath != "" {
		return globalStorePath
	}
	return DefaultDBPath
}

// OpenDB opens the SQLite database, creating it if necessary,
// and runs pending migrations.
func OpenDB(path string) (*sql.DB, error) {
	if path == "" {
		path = DefaultDBPath
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if err := migrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// EnsureStoreDir creates ~/.marbles/ and identities/ subdirectory.
func EnsureStoreDir() error {
	for _, d := range []string{DefaultStoreDir, IdentitiesDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

// migrate runs forward-only schema migrations.
func migrate(db *sql.DB) error {
	// Create schema_version table if not exists.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}

	migrations := []struct {
		version int
		sql     string
	}{
		{1, `
			CREATE TABLE IF NOT EXISTS items (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				kind TEXT NOT NULL CHECK(kind IN ('task','project')),
				title TEXT NOT NULL CHECK(length(title) > 0),
				body TEXT NOT NULL DEFAULT '',
				status TEXT NOT NULL DEFAULT 'open' CHECK(status IN ('open','review','closed')),
				priority INTEGER NOT NULL DEFAULT 2 CHECK(priority >= 0 AND priority <= 3),
				claimed_by TEXT,
				parent_item INTEGER REFERENCES items(id),
				cwd_hint TEXT,
				created_at INTEGER NOT NULL,
				created_by TEXT NOT NULL,
				updated_at INTEGER NOT NULL,
				closed_at INTEGER
			);
			CREATE INDEX IF NOT EXISTS idx_items_kind ON items(kind);
			CREATE INDEX IF NOT EXISTS idx_items_status ON items(status);
			CREATE INDEX IF NOT EXISTS idx_items_parent ON items(parent_item);

			CREATE TABLE IF NOT EXISTS links (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				from_item INTEGER NOT NULL REFERENCES items(id),
				to_item INTEGER NOT NULL REFERENCES items(id),
				rel TEXT NOT NULL CHECK(rel IN ('blocks','related','parent','child')),
				created_at INTEGER NOT NULL,
				created_by TEXT NOT NULL,
				UNIQUE(from_item, to_item, rel)
			);
			CREATE INDEX IF NOT EXISTS idx_links_from ON links(from_item);
			CREATE INDEX IF NOT EXISTS idx_links_to ON links(to_item);

			CREATE TABLE IF NOT EXISTS comments (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				item INTEGER NOT NULL REFERENCES items(id),
				author TEXT NOT NULL,
				body TEXT NOT NULL,
				created_at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_comments_item ON comments(item);

			CREATE TABLE IF NOT EXISTS events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				item INTEGER REFERENCES items(id),
				actor TEXT NOT NULL,
				verb TEXT NOT NULL,
				detail TEXT NOT NULL DEFAULT '{}',
				at INTEGER NOT NULL
			);
			CREATE INDEX IF NOT EXISTS idx_events_item ON events(item);

			CREATE TABLE IF NOT EXISTS agents (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				name TEXT NOT NULL UNIQUE,
				token_hash TEXT NOT NULL DEFAULT '',
				fingerprint TEXT NOT NULL DEFAULT '',
				created_at INTEGER NOT NULL,
				created_by TEXT NOT NULL
			);
		`},
		{2, `
			PRAGMA writable_schema=ON;
			UPDATE sqlite_master SET sql = REPLACE(
				sql,
				'CHECK(status IN (''open'',''closed''))',
				'CHECK(status IN (''open'',''review'',''closed''))'
			) WHERE name = 'items' AND type = 'table';
			PRAGMA writable_schema=RESET;
		`},
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %d: %w", m.version, err)
		}
		if _, err := tx.Exec("INSERT INTO schema_version (version, applied_at) VALUES (?, ?)", m.version, time.Now().Unix()); err != nil {
			tx.Rollback()
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.version, err)
		}
	}
	return nil
}
