package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

// sameTodo reports whether a and b hold the same data; timestamps are compared
// with Equal rather than field by field.
func sameTodo(a, b Todo) bool {
	return a.ID == b.ID &&
		a.Title == b.Title &&
		a.CategoryID == b.CategoryID &&
		a.Position == b.Position &&
		string(a.Metadata) == string(b.Metadata) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt)
}

// showTodo formats a todo for failure messages; %+v would print the metadata as
// a list of byte values.
func showTodo(t Todo) string {
	return fmt.Sprintf("{id=%d title=%q category=%d position=%d metadata=%s created=%s updated=%s}",
		t.ID, t.Title, t.CategoryID, t.Position, t.Metadata,
		t.CreatedAt.Format(time.RFC3339Nano), t.UpdatedAt.Format(time.RFC3339Nano))
}

// order returns the titles of the todos of a column from top to bottom, by
// their positions. It reads the table itself and does not rely on the sort of
// ListTodos.
func order(t *testing.T, s *Store, categoryID int64) []string {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(),
		`SELECT title FROM todos WHERE category_id = ? ORDER BY position, id`, categoryID)
	if err != nil {
		t.Fatalf("order of category %d: %v", categoryID, err)
	}
	defer rows.Close()

	titles := []string{}
	for rows.Next() {
		var title string
		if err := rows.Scan(&title); err != nil {
			t.Fatalf("order of category %d: %v", categoryID, err)
		}
		titles = append(titles, title)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("order of category %d: %v", categoryID, err)
	}
	return titles
}

// assertOrder fails the test unless the column holds exactly the given titles,
// from top to bottom.
func assertOrder(t *testing.T, s *Store, categoryID int64, want ...string) {
	t.Helper()
	if got := order(t, s, categoryID); !slices.Equal(got, want) {
		t.Errorf("column %d = %q, want %q (top to bottom)", categoryID, got, want)
	}
}

// assertDensePositions checks the invariant of step 2 spec 4.2: in every
// column the positions of the todos are exactly 0..n-1, with no gap and no
// duplicate.
func assertDensePositions(t *testing.T, s *Store) {
	t.Helper()
	rows, err := s.db.QueryContext(t.Context(), `SELECT category_id, position FROM todos ORDER BY category_id, position`)
	if err != nil {
		t.Fatalf("read positions: %v", err)
	}
	defer rows.Close()

	positions := make(map[int64][]int64) // category id -> positions, ascending
	for rows.Next() {
		var categoryID, position int64
		if err := rows.Scan(&categoryID, &position); err != nil {
			t.Fatalf("read positions: %v", err)
		}
		positions[categoryID] = append(positions[categoryID], position)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read positions: %v", err)
	}
	for _, categoryID := range slices.Sorted(maps.Keys(positions)) {
		got := positions[categoryID]
		want := make([]int64, len(got))
		for i := range want {
			want[i] = int64(i)
		}
		if !slices.Equal(got, want) {
			t.Errorf("column %d: positions = %v, want %v (0..n-1)", categoryID, got, want)
		}
	}
}

// createInOrder creates one todo per title in the column, one after the other,
// and returns their ids by title. A new todo goes to the top, so the column ends
// up in the reverse order: the last title is on top.
func createInOrder(t *testing.T, s *Store, categoryID int64, titles ...string) map[string]int64 {
	t.Helper()
	ids := make(map[string]int64, len(titles))
	for _, title := range titles {
		todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title, CategoryID: ptr(categoryID)})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		ids[title] = todo.ID
	}
	return ids
}

// newBoard returns a store whose column 1 holds A, B, C and whose column 2 holds
// D, E, each from top to bottom, and the ids of the todos by title.
func newBoard(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	s := newTestStore(t)
	ids := createInOrder(t, s, 1, "C", "B", "A")
	maps.Copy(ids, createInOrder(t, s, 2, "E", "D"))
	assertOrder(t, s, 1, "A", "B", "C")
	assertOrder(t, s, 2, "D", "E")
	return s, ids
}

// metadataStorageClass returns SQLite's storage class of the stored metadata.
// Binding a json.RawMessage or []byte directly would store a blob, not text.
func metadataStorageClass(t *testing.T, s *Store, id int64) string {
	t.Helper()
	var class string
	if err := s.db.QueryRow(`SELECT typeof(metadata) FROM todos WHERE id = ?`, id).Scan(&class); err != nil {
		t.Fatalf("typeof(metadata): %v", err)
	}
	return class
}

func TestCreateTodoDefaultsToFirstCategory(t *testing.T) {
	s := newTestStore(t)

	todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if todo.ID <= 0 {
		t.Errorf("ID = %d, want > 0", todo.ID)
	}
	if todo.Title != "Süt al" {
		t.Errorf("Title = %q, want %q", todo.Title, "Süt al")
	}
	if todo.CategoryID != 1 {
		t.Errorf("CategoryID = %d, want 1 (the first column)", todo.CategoryID)
	}
	if string(todo.Metadata) != "{}" {
		t.Errorf("Metadata = %s, want {}", todo.Metadata)
	}
	if todo.CreatedAt.IsZero() {
		t.Error("CreatedAt is zero")
	}
	if todo.CreatedAt.Location() != time.UTC {
		t.Errorf("CreatedAt location = %v, want UTC", todo.CreatedAt.Location())
	}
	if todo.UpdatedAt.Location() != time.UTC {
		t.Errorf("UpdatedAt location = %v, want UTC", todo.UpdatedAt.Location())
	}
	if !todo.UpdatedAt.Equal(todo.CreatedAt) {
		t.Errorf("UpdatedAt = %v, want it equal to CreatedAt %v on insert", todo.UpdatedAt, todo.CreatedAt)
	}
	// The timestamp is SQL "now" in UTC; a time zone mix-up would show as hours of skew.
	if d := time.Since(todo.CreatedAt); d < -time.Minute || d > time.Minute {
		t.Errorf("CreatedAt = %v is %v away from now", todo.CreatedAt, d)
	}
}

func TestCreateTodoWithCategoryAndMetadata(t *testing.T) {
	s := newTestStore(t)
	const metadata = `{"source":"test","n":1}`

	todo, err := s.CreateTodo(t.Context(), CreateTodoParams{
		Title:      "Fatura öde",
		CategoryID: ptr(int64(3)),
		Metadata:   json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if todo.CategoryID != 3 {
		t.Errorf("CategoryID = %d, want 3", todo.CategoryID)
	}
	var got map[string]any
	if err := json.Unmarshal(todo.Metadata, &got); err != nil {
		t.Fatalf("Metadata %s is not valid JSON: %v", todo.Metadata, err)
	}
	if want := map[string]any{"source": "test", "n": float64(1)}; !reflect.DeepEqual(got, want) {
		t.Errorf("Metadata = %v, want %v", got, want)
	}
	if string(todo.Metadata) != metadata {
		t.Errorf("Metadata = %s, want it returned unchanged as %s", todo.Metadata, metadata)
	}
	if class := metadataStorageClass(t, s, todo.ID); class != "text" {
		t.Errorf("metadata is stored as %s, want text", class)
	}

	// What was returned is what was stored.
	stored, err := s.GetTodo(t.Context(), todo.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if !sameTodo(stored, todo) {
		t.Errorf("GetTodo = %s, want %s", showTodo(stored), showTodo(todo))
	}
}

func TestCreateTodoInvalidCategory(t *testing.T) {
	s := newTestStore(t)

	_, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "x", CategoryID: ptr(int64(99))})

	if !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("err = %v, want ErrInvalidCategory", err)
	}
	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if len(todos) != 0 {
		t.Errorf("%d todos stored after a failed create, want 0", len(todos))
	}
}

func TestTurkishTextRoundTrip(t *testing.T) {
	s := newTestStore(t)
	const title = "Süt al — çğıöşü İĞÜŞÖÇ"
	const metadata = `{"not":"çğıöşü İĞÜŞÖÇ"}`

	created, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title, Metadata: json.RawMessage(metadata)})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	got, err := s.GetTodo(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}

	if got.Title != title {
		t.Errorf("Title = %q, want %q", got.Title, title)
	}
	if string(got.Metadata) != metadata {
		t.Errorf("Metadata = %s, want %s", got.Metadata, metadata)
	}
}

func TestGetTodoNotFound(t *testing.T) {
	s := newTestStore(t)

	_, err := s.GetTodo(t.Context(), 999)

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestListTodosEmpty(t *testing.T) {
	s := newTestStore(t)

	todos, err := s.ListTodos(t.Context())

	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if todos == nil {
		t.Error("ListTodos returned a nil slice, want an empty one (it must encode as [] not null)")
	}
	if len(todos) != 0 {
		t.Errorf("len = %d, want 0", len(todos))
	}
}

func TestListTodosNewestFirst(t *testing.T) {
	s := newTestStore(t)
	var ids []int64
	for _, title := range []string{"A", "B", "C"} {
		todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		ids = append(ids, todo.ID)
	}

	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}

	var got []int64
	for _, todo := range todos {
		got = append(got, todo.ID)
	}
	if want := []int64{ids[2], ids[1], ids[0]}; !slices.Equal(got, want) {
		t.Errorf("ids = %v, want %v (newest first)", got, want)
	}
}

// listedTitles returns the titles ListTodos returns, in its order.
func listedTitles(t *testing.T, s *Store) []string {
	t.Helper()
	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	titles := []string{}
	for _, todo := range todos {
		titles = append(titles, todo.Title)
	}
	return titles
}

// ListTodos sorts by category_id, position, id (step 2 spec 4.3): the columns
// one after the other, each from top to bottom.
func TestListTodosOrdersByCategoryThenPosition(t *testing.T) {
	t.Run("created through the store", func(t *testing.T) {
		s := newTestStore(t)
		createInOrder(t, s, 3, "X")
		createInOrder(t, s, 1, "A", "B")

		got := listedTitles(t, s)

		if want := []string{"B", "A", "X"}; !slices.Equal(got, want) {
			t.Errorf("ListTodos = %q, want %q", got, want)
		}
	})

	// Created through the store, the newest todo is always on top of its column,
	// so a sort by age (the order of step 1) or by id, newest first, gives the
	// same list. These rows are inserted directly, with ids, categories and
	// positions running against each other.
	t.Run("position decides, not id or age", func(t *testing.T) {
		s := newTestStore(t)
		for _, r := range []struct {
			title    string
			category int64
			position int64
		}{
			{"column 2, position 1", 2, 1},
			{"column 2, position 0", 2, 0},
			{"column 1, position 0", 1, 0},
			{"column 3, position 0", 3, 0},
		} {
			_, err := s.db.Exec(`INSERT INTO todos (title, category_id, position) VALUES (?, ?, ?)`,
				r.title, r.category, r.position)
			if err != nil {
				t.Fatalf("insert %q: %v", r.title, err)
			}
		}

		got := listedTitles(t, s)

		want := []string{"column 1, position 0", "column 2, position 0", "column 2, position 1", "column 3, position 0"}
		if !slices.Equal(got, want) {
			t.Errorf("ListTodos = %q, want %q", got, want)
		}
	})
}

func TestCreateTodoGoesToTop(t *testing.T) {
	s := newTestStore(t)

	for _, title := range []string{"A", "B", "C"} {
		todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title, CategoryID: ptr(int64(1))})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		if todo.Position != 0 {
			t.Errorf("%s: Position = %d, want 0 (a new todo goes to the top)", title, todo.Position)
		}
		assertDensePositions(t, s)
	}
	assertOrder(t, s, 1, "C", "B", "A")

	// Another column is renumbered on its own.
	d, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "D", CategoryID: ptr(int64(3))})
	if err != nil {
		t.Fatalf("CreateTodo(D): %v", err)
	}
	if d.Position != 0 {
		t.Errorf("D: Position = %d, want 0 (the top of an empty column)", d.Position)
	}
	assertOrder(t, s, 3, "D")
	assertOrder(t, s, 1, "C", "B", "A")
	assertDensePositions(t, s)

	// Without a category the todo goes to the top of the first column.
	e, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "E"})
	if err != nil {
		t.Fatalf("CreateTodo(E): %v", err)
	}
	if e.Position != 0 || e.CategoryID != 1 {
		t.Errorf("E: position %d in category %d, want position 0 in category 1", e.Position, e.CategoryID)
	}
	assertOrder(t, s, 1, "E", "C", "B", "A")
	assertDensePositions(t, s)
}

func TestDeleteTodoClosesGap(t *testing.T) {
	tests := []struct {
		name, title  string
		want1, want2 []string // the columns afterwards, from top to bottom
	}{
		{"middle of column 1", "B", []string{"C", "A"}, []string{"Z", "Y", "X"}},
		{"top of column 1", "C", []string{"B", "A"}, []string{"Z", "Y", "X"}},
		{"bottom of column 1", "A", []string{"C", "B"}, []string{"Z", "Y", "X"}},
		{"middle of column 2", "Y", []string{"C", "B", "A"}, []string{"Z", "X"}},
		{"top of column 2", "Z", []string{"C", "B", "A"}, []string{"Y", "X"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			// Both columns are as full as each other, so that a shift that took
			// the wrong column, or none, shows up in the other one.
			ids := createInOrder(t, s, 1, "A", "B", "C")
			maps.Copy(ids, createInOrder(t, s, 2, "X", "Y", "Z"))
			assertOrder(t, s, 1, "C", "B", "A")
			assertOrder(t, s, 2, "Z", "Y", "X")

			if err := s.DeleteTodo(t.Context(), ids[tc.title]); err != nil {
				t.Fatalf("DeleteTodo(%s): %v", tc.title, err)
			}

			assertOrder(t, s, 1, tc.want1...)
			assertOrder(t, s, 2, tc.want2...)
			assertDensePositions(t, s)
		})
	}
}

func TestConstraints(t *testing.T) {
	s := newTestStore(t)

	// Each statement must fail because of the constraint itself, not because of
	// a typo in the SQL, so the error text is checked as well.
	for _, metadata := range []string{`[]`, `"text"`, `1`, `null`, `not json`} {
		_, err := s.db.Exec(`INSERT INTO todos (title, category_id, metadata) VALUES ('x', 1, ?)`, metadata)
		if err == nil {
			t.Errorf("metadata %s was accepted, want it rejected (only JSON objects are valid)", metadata)
		} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Errorf("metadata %s: err = %v, want a CHECK constraint failure", metadata, err)
		}
	}

	if _, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "kategoriyi tutar"}); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	_, err := s.db.Exec(`DELETE FROM categories WHERE id = 1`)
	if err == nil {
		t.Error("deleted a category that still has todos, want ON DELETE RESTRICT to refuse")
	} else if !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Errorf("err = %v, want a FOREIGN KEY constraint failure", err)
	}
}

func TestConcurrentCreates(t *testing.T) {
	s := newTestStore(t)
	const n = 20

	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			<-start // release everyone at once to maximize write contention
			if _, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: fmt.Sprintf("t%d", i)}); err != nil {
				t.Errorf("CreateTodo t%d: %v", i, err)
			}
		})
	}
	close(start)
	wg.Wait()

	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}
	if len(todos) != n {
		t.Fatalf("len(todos) = %d, want %d", len(todos), n)
	}
	seen := make(map[string]bool)
	for _, todo := range todos {
		seen[todo.Title] = true
	}
	if len(seen) != n {
		t.Errorf("%d distinct titles stored, want %d", len(seen), n)
	}
	// Creates that overlap must not leave two todos on the same position.
	assertDensePositions(t, s)
}

func TestUpdateTodoFields(t *testing.T) {
	tests := []struct {
		name   string
		params UpdateTodoParams
		change func(want *Todo) // applies the expected change to a copy of the original
	}{
		{
			name:   "title",
			params: UpdateTodoParams{Title: ptr("Yeni başlık")},
			change: func(w *Todo) { w.Title = "Yeni başlık" },
		},
		{
			name:   "category",
			params: UpdateTodoParams{CategoryID: ptr(int64(3))},
			change: func(w *Todo) { w.CategoryID = 3 },
		},
		{
			// The new object replaces the old one; keys are not merged.
			name:   "metadata",
			params: UpdateTodoParams{Metadata: json.RawMessage(`{"b":2}`)},
			change: func(w *Todo) { w.Metadata = json.RawMessage(`{"b":2}`) },
		},
		{
			name: "all fields",
			params: UpdateTodoParams{
				Title:      ptr("Hepsi"),
				CategoryID: ptr(int64(2)),
				Metadata:   json.RawMessage(`{"c":3}`),
			},
			change: func(w *Todo) {
				w.Title = "Hepsi"
				w.CategoryID = 2
				w.Metadata = json.RawMessage(`{"c":3}`)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			orig, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Eski", Metadata: json.RawMessage(`{"a":1}`)})
			if err != nil {
				t.Fatalf("CreateTodo: %v", err)
			}
			time.Sleep(20 * time.Millisecond) // timestamps have millisecond resolution

			got, err := s.UpdateTodo(t.Context(), orig.ID, tc.params)
			if err != nil {
				t.Fatalf("UpdateTodo: %v", err)
			}

			if !got.UpdatedAt.After(orig.UpdatedAt) {
				t.Errorf("UpdatedAt = %v, want it after %v", got.UpdatedAt, orig.UpdatedAt)
			}
			// Everything else: the changed field has its new value, all other
			// fields (and CreatedAt) are exactly what they were.
			want := orig
			tc.change(&want)
			want.UpdatedAt = got.UpdatedAt
			if !sameTodo(got, want) {
				t.Errorf("UpdateTodo = %s, want %s", showTodo(got), showTodo(want))
			}
			if class := metadataStorageClass(t, s, orig.ID); class != "text" {
				t.Errorf("metadata is stored as %s, want text", class)
			}

			stored, err := s.GetTodo(t.Context(), orig.ID)
			if err != nil {
				t.Fatalf("GetTodo: %v", err)
			}
			if !sameTodo(stored, got) {
				t.Errorf("GetTodo = %s, want what UpdateTodo returned: %s", showTodo(stored), showTodo(got))
			}
		})
	}
}

func TestUpdateTodoNotFound(t *testing.T) {
	s := newTestStore(t)

	// The category is invalid as well; the missing todo has to be reported first.
	_, err := s.UpdateTodo(t.Context(), 999, UpdateTodoParams{CategoryID: ptr(int64(99))})

	if !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestUpdateTodoInvalidCategory(t *testing.T) {
	s := newTestStore(t)
	orig, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Eski"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	// The title is valid, but the update must be all or nothing.
	_, err = s.UpdateTodo(t.Context(), orig.ID, UpdateTodoParams{Title: ptr("Değişmemeli"), CategoryID: ptr(int64(99))})

	if !errors.Is(err, ErrInvalidCategory) {
		t.Errorf("err = %v, want ErrInvalidCategory", err)
	}
	stored, err := s.GetTodo(t.Context(), orig.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if !sameTodo(stored, orig) {
		t.Errorf("todo changed by a rejected update: got %s, want %s", showTodo(stored), showTodo(orig))
	}
}

// Step 2 spec 4.3. Every test starts from the same board: column 1 holds
// A, B, C, column 2 holds D, E and column 3 is empty, each from top to bottom.
func TestUpdateTodoPosition(t *testing.T) {
	type columns = map[int64][]string // category id -> titles, top to bottom; a missing column is empty
	tests := []struct {
		name   string
		move   string // title of the todo to update
		params UpdateTodoParams
		want   columns // the board afterwards
	}{
		// The position counts in the column without the todo itself.
		{"down within column", "A", UpdateTodoParams{Position: ptr(int64(2))},
			columns{1: {"B", "C", "A"}, 2: {"D", "E"}}},
		{"up within column", "C", UpdateTodoParams{Position: ptr(int64(0))},
			columns{1: {"C", "A", "B"}, 2: {"D", "E"}}},
		{"to the middle", "A", UpdateTodoParams{Position: ptr(int64(1))},
			columns{1: {"B", "A", "C"}, 2: {"D", "E"}}},
		{"across columns at index", "B", UpdateTodoParams{CategoryID: ptr(int64(2)), Position: ptr(int64(1))},
			columns{1: {"A", "C"}, 2: {"D", "B", "E"}}},
		{"across past the end", "A", UpdateTodoParams{CategoryID: ptr(int64(2)), Position: ptr(int64(99))},
			columns{1: {"B", "C"}, 2: {"D", "E", "A"}}},
		{"within past the end", "A", UpdateTodoParams{Position: ptr(int64(99))},
			columns{1: {"B", "C", "A"}, 2: {"D", "E"}}},
		{"category only goes to top", "C", UpdateTodoParams{CategoryID: ptr(int64(2))},
			columns{1: {"A", "B"}, 2: {"C", "D", "E"}}},
		{"same category keeps place", "B", UpdateTodoParams{CategoryID: ptr(int64(1))},
			columns{1: {"A", "B", "C"}, 2: {"D", "E"}}},
		{"title only keeps place", "B", UpdateTodoParams{Title: ptr("B2")},
			columns{1: {"A", "B2", "C"}, 2: {"D", "E"}}},

		// Beyond the cases of the brief.
		{"same position keeps place", "B", UpdateTodoParams{Position: ptr(int64(1))},
			columns{1: {"A", "B", "C"}, 2: {"D", "E"}}},
		// Another column has room for one more place than the column the todo
		// leaves: the end is position 2 there, and 2 must not be cut back to 1.
		{"across to the end exactly", "A", UpdateTodoParams{CategoryID: ptr(int64(2)), Position: ptr(int64(2))},
			columns{1: {"B", "C"}, 2: {"D", "E", "A"}}},
		{"into an empty column", "A", UpdateTodoParams{CategoryID: ptr(int64(3)), Position: ptr(int64(0))},
			columns{1: {"B", "C"}, 2: {"D", "E"}, 3: {"A"}}},
		{"into an empty column past the end", "B", UpdateTodoParams{CategoryID: ptr(int64(3)), Position: ptr(int64(5))},
			columns{1: {"A", "C"}, 2: {"D", "E"}, 3: {"B"}}},
		{"within the largest position", "A", UpdateTodoParams{Position: ptr(int64(math.MaxInt64))},
			columns{1: {"B", "C", "A"}, 2: {"D", "E"}}},
		{"title and position", "B", UpdateTodoParams{Title: ptr("B2"), Position: ptr(int64(0))},
			columns{1: {"B2", "A", "C"}, 2: {"D", "E"}}},
		// Every move above starts in column 1; these start in column 2.
		{"up within the second column", "E", UpdateTodoParams{Position: ptr(int64(0))},
			columns{1: {"A", "B", "C"}, 2: {"E", "D"}}},
		{"from the second column to the first", "D", UpdateTodoParams{CategoryID: ptr(int64(1)), Position: ptr(int64(1))},
			columns{1: {"A", "D", "B", "C"}, 2: {"E"}}},
		{"from the second column to the third", "D", UpdateTodoParams{CategoryID: ptr(int64(3))},
			columns{1: {"A", "B", "C"}, 2: {"E"}, 3: {"D"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, ids := newBoard(t)
			before, err := s.GetTodo(t.Context(), ids[tc.move])
			if err != nil {
				t.Fatalf("GetTodo: %v", err)
			}
			time.Sleep(20 * time.Millisecond) // timestamps have millisecond resolution

			got, err := s.UpdateTodo(t.Context(), ids[tc.move], tc.params)
			if err != nil {
				t.Fatalf("UpdateTodo: %v", err)
			}

			for categoryID := int64(1); categoryID <= 3; categoryID++ {
				assertOrder(t, s, categoryID, tc.want[categoryID]...)
			}
			assertDensePositions(t, s)

			// The returned todo knows where it ended up: its column, and its
			// position is its index in that column.
			wantCategory, wantPosition := int64(-1), int64(-1)
			for categoryID, titles := range tc.want {
				if i := slices.Index(titles, got.Title); i >= 0 {
					wantCategory, wantPosition = categoryID, int64(i)
				}
			}
			if got.CategoryID != wantCategory || got.Position != wantPosition {
				t.Errorf("UpdateTodo returned category %d, position %d; want category %d, position %d",
					got.CategoryID, got.Position, wantCategory, wantPosition)
			}
			if !got.UpdatedAt.After(before.UpdatedAt) {
				t.Errorf("UpdatedAt = %v, want it after %v", got.UpdatedAt, before.UpdatedAt)
			}
			if got.ID != before.ID || !got.CreatedAt.Equal(before.CreatedAt) {
				t.Errorf("UpdateTodo = %s, want the same id and created_at as %s", showTodo(got), showTodo(before))
			}
			stored, err := s.GetTodo(t.Context(), got.ID)
			if err != nil {
				t.Fatalf("GetTodo: %v", err)
			}
			if !sameTodo(stored, got) {
				t.Errorf("GetTodo = %s, want what UpdateTodo returned: %s", showTodo(stored), showTodo(got))
			}
		})
	}
}

// All four fields at once: the single UPDATE statement binds them by position,
// so a mix-up between neighbouring parameters shows here.
func TestUpdateTodoAllFieldsWithPosition(t *testing.T) {
	s, ids := newBoard(t)

	got, err := s.UpdateTodo(t.Context(), ids["B"], UpdateTodoParams{
		Title:      ptr("B2"),
		CategoryID: ptr(int64(2)),
		Position:   ptr(int64(1)),
		Metadata:   json.RawMessage(`{"k":1}`),
	})
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	if got.Title != "B2" || got.CategoryID != 2 || got.Position != 1 || string(got.Metadata) != `{"k":1}` {
		t.Errorf("UpdateTodo = %s, want title B2, category 2, position 1, metadata {\"k\":1}", showTodo(got))
	}
	assertOrder(t, s, 1, "A", "C")
	assertOrder(t, s, 2, "D", "B2", "E")
	assertDensePositions(t, s)
}

// The errors are those of step 1, and a rejected update moves nothing.
func TestUpdateTodoPositionErrors(t *testing.T) {
	t.Run("missing todo is reported before the category", func(t *testing.T) {
		s, _ := newBoard(t)

		_, err := s.UpdateTodo(t.Context(), 999, UpdateTodoParams{CategoryID: ptr(int64(99)), Position: ptr(int64(0))})

		if !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
		assertOrder(t, s, 1, "A", "B", "C")
		assertOrder(t, s, 2, "D", "E")
		assertDensePositions(t, s)
	})

	t.Run("invalid category changes nothing", func(t *testing.T) {
		s, ids := newBoard(t)

		_, err := s.UpdateTodo(t.Context(), ids["B"], UpdateTodoParams{
			Title: ptr("B2"), CategoryID: ptr(int64(99)), Position: ptr(int64(0)),
		})

		if !errors.Is(err, ErrInvalidCategory) {
			t.Errorf("err = %v, want ErrInvalidCategory", err)
		}
		assertOrder(t, s, 1, "A", "B", "C") // B has not been renamed or moved
		assertOrder(t, s, 2, "D", "E")
		assertDensePositions(t, s)
	})
}

func TestDeleteTodo(t *testing.T) {
	s := newTestStore(t)
	keep, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Kalsın"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	gone, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Silinecek"})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if err := s.DeleteTodo(t.Context(), gone.ID); err != nil {
		t.Fatalf("DeleteTodo: %v", err)
	}

	if _, err := s.GetTodo(t.Context(), gone.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetTodo after delete: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTodo(t.Context(), gone.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second DeleteTodo: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteTodo(t.Context(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("DeleteTodo(999): err = %v, want ErrNotFound", err)
	}
	if _, err := s.GetTodo(t.Context(), keep.ID); err != nil {
		t.Errorf("the other todo was affected by the delete: %v", err)
	}
}
