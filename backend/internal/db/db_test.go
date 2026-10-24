package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

// wantCategories are the categories seeded by migration 0001, in column order.
var wantCategories = []Category{
	{ID: 1, Name: "Henüz yapılmadı", Position: 1},
	{ID: 2, Name: "Yapılıyor", Position: 2},
	{ID: 3, Name: "Tamamlandı", Position: 3},
}

// newTestStore opens a Store on a fresh database file in t.TempDir(). The store
// is closed when the test ends, before the directory is removed: Windows cannot
// delete files that are still open.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	return openStore(t, filepath.Join(t.TempDir(), "test.db"))
}

// openStore opens the database at path and closes it when the test ends.
func openStore(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

// openRawDB opens a fresh database with openDB only, so no migration has run.
func openRawDB(t *testing.T) *sql.DB {
	t.Helper()
	return openRawDBAt(t, filepath.Join(t.TempDir(), "m.db"))
}

// openRawDBAt is openRawDB for a given path. It is closed when the test ends;
// a test may also close it earlier to release the file.
func openRawDBAt(t *testing.T, path string) *sql.DB {
	t.Helper()
	raw, err := openDB(path)
	if err != nil {
		t.Fatalf("openDB(%q): %v", path, err)
	}
	t.Cleanup(func() { raw.Close() })
	return raw
}

// pragmaInt returns the integer result of "PRAGMA <name>".
func pragmaInt(t *testing.T, db *sql.DB, name string) int {
	t.Helper()
	var v int
	if err := db.QueryRow("PRAGMA " + name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

// userVersion returns PRAGMA user_version of the store's database.
func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	return pragmaInt(t, s.db, "user_version")
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		t.Fatalf("query sqlite_master: %v", err)
	}
	return n > 0
}

// holdConns checks out n connections at the same time. The pool has to open a
// distinct physical connection for each one, which is the only way to check
// settings that apply per connection.
func holdConns(t *testing.T, s *Store, n int) []*sql.Conn {
	t.Helper()
	var conns []*sql.Conn
	t.Cleanup(func() {
		for _, c := range conns {
			c.Close()
		}
	})
	for i := 0; i < n; i++ {
		c, err := s.db.Conn(t.Context())
		if err != nil {
			t.Fatalf("db.Conn #%d: %v", i, err)
		}
		conns = append(conns, c)
	}
	return conns
}

func TestOpenCreatesDirAndAppliesMigrations(t *testing.T) {
	// The directory does not exist yet and its name contains a space, like the
	// absolute DB_PATH of this project ("claude testing").
	path := filepath.Join(t.TempDir(), "nested dir", "todos.db")

	s := openStore(t, path)

	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file was not created: %v", err)
	}
	if got := userVersion(t, s); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
	got, err := s.ListCategories(t.Context())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if !reflect.DeepEqual(got, wantCategories) {
		t.Errorf("ListCategories = %+v, want %+v", got, wantCategories)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todos.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	second := openStore(t, path)

	if got := userVersion(t, second); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
	got, err := second.ListCategories(t.Context())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if !reflect.DeepEqual(got, wantCategories) {
		t.Errorf("ListCategories after reopen = %+v, want %+v (migration must not run twice)", got, wantCategories)
	}
}

func TestOpenRelativePath(t *testing.T) {
	t.Chdir(t.TempDir())

	openStore(t, "data/todos.db")

	if _, err := os.Stat("data/todos.db"); err != nil {
		t.Errorf("database file was not created at the relative path: %v", err)
	}
}

// The DSN is a plain path, not a "file:" URI. In a URI "%20" is an escape, "#"
// starts a fragment and "&" separates parameters; all of them are valid in a
// Windows directory name and must be taken literally.
func TestOpenPathWithURISpecialCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "100%20 #1 & more", "todos.db")

	openStore(t, path)

	if _, err := os.Stat(path); err != nil {
		t.Errorf("database file was not created at the exact path: %v", err)
	}
}

func TestOpenFailsWhenDirectoryCannotBeCreated(t *testing.T) {
	// A regular file where a directory is needed.
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := Open(filepath.Join(blocker, "todos.db"))
	if err == nil {
		s.Close()
		t.Fatal("Open succeeded, want error")
	}
	if s != nil {
		t.Errorf("Open returned a non-nil store together with error %v", err)
	}
}

func TestOpenRollsBackAndReleasesFileWhenMigrationFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todos.db")
	// A "todos" table that migration 0001 does not expect: the migration creates
	// and seeds "categories" first and then fails on CREATE TABLE todos.
	pre := openRawDBAt(t, path)
	if _, err := pre.Exec(`CREATE TABLE todos (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if err := pre.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)

	if err == nil {
		s.Close()
		t.Fatal("Open succeeded, want error from migration 0001_init.sql")
	}
	if !strings.Contains(err.Error(), "0001_init.sql") {
		t.Errorf("error %q does not name the failing file 0001_init.sql", err)
	}
	// Windows cannot delete an open file, so this also shows that Open closed
	// the database it had opened.
	if err := os.Remove(path); err != nil {
		t.Errorf("database file is still in use after the failed Open: %v", err)
	}
}

func TestOpenFailedMigrationLeavesDatabaseUntouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todos.db")
	pre := openRawDBAt(t, path)
	if _, err := pre.Exec(`CREATE TABLE todos (id INTEGER)`); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("Open succeeded, want error")
	}

	if got := pragmaInt(t, pre, "user_version"); got != 0 {
		t.Errorf("user_version = %d, want 0", got)
	}
	if tableExists(t, pre, "categories") {
		t.Error("table categories exists: the failed migration was not rolled back")
	}
}

func TestForeignKeysEnabledOnEveryConnection(t *testing.T) {
	s := newTestStore(t)

	for i, c := range holdConns(t, s, 3) {
		var on int
		if err := c.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&on); err != nil {
			t.Fatalf("connection %d: PRAGMA foreign_keys: %v", i, err)
		}
		if on != 1 {
			t.Errorf("connection %d: foreign_keys = %d, want 1", i, on)
		}
	}
}

func TestJournalModeAndBusyTimeoutOnEveryConnection(t *testing.T) {
	s := newTestStore(t)

	for i, c := range holdConns(t, s, 3) {
		var mode string
		if err := c.QueryRowContext(t.Context(), "PRAGMA journal_mode").Scan(&mode); err != nil {
			t.Fatalf("connection %d: PRAGMA journal_mode: %v", i, err)
		}
		if mode != "wal" {
			t.Errorf("connection %d: journal_mode = %q, want %q", i, mode, "wal")
		}

		var timeout int
		if err := c.QueryRowContext(t.Context(), "PRAGMA busy_timeout").Scan(&timeout); err != nil {
			t.Fatalf("connection %d: PRAGMA busy_timeout: %v", i, err)
		}
		if timeout != 5000 {
			t.Errorf("connection %d: busy_timeout = %d, want 5000", i, timeout)
		}
	}
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	raw := openRawDB(t)
	fsys := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE a (id INTEGER);")},
		"0002_bad.sql": {Data: []byte("CREATE TABLE b (id INTEGER); SELECT * FROM missing_table;")},
	}

	err := migrate(t.Context(), raw, fsys)

	if err == nil {
		t.Fatal("migrate succeeded, want error from 0002_bad.sql")
	}
	if !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Errorf("error %q does not name the failing file 0002_bad.sql", err)
	}
	if got := pragmaInt(t, raw, "user_version"); got != 1 {
		t.Errorf("user_version = %d, want 1 (0001 applied, 0002 rolled back)", got)
	}
	if !tableExists(t, raw, "a") {
		t.Error("table a is missing: the successful migration must stay applied")
	}
	if tableExists(t, raw, "b") {
		t.Error("table b exists: the failed migration was not rolled back")
	}
}

func TestMigrateRejectsInvalidMigrationSet(t *testing.T) {
	sqlFile := func() *fstest.MapFile { return &fstest.MapFile{Data: []byte("CREATE TABLE a (id INTEGER);")} }
	tests := []struct {
		name  string
		files fstest.MapFS
	}{
		{"version is not four digits", fstest.MapFS{"0001_ok.sql": sqlFile(), "2_late.sql": sqlFile()}},
		{"no version prefix", fstest.MapFS{"0001_ok.sql": sqlFile(), "late.sql": sqlFile()}},
		{"not an sql file", fstest.MapFS{"0001_ok.sql": sqlFile(), "0002_notes.txt": sqlFile()}},
		{"duplicate version", fstest.MapFS{"0001_ok.sql": sqlFile(), "0001_other.sql": sqlFile()}},
		{"version zero", fstest.MapFS{"0000_zero.sql": sqlFile()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := openRawDB(t)

			if err := migrate(t.Context(), raw, tc.files); err == nil {
				t.Fatal("migrate succeeded, want error")
			}
			// The whole set is validated before the first migration runs.
			if got := pragmaInt(t, raw, "user_version"); got != 0 {
				t.Errorf("user_version = %d, want 0", got)
			}
			if tableExists(t, raw, "a") {
				t.Error("table a exists: nothing may be applied from an invalid set")
			}
		})
	}
}
