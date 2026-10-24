// Package db is the SQLite data layer of the todo app: connection setup,
// embedded schema migrations, and typed access to categories and todos.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver (CGO is not available)
)

var (
	ErrNotFound        = errors.New("not found")        // todo not found
	ErrInvalidCategory = errors.New("invalid category") // category_id does not exist
)

// Store gives access to the todo database. It is safe for concurrent use.
type Store struct {
	db *sql.DB
}

// dsnParams are applied to every connection the pool opens:
//
//   - foreign_keys(1): SQLite leaves foreign key checks off by default and the
//     setting is per connection, so without it category_id is not validated.
//   - journal_mode(WAL): readers do not block the writer and vice versa.
//   - busy_timeout(5000): a connection waits up to 5s for a lock instead of
//     failing at once with "database is locked".
//   - _txlock=immediate: transactions take the write lock at BEGIN. A deferred
//     transaction that reads first and then writes can fail with SQLITE_BUSY
//     no matter how long busy_timeout is; an immediate one simply waits.
const dsnParams = "_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"

// Open opens the SQLite database at path, creating the file and its parent
// directory if they do not exist, and applies pending migrations. path may be
// relative or absolute and may contain spaces; it must name a file (":memory:"
// is not supported because every pooled connection would get its own database).
func Open(path string) (*Store, error) {
	migrations, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open migrations: %w", err)
	}
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}
	if err := migrate(context.Background(), db, migrations); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database %q: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// openDB opens the connection pool for path without running migrations.
func openDB(path string) (*sql.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	// The DSN is the plain path, not a "file:" URI. The driver only cuts the
	// query off a plain path and hands the rest to SQLite untouched, so the
	// path is taken literally. In a "file:" URI, "%", "#" and "?" would be read
	// as escapes, fragment and query, and the path would have to be escaped.
	// The pool is deliberately not limited to one connection: concurrent
	// writers are serialized by SQLite via _txlock=immediate and busy_timeout.
	db, err := sql.Open("sqlite", path+"?"+dsnParams)
	if err != nil {
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	// sql.Open connects lazily; ping now so a bad path or DSN fails here.
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database %q: %w", path, err)
	}
	return db, nil
}
