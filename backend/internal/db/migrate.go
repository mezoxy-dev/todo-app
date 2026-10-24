package db

import (
	"cmp"
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// migrationName matches NNNN_description.sql. The four digits are the schema
// version that is stored in PRAGMA user_version once the file has been applied.
var migrationName = regexp.MustCompile(`^(\d{4})_.+\.sql$`)

type migration struct {
	version int
	name    string // file name, used in error messages
	query   string
}

// migrate applies, in version order, every migration in the root of fsys whose
// version is greater than the database's PRAGMA user_version.
//
// Each migration runs in its own transaction together with the update of
// user_version, so one that fails is rolled back completely and the ones
// before it stay applied. Migration files must not contain transaction
// statements of their own. The whole set is validated before anything runs: a
// file that does not match NNNN_description.sql, or a version used twice, is an
// error.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	migrations, err := loadMigrations(fsys)
	if err != nil {
		return err
	}

	var current int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&current); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		if err := applyMigration(ctx, db, m); err != nil {
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
	}
	return nil
}

// loadMigrations reads and validates the migration files in the root of fsys
// and returns them sorted by version.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	var migrations []migration
	names := make(map[int]string) // version -> file name, to detect duplicates
	for _, e := range entries {
		name := e.Name()
		match := migrationName.FindStringSubmatch(name)
		if match == nil {
			return nil, fmt.Errorf("migration %q: file name must match NNNN_description.sql", name)
		}
		version, err := strconv.Atoi(match[1])
		if err != nil {
			return nil, fmt.Errorf("migration %q: invalid version: %w", name, err)
		}
		if version < 1 {
			// user_version starts at 0, so a version 0 file would never run.
			return nil, fmt.Errorf("migration %q: version must be at least 1", name)
		}
		if other, dup := names[version]; dup {
			return nil, fmt.Errorf("migrations %q and %q have the same version %d", other, name, version)
		}
		names[version] = name

		query, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		migrations = append(migrations, migration{version: version, name: name, query: string(query)})
	}

	slices.SortFunc(migrations, func(a, b migration) int { return cmp.Compare(a.version, b.version) })
	return migrations, nil
}

// applyMigration runs one migration and bumps user_version in a single
// transaction. Both the DDL and the user_version change are transactional in
// SQLite, so a failure leaves the database exactly as it was.
func applyMigration(ctx context.Context, db *sql.DB, m migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	if _, err := tx.ExecContext(ctx, m.query); err != nil {
		return err
	}
	// PRAGMA takes no bound parameters; m.version is an int parsed from the file name.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
		return err
	}
	return tx.Commit()
}
