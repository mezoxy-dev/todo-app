package db

import (
	"database/sql"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// The ids migration 0003 gives the categories in a fresh database. The three
// columns of step 2 had the ids 1-3 and are deleted; AUTOINCREMENT never reuses
// an id, so the new categories start at 4, in the order of their positions.
// wantCategories checks these against the database.
const (
	catInbox int64 = 4 + iota
	catPurchase
	catResearch
	catCommunication
	catPayment
	catAppointment
	catWork
	catHome
	catOther
)

// wantCategories are the categories seeded by migration 0003, in column order.
// The descriptions are those of the SQL in step 3 spec 4, character for
// character: the AI instruction is built from them.
var wantCategories = []Category{
	{ID: catInbox, Key: "inbox", Name: "Gelen kutusu", Position: 0,
		Description: "Sistem sütunu: sınıflandırılmayı bekleyen ya da sınıflandırılamayan kartlar."},
	{ID: catPurchase, Key: "purchase", Name: "Satın alım", Position: 1,
		Description: "Satın alınacak ya da sipariş edilecek şeyler; markete, bakkala ya da pazara gitmek ve alışveriş yapmak. Örnek: süt al, kulaklık sipariş et, bakkala git, market alışverişi yap."},
	{ID: catResearch, Key: "research", Name: "Araştırma", Position: 2,
		Description: "Bilgi toplama, öğrenme, karşılaştırma ya da ne yapılacağı için gerekli bilgisi verilmemiş belirsiz işler. Örnek: tatil için otel bak, X nedir öğren, bir hediye bul."},
	{ID: catCommunication, Key: "communication", Name: "İletişim", Position: 3,
		Description: "Birini arama, mesaj ya da e-posta gönderme. Örnek: annemi ara, Ali'ye mail at."},
	{ID: catPayment, Key: "payment", Name: "Ödeme", Position: 4,
		Description: "Fatura, kira, vergi ve diğer para işleri. Örnek: elektrik faturasını öde."},
	{ID: catAppointment, Key: "appointment", Name: "Randevu & Etkinlik", Position: 5,
		Description: "Tarihi ya da saati olan işler. Örnek: dişçi randevusu al, cuma toplantısı."},
	{ID: catWork, Key: "work", Name: "İş", Position: 6,
		Description: "Mesleki görevler. Örnek: sunumu bitir, kod incelemesi."},
	{ID: catHome, Key: "home", Name: "Ev & Kişisel", Position: 7,
		Description: "Evde yapılan işler, sağlık, spor ve kişisel bakım. Alışverişe gitmek buraya değil Satın alım'a girer. Örnek: çamaşır yıka, spora git, banyo yap."},
	{ID: catOther, Key: "other", Name: "Diğer", Position: 8,
		Description: "Hiçbir kategoriye uymayanlar."},
}

// wantKeys are the keys of wantCategories, in column order.
var wantKeys = []string{
	"inbox", "purchase", "research", "communication", "payment", "appointment", "work", "home", "other",
}

// migrateUpTo applies the embedded migrations whose version is at most n to raw,
// so that a test can fill the database with the data of an earlier step and then
// run the next migration on it.
func migrateUpTo(t *testing.T, raw *sql.DB, n int) {
	t.Helper()
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	files := fstest.MapFS{}
	for _, e := range entries {
		version, err := strconv.Atoi(e.Name()[:4]) // NNNN_description.sql
		if err != nil {
			t.Fatalf("migration %q: %v", e.Name(), err)
		}
		if version > n {
			continue
		}
		data, err := fs.ReadFile(migrationsFS, "migrations/"+e.Name())
		if err != nil {
			t.Fatalf("read migration %q: %v", e.Name(), err)
		}
		files[e.Name()] = &fstest.MapFile{Data: data}
	}
	if err := migrate(t.Context(), raw, files); err != nil {
		t.Fatalf("migrate up to %d: %v", n, err)
	}
	if got := pragmaInt(t, raw, "user_version"); got != n {
		t.Fatalf("user_version = %d after migrating up to %d", got, n)
	}
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
	if got := userVersion(t, s); got != 3 {
		t.Errorf("user_version = %d, want 3", got)
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

	if got := userVersion(t, second); got != 3 {
		t.Errorf("user_version = %d, want 3", got)
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

// Migration 0002 on a database that step 1 filled: every column keeps the order
// it was shown in (newest on top), now as positions 0..n-1. Migration 0001 is
// applied alone first, so the rows are inserted into the schema that has no
// position column yet.
func TestMigration0002BackfillsPositions(t *testing.T) {
	raw := openRawDB(t)
	initSQL, err := fs.ReadFile(migrationsFS, "migrations/0001_init.sql")
	if err != nil {
		t.Fatalf("read migration 0001: %v", err)
	}
	if err := migrate(t.Context(), raw, fstest.MapFS{"0001_init.sql": {Data: initSQL}}); err != nil {
		t.Fatalf("migrate with 0001 only: %v", err)
	}
	if got := pragmaInt(t, raw, "user_version"); got != 1 {
		t.Fatalf("user_version = %d after 0001, want 1", got)
	}

	// Inserted in the order of their ids. Category 2 holds two todos of the same
	// instant: the one with the higher id counts as newer, as it did in step 1.
	for _, r := range []struct {
		title     string
		category  int64
		createdAt string
	}{
		{"A", 1, "2026-01-01T00:00:01.000Z"},
		{"B", 1, "2026-01-01T00:00:02.000Z"},
		{"C", 1, "2026-01-01T00:00:03.000Z"},
		{"D", 3, "2026-01-01T00:00:01.000Z"},
		{"E", 3, "2026-01-01T00:00:02.000Z"},
		{"F", 2, "2026-01-01T00:00:05.000Z"},
		{"G", 2, "2026-01-01T00:00:05.000Z"},
	} {
		_, err := raw.Exec(
			`INSERT INTO todos (title, category_id, created_at, updated_at) VALUES (?, ?, ?, ?)`,
			r.title, r.category, r.createdAt, r.createdAt)
		if err != nil {
			t.Fatalf("insert %q: %v", r.title, err)
		}
	}

	// Only 0002: migration 0003 deletes the three columns this test looks at and
	// has a test of its own.
	migrateUpTo(t, raw, 2)

	s := &Store{db: raw}
	assertOrder(t, s, 1, "C", "B", "A")
	assertOrder(t, s, 2, "G", "F")
	assertOrder(t, s, 3, "E", "D")
	assertDensePositions(t, s)
}

// Migration 0003 on a database that step 2 filled (step 3 spec 4): the status
// comes from the old column, every card moves to the inbox as pending, and the
// inbox is ordered by old column and then old position. The cards are inserted
// in an order that disagrees with that, so a migration that sorted by id or by
// age would show.
func TestMigration0003ConvertsStep2Data(t *testing.T) {
	raw := openRawDB(t)
	migrateUpTo(t, raw, 2)

	// Step 2 columns: 1 "Henüz yapılmadı", 2 "Yapılıyor", 3 "Tamamlandı".
	const metadata = `{"source":"step2","n":[1,2]}`
	for _, r := range []struct {
		title     string
		category  int64
		position  int64
		createdAt string
	}{
		{"D", 3, 0, "2026-01-01T00:00:01.000Z"},
		{"C2", 2, 1, "2026-01-01T00:00:02.000Z"},
		{"C", 2, 0, "2026-01-01T00:00:03.000Z"},
		{"B", 1, 1, "2026-01-01T00:00:04.000Z"},
		{"A", 1, 0, "2026-01-01T00:00:05.000Z"},
	} {
		_, err := raw.Exec(
			`INSERT INTO todos (title, category_id, position, metadata, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
			r.title, r.category, r.position, metadata, r.createdAt, r.createdAt)
		if err != nil {
			t.Fatalf("insert %q: %v", r.title, err)
		}
	}

	migrateUpTo(t, raw, 3)

	s := &Store{db: raw}
	categories, err := s.ListCategories(t.Context())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	var keys []string
	for _, c := range categories {
		keys = append(keys, c.Key)
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("category keys = %q, want %q", keys, wantKeys)
	}
	if !reflect.DeepEqual(categories, wantCategories) {
		t.Errorf("ListCategories = %+v, want %+v (the three old columns must be gone)", categories, wantCategories)
	}

	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if len(todos) != 5 {
		t.Fatalf("%d todos after the migration, want 5", len(todos))
	}
	wantStatus := map[string]string{"A": "todo", "B": "todo", "C": "in_progress", "C2": "in_progress", "D": "done"}
	for _, todo := range todos {
		if todo.Status != wantStatus[todo.Title] {
			t.Errorf("%s: status = %q, want %q (from its old column)", todo.Title, todo.Status, wantStatus[todo.Title])
		}
		if todo.CategoryID != catInbox {
			t.Errorf("%s: category_id = %d, want %d (the inbox)", todo.Title, todo.CategoryID, catInbox)
		}
		if todo.Classification != ClassificationPending {
			t.Errorf("%s: classification = %q, want %q", todo.Title, todo.Classification, ClassificationPending)
		}
		if todo.Description != "" {
			t.Errorf("%s: description = %q, want empty", todo.Title, todo.Description)
		}
		// Nothing but the place, the status and the classification is touched.
		if string(todo.Metadata) != metadata {
			t.Errorf("%s: metadata = %s, want %s", todo.Title, todo.Metadata, metadata)
		}
		if !todo.CreatedAt.Equal(todo.UpdatedAt) {
			t.Errorf("%s: updated_at %v differs from created_at %v, want both untouched", todo.Title, todo.UpdatedAt, todo.CreatedAt)
		}
	}
	assertOrder(t, s, catInbox, "A", "B", "C", "C2", "D")
	assertDensePositions(t, s)
	if got := listedTitles(t, s); !reflect.DeepEqual(got, []string{"A", "B", "C", "C2", "D"}) {
		t.Errorf("ListTodos = %q, want the inbox from top to bottom", got)
	}
}
