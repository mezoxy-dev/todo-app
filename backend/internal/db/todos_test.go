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

// The position tests of step 2 move todos between three columns. They use these
// three categories of the step 3 model.
const (
	col1 = catPurchase
	col2 = catResearch
	col3 = catCommunication
)

// sameTodo reports whether a and b hold the same data; timestamps are compared
// with Equal rather than field by field.
func sameTodo(a, b Todo) bool {
	return a.ID == b.ID &&
		a.Title == b.Title &&
		a.Description == b.Description &&
		a.CategoryID == b.CategoryID &&
		a.Position == b.Position &&
		a.Status == b.Status &&
		a.Classification == b.Classification &&
		string(a.Metadata) == string(b.Metadata) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt)
}

// showTodo formats a todo for failure messages; %+v would print the metadata as
// a list of byte values.
func showTodo(t Todo) string {
	return fmt.Sprintf("{id=%d title=%q description=%q category=%d position=%d status=%s classification=%s metadata=%s created=%s updated=%s}",
		t.ID, t.Title, t.Description, t.CategoryID, t.Position, t.Status, t.Classification, t.Metadata,
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
// duplicate. Step 3 keeps it for the inbox as well.
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
// up in the reverse order: the last title is on top. A todo is always created in
// the inbox; one for another column is moved to the top of it right away, which
// leaves the same order as creating it there. (Such a todo is a manual one.)
func createInOrder(t *testing.T, s *Store, categoryID int64, titles ...string) map[string]int64 {
	t.Helper()
	ids := make(map[string]int64, len(titles))
	for _, title := range titles {
		todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		if categoryID != catInbox {
			if _, err := s.UpdateTodo(t.Context(), todo.ID, UpdateTodoParams{CategoryID: &categoryID}); err != nil {
				t.Fatalf("UpdateTodo(%s) into category %d: %v", title, categoryID, err)
			}
		}
		ids[title] = todo.ID
	}
	return ids
}

// newBoard returns a store whose column col1 holds A, B, C and whose column col2
// holds D, E, each from top to bottom, and the ids of the todos by title.
func newBoard(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	s := newTestStore(t)
	ids := createInOrder(t, s, col1, "C", "B", "A")
	maps.Copy(ids, createInOrder(t, s, col2, "E", "D"))
	assertOrder(t, s, col1, "A", "B", "C")
	assertOrder(t, s, col2, "D", "E")
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

// Step 3 spec 7: a new todo goes to the top of the inbox, waiting for the AI.
func TestCreateTodoGoesToInboxPending(t *testing.T) {
	s := newTestStore(t)

	todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al", Description: "2 litre", Status: StatusDone})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}

	if todo.ID <= 0 {
		t.Errorf("ID = %d, want > 0", todo.ID)
	}
	if todo.Title != "Süt al" || todo.Description != "2 litre" {
		t.Errorf("title %q, description %q; want %q and %q", todo.Title, todo.Description, "Süt al", "2 litre")
	}
	if todo.CategoryID != catInbox || todo.Position != 0 {
		t.Errorf("category %d, position %d; want the top of the inbox (category %d, position 0)",
			todo.CategoryID, todo.Position, catInbox)
	}
	if todo.Classification != ClassificationPending {
		t.Errorf("Classification = %q, want %q", todo.Classification, ClassificationPending)
	}
	if todo.Status != StatusDone {
		t.Errorf("Status = %q, want %q", todo.Status, StatusDone)
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

	// Without a status the todo is still to do, and the first one moves down.
	plain, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Ekmek al"})
	if err != nil {
		t.Fatalf("CreateTodo without a status: %v", err)
	}
	if plain.Status != StatusTodo || plain.Description != "" {
		t.Errorf("status %q, description %q; want %q and an empty description", plain.Status, plain.Description, StatusTodo)
	}
	if plain.CategoryID != catInbox || plain.Position != 0 || plain.Classification != ClassificationPending {
		t.Errorf("second todo: category %d, position %d, classification %q; want the top of the inbox, pending",
			plain.CategoryID, plain.Position, plain.Classification)
	}
	assertOrder(t, s, catInbox, "Ekmek al", "Süt al")
	assertDensePositions(t, s)
}

func TestCreateTodoWithMetadata(t *testing.T) {
	s := newTestStore(t)
	const metadata = `{"source":"test","n":1}`

	todo, err := s.CreateTodo(t.Context(), CreateTodoParams{
		Title:    "Fatura öde",
		Metadata: json.RawMessage(metadata),
	})
	if err != nil {
		t.Fatalf("CreateTodo: %v", err)
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

func TestTurkishTextRoundTrip(t *testing.T) {
	s := newTestStore(t)
	const title = "Süt al — çğıöşü İĞÜŞÖÇ"
	const description = "Yağsız, 2 litre — şişe değil"
	const metadata = `{"not":"çğıöşü İĞÜŞÖÇ"}`

	created, err := s.CreateTodo(t.Context(), CreateTodoParams{
		Title: title, Description: description, Metadata: json.RawMessage(metadata),
	})
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
	if got.Description != description {
		t.Errorf("Description = %q, want %q", got.Description, description)
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
		createInOrder(t, s, col3, "X")
		createInOrder(t, s, col1, "A", "B")

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
			{"column 2, position 1", col2, 1},
			{"column 2, position 0", col2, 0},
			{"column 1, position 0", col1, 0},
			{"column 3, position 0", col3, 0},
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
		todo, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: title})
		if err != nil {
			t.Fatalf("CreateTodo(%s): %v", title, err)
		}
		if todo.CategoryID != catInbox || todo.Position != 0 {
			t.Errorf("%s: category %d, position %d; want position 0 (a new todo goes to the top) of the inbox",
				title, todo.CategoryID, todo.Position)
		}
		assertDensePositions(t, s)
	}
	assertOrder(t, s, catInbox, "C", "B", "A")

	// A todo that has been moved to another column keeps its place there; only
	// the inbox is renumbered when a new todo arrives.
	createInOrder(t, s, col3, "D")
	assertOrder(t, s, catInbox, "C", "B", "A")
	assertOrder(t, s, col3, "D")

	e, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "E"})
	if err != nil {
		t.Fatalf("CreateTodo(E): %v", err)
	}
	if e.Position != 0 || e.CategoryID != catInbox {
		t.Errorf("E: position %d in category %d, want position 0 in category %d", e.Position, e.CategoryID, catInbox)
	}
	assertOrder(t, s, catInbox, "E", "C", "B", "A")
	assertOrder(t, s, col3, "D")
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
			ids := createInOrder(t, s, col1, "A", "B", "C")
			maps.Copy(ids, createInOrder(t, s, col2, "X", "Y", "Z"))
			assertOrder(t, s, col1, "C", "B", "A")
			assertOrder(t, s, col2, "Z", "Y", "X")

			if err := s.DeleteTodo(t.Context(), ids[tc.title]); err != nil {
				t.Fatalf("DeleteTodo(%s): %v", tc.title, err)
			}

			assertOrder(t, s, col1, tc.want1...)
			assertOrder(t, s, col2, tc.want2...)
			assertDensePositions(t, s)
		})
	}
}

func TestConstraints(t *testing.T) {
	s := newTestStore(t)

	// Each statement must fail because of the constraint itself, not because of
	// a typo in the SQL, so the error text is checked as well.
	for _, metadata := range []string{`[]`, `"text"`, `1`, `null`, `not json`} {
		_, err := s.db.Exec(`INSERT INTO todos (title, category_id, metadata) VALUES ('x', ?, ?)`, catInbox, metadata)
		if err == nil {
			t.Errorf("metadata %s was accepted, want it rejected (only JSON objects are valid)", metadata)
		} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Errorf("metadata %s: err = %v, want a CHECK constraint failure", metadata, err)
		}
	}

	// status and classification take nothing but their listed values.
	for _, c := range []struct{ column, value string }{
		{"status", "bitti"}, {"status", ""}, {"status", "Done"},
		{"classification", "x"}, {"classification", ""}, {"classification", "Pending"},
	} {
		_, err := s.db.Exec(`INSERT INTO todos (title, category_id, `+c.column+`) VALUES ('x', ?, ?)`, catInbox, c.value)
		if err == nil {
			t.Errorf("%s %q was accepted, want it rejected", c.column, c.value)
		} else if !strings.Contains(err.Error(), "CHECK constraint failed") {
			t.Errorf("%s %q: err = %v, want a CHECK constraint failure", c.column, c.value, err)
		}
	}

	// A category key names one category only.
	_, err := s.db.Exec(`INSERT INTO categories (key, name, position) VALUES ('inbox', 'Başka bir ad', 99)`)
	if err == nil {
		t.Error("a second category with the key inbox was accepted, want the unique index to refuse")
	} else if !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Errorf("duplicate key: err = %v, want a UNIQUE constraint failure", err)
	}

	if _, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "kategoriyi tutar"}); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	_, err = s.db.Exec(`DELETE FROM categories WHERE id = ?`, catInbox)
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
			name:   "description",
			params: UpdateTodoParams{Description: ptr("Yeni açıklama")},
			change: func(w *Todo) { w.Description = "Yeni açıklama" },
		},
		{
			name:   "status",
			params: UpdateTodoParams{Status: ptr(StatusInProgress)},
			change: func(w *Todo) { w.Status = StatusInProgress },
		},
		{
			// A move by hand: the classification becomes "manual".
			name:   "category",
			params: UpdateTodoParams{CategoryID: ptr(catResearch)},
			change: func(w *Todo) {
				w.CategoryID = catResearch
				w.Classification = ClassificationManual
			},
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
				Title:       ptr("Hepsi"),
				Description: ptr("Açıklama"),
				Status:      ptr(StatusDone),
				CategoryID:  ptr(catPayment),
				Metadata:    json.RawMessage(`{"c":3}`),
			},
			change: func(w *Todo) {
				w.Title = "Hepsi"
				w.Description = "Açıklama"
				w.Status = StatusDone
				w.CategoryID = catPayment
				w.Classification = ClassificationManual
				w.Metadata = json.RawMessage(`{"c":3}`)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			orig, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Eski", Description: "eski açıklama", Metadata: json.RawMessage(`{"a":1}`)})
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

// Step 3 spec 5: only a different category_id makes a todo "manual". Status,
// description and a move within the column leave the classification alone, and
// the description can be cleared with an empty string.
func TestUpdateTodoStatusDescriptionAndManual(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "A", Description: "eski"})
	if err != nil {
		t.Fatalf("CreateTodo(A): %v", err)
	}
	b, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "B"}) // on top of A
	if err != nil {
		t.Fatalf("CreateTodo(B): %v", err)
	}
	update := func(name string, p UpdateTodoParams) Todo {
		t.Helper()
		got, err := s.UpdateTodo(t.Context(), a.ID, p)
		if err != nil {
			t.Fatalf("%s: UpdateTodo: %v", name, err)
		}
		return got
	}
	wantClassification := func(name string, got Todo, want string) {
		t.Helper()
		if got.Classification != want {
			t.Errorf("%s: classification = %q, want %q", name, got.Classification, want)
		}
	}

	// Status and description change; the category stays, and so does the classification.
	got := update("status and description", UpdateTodoParams{Status: ptr(StatusInProgress), Description: ptr("yeni")})
	if got.Status != StatusInProgress || got.Description != "yeni" {
		t.Errorf("status %q, description %q; want %q and %q", got.Status, got.Description, StatusInProgress, "yeni")
	}
	if got.CategoryID != catInbox || got.Position != 1 {
		t.Errorf("category %d, position %d; want A where it was: below B in the inbox", got.CategoryID, got.Position)
	}
	wantClassification("status and description", got, ClassificationPending)

	got = update("clear the description", UpdateTodoParams{Description: ptr("")})
	if got.Description != "" || got.Status != StatusInProgress {
		t.Errorf("description %q, status %q; want it cleared and the status kept as %q", got.Description, got.Status, StatusInProgress)
	}

	// The category it is in already is no move: alone, with a position, or as a position only.
	got = update("same category", UpdateTodoParams{CategoryID: ptr(catInbox)})
	wantClassification("same category", got, ClassificationPending)
	got = update("same category and position", UpdateTodoParams{CategoryID: ptr(catInbox), Position: ptr(int64(0))})
	wantClassification("same category and position", got, ClassificationPending)
	if got.Position != 0 {
		t.Errorf("same category and position: position = %d, want 0", got.Position)
	}
	assertOrder(t, s, catInbox, "A", "B")
	got = update("position only", UpdateTodoParams{Position: ptr(int64(1))})
	wantClassification("position only", got, ClassificationPending)
	assertOrder(t, s, catInbox, "B", "A")

	// A different category is a move by hand: "manual", and it stays so.
	got = update("another category", UpdateTodoParams{CategoryID: ptr(catResearch)})
	if got.CategoryID != catResearch || got.Position != 0 {
		t.Errorf("another category: category %d, position %d; want the top of category %d", got.CategoryID, got.Position, catResearch)
	}
	wantClassification("another category", got, ClassificationManual)
	got = update("title and status afterwards", UpdateTodoParams{Title: ptr("A2"), Status: ptr(StatusDone)})
	wantClassification("title and status afterwards", got, ClassificationManual)

	// Back to the inbox is a move by hand as well.
	got = update("back to the inbox", UpdateTodoParams{CategoryID: ptr(catInbox)})
	wantClassification("back to the inbox", got, ClassificationManual)

	// The other todo was not touched by any of this.
	stored, err := s.GetTodo(t.Context(), b.ID)
	if err != nil {
		t.Fatalf("GetTodo(B): %v", err)
	}
	if stored.Classification != ClassificationPending || stored.Status != StatusTodo || stored.Title != "B" {
		t.Errorf("B = %s, want it untouched (pending, to do)", showTodo(stored))
	}
	assertDensePositions(t, s)
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

// Step 2 spec 4.3. Every test starts from the same board: column col1 holds
// A, B, C, column col2 holds D, E and column col3 is empty, each from top to bottom.
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
			columns{col1: {"B", "C", "A"}, col2: {"D", "E"}}},
		{"up within column", "C", UpdateTodoParams{Position: ptr(int64(0))},
			columns{col1: {"C", "A", "B"}, col2: {"D", "E"}}},
		{"to the middle", "A", UpdateTodoParams{Position: ptr(int64(1))},
			columns{col1: {"B", "A", "C"}, col2: {"D", "E"}}},
		{"across columns at index", "B", UpdateTodoParams{CategoryID: ptr(col2), Position: ptr(int64(1))},
			columns{col1: {"A", "C"}, col2: {"D", "B", "E"}}},
		{"across past the end", "A", UpdateTodoParams{CategoryID: ptr(col2), Position: ptr(int64(99))},
			columns{col1: {"B", "C"}, col2: {"D", "E", "A"}}},
		{"within past the end", "A", UpdateTodoParams{Position: ptr(int64(99))},
			columns{col1: {"B", "C", "A"}, col2: {"D", "E"}}},
		{"category only goes to top", "C", UpdateTodoParams{CategoryID: ptr(col2)},
			columns{col1: {"A", "B"}, col2: {"C", "D", "E"}}},
		{"same category keeps place", "B", UpdateTodoParams{CategoryID: ptr(col1)},
			columns{col1: {"A", "B", "C"}, col2: {"D", "E"}}},
		{"title only keeps place", "B", UpdateTodoParams{Title: ptr("B2")},
			columns{col1: {"A", "B2", "C"}, col2: {"D", "E"}}},

		// Beyond the cases of the brief.
		{"same position keeps place", "B", UpdateTodoParams{Position: ptr(int64(1))},
			columns{col1: {"A", "B", "C"}, col2: {"D", "E"}}},
		// Another column has room for one more place than the column the todo
		// leaves: the end is position 2 there, and 2 must not be cut back to 1.
		{"across to the end exactly", "A", UpdateTodoParams{CategoryID: ptr(col2), Position: ptr(int64(2))},
			columns{col1: {"B", "C"}, col2: {"D", "E", "A"}}},
		{"into an empty column", "A", UpdateTodoParams{CategoryID: ptr(col3), Position: ptr(int64(0))},
			columns{col1: {"B", "C"}, col2: {"D", "E"}, col3: {"A"}}},
		{"into an empty column past the end", "B", UpdateTodoParams{CategoryID: ptr(col3), Position: ptr(int64(5))},
			columns{col1: {"A", "C"}, col2: {"D", "E"}, col3: {"B"}}},
		{"within the largest position", "A", UpdateTodoParams{Position: ptr(int64(math.MaxInt64))},
			columns{col1: {"B", "C", "A"}, col2: {"D", "E"}}},
		{"title and position", "B", UpdateTodoParams{Title: ptr("B2"), Position: ptr(int64(0))},
			columns{col1: {"B2", "A", "C"}, col2: {"D", "E"}}},
		// Every move above starts in the first column; these start in the second.
		{"up within the second column", "E", UpdateTodoParams{Position: ptr(int64(0))},
			columns{col1: {"A", "B", "C"}, col2: {"E", "D"}}},
		{"from the second column to the first", "D", UpdateTodoParams{CategoryID: ptr(col1), Position: ptr(int64(1))},
			columns{col1: {"A", "D", "B", "C"}, col2: {"E"}}},
		{"from the second column to the third", "D", UpdateTodoParams{CategoryID: ptr(col3)},
			columns{col1: {"A", "B", "C"}, col2: {"E"}, col3: {"D"}}},
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

			for _, categoryID := range []int64{col1, col2, col3} {
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

// All the fields at once: the single UPDATE statement binds them by position,
// so a mix-up between neighbouring parameters shows here.
func TestUpdateTodoAllFieldsWithPosition(t *testing.T) {
	s, ids := newBoard(t)

	got, err := s.UpdateTodo(t.Context(), ids["B"], UpdateTodoParams{
		Title:       ptr("B2"),
		Description: ptr("açıklama"),
		Status:      ptr(StatusInProgress),
		CategoryID:  ptr(col2),
		Position:    ptr(int64(1)),
		Metadata:    json.RawMessage(`{"k":1}`),
	})
	if err != nil {
		t.Fatalf("UpdateTodo: %v", err)
	}

	if got.Title != "B2" || got.Description != "açıklama" || got.Status != StatusInProgress ||
		got.CategoryID != col2 || got.Position != 1 || string(got.Metadata) != `{"k":1}` {
		t.Errorf("UpdateTodo = %s, want title B2, description açıklama, in progress, category %d, position 1, metadata {\"k\":1}",
			showTodo(got), col2)
	}
	if got.Classification != ClassificationManual {
		t.Errorf("Classification = %q, want %q after a move to another category", got.Classification, ClassificationManual)
	}
	assertOrder(t, s, col1, "A", "C")
	assertOrder(t, s, col2, "D", "B2", "E")
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
		assertOrder(t, s, col1, "A", "B", "C")
		assertOrder(t, s, col2, "D", "E")
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
		assertOrder(t, s, col1, "A", "B", "C") // B has not been renamed or moved
		assertOrder(t, s, col2, "D", "E")
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

// The metadata.ai the worker writes (step 3 spec 4): on success the model's
// answer, on failure the error.
const (
	aiSuccess = `{"model":"m","category":"purchase","reason":"r","duration_ms":5,"at":"2026-10-25T10:00:00.000Z"}`
	aiFailure = `{"model":"m","error":"boom","at":"2026-10-25T10:00:00.000Z"}`
)

// metadataKeys decodes the metadata of a todo into its top-level keys.
func metadataKeys(t *testing.T, todo Todo) map[string]json.RawMessage {
	t.Helper()
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(todo.Metadata, &keys); err != nil {
		t.Fatalf("metadata %s of %s is not a JSON object: %v", todo.Metadata, todo.Title, err)
	}
	return keys
}

// assertAI fails the test unless metadata.ai of the todo is the JSON value want,
// or, for want == "", is not there. JSON is compared by value, not by text.
func assertAI(t *testing.T, todo Todo, want string) {
	t.Helper()
	got, ok := metadataKeys(t, todo)["ai"]
	if want == "" {
		if ok {
			t.Errorf("%s: metadata = %s, want no metadata.ai", todo.Title, todo.Metadata)
		}
		return
	}
	if !ok {
		t.Errorf("%s: metadata = %s, want metadata.ai = %s", todo.Title, todo.Metadata, want)
		return
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("%s: metadata.ai %s: %v", todo.Title, got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("%s: metadata.ai = %s, want %s", todo.Title, got, want)
	}
}

// mustGet returns the stored todo.
func mustGet(t *testing.T, s *Store, id int64) Todo {
	t.Helper()
	todo, err := s.GetTodo(t.Context(), id)
	if err != nil {
		t.Fatalf("GetTodo(%d): %v", id, err)
	}
	return todo
}

// Step 3 spec 5. The result of the AI is applied only to a todo that is still in
// the inbox and still pending; otherwise nothing changes. This is the guard
// against an answer that arrives after the user has moved the card by hand.
func TestApplyClassification(t *testing.T) {
	t.Run("moves a pending inbox todo to the top of its category", func(t *testing.T) {
		s := newTestStore(t)
		createInOrder(t, s, catPurchase, "Eski") // the category holds a todo already
		x, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al", Status: StatusInProgress, Metadata: json.RawMessage(`{"k":1}`)})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		if _, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Y"}); err != nil { // on top of x in the inbox
			t.Fatalf("CreateTodo: %v", err)
		}
		time.Sleep(20 * time.Millisecond) // timestamps have millisecond resolution

		got, applied, err := s.ApplyClassification(t.Context(), x.ID, "purchase", json.RawMessage(aiSuccess))

		if err != nil || !applied {
			t.Fatalf("ApplyClassification = (applied %v, err %v), want it applied", applied, err)
		}
		if got.CategoryID != catPurchase || got.Position != 0 || got.Classification != ClassificationDone {
			t.Errorf("got %s, want the top of category %d, classification done", showTodo(got), catPurchase)
		}
		if got.Title != "Süt al" || got.Status != StatusInProgress {
			t.Errorf("got %s, want title and status as they were", showTodo(got))
		}
		assertAI(t, got, aiSuccess)
		if k := metadataKeys(t, got)["k"]; string(k) != "1" {
			t.Errorf("metadata = %s, want the other keys kept", got.Metadata)
		}
		if class := metadataStorageClass(t, s, x.ID); class != "text" {
			t.Errorf("metadata is stored as %s, want text", class)
		}
		if !got.UpdatedAt.After(x.UpdatedAt) {
			t.Errorf("UpdatedAt = %v, want it after %v", got.UpdatedAt, x.UpdatedAt)
		}
		assertOrder(t, s, catPurchase, "Süt al", "Eski") // on top of the one that was there
		assertOrder(t, s, catInbox, "Y")                 // the gap in the inbox is closed
		assertDensePositions(t, s)
		if stored := mustGet(t, s, x.ID); !sameTodo(stored, got) {
			t.Errorf("GetTodo = %s, want what ApplyClassification returned: %s", showTodo(stored), showTodo(got))
		}

		// A second answer for the same todo finds it done and changes nothing.
		again, applied, err := s.ApplyClassification(t.Context(), x.ID, "research", json.RawMessage(aiFailure))
		if err != nil || applied {
			t.Errorf("second ApplyClassification = (applied %v, err %v), want not applied", applied, err)
		}
		if !sameTodo(again, got) {
			t.Errorf("second ApplyClassification returned %s, want the todo as it is: %s", showTodo(again), showTodo(got))
		}
		assertOrder(t, s, catPurchase, "Süt al", "Eski")
	})

	t.Run("a todo that was moved by hand in the meantime is left alone", func(t *testing.T) {
		s := newTestStore(t)
		x, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al"})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		// The user drags the card to "research" while the AI is still thinking.
		if _, err := s.UpdateTodo(t.Context(), x.ID, UpdateTodoParams{CategoryID: ptr(catResearch)}); err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		before := mustGet(t, s, x.ID)

		got, applied, err := s.ApplyClassification(t.Context(), x.ID, "purchase", json.RawMessage(aiSuccess))

		if err != nil || applied {
			t.Fatalf("ApplyClassification = (applied %v, err %v), want it not applied", applied, err)
		}
		if got.CategoryID != catResearch || got.Classification != ClassificationManual {
			t.Errorf("got %s, want the todo to stay in category %d as manual", showTodo(got), catResearch)
		}
		if stored := mustGet(t, s, x.ID); !sameTodo(stored, before) {
			t.Errorf("the todo changed: got %s, want %s", showTodo(stored), showTodo(before))
		}
		assertAI(t, got, "")
		assertOrder(t, s, catResearch, "Süt al")
		assertOrder(t, s, catPurchase)
	})

	// The user can put a card back into the inbox by hand. It is in the inbox
	// again, but it is the user's decision (manual), not a request for the AI.
	t.Run("a todo that was moved by hand back into the inbox is left alone", func(t *testing.T) {
		s := newTestStore(t)
		x, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al"})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		for _, category := range []int64{catResearch, catInbox} {
			if _, err := s.UpdateTodo(t.Context(), x.ID, UpdateTodoParams{CategoryID: ptr(category)}); err != nil {
				t.Fatalf("UpdateTodo into category %d: %v", category, err)
			}
		}
		before := mustGet(t, s, x.ID)
		if before.CategoryID != catInbox || before.Classification != ClassificationManual {
			t.Fatalf("setup: got %s, want a manual todo in the inbox", showTodo(before))
		}

		got, applied, err := s.ApplyClassification(t.Context(), x.ID, "purchase", json.RawMessage(aiSuccess))

		if err != nil || applied {
			t.Fatalf("ApplyClassification = (applied %v, err %v), want it not applied", applied, err)
		}
		if !sameTodo(got, before) || !sameTodo(mustGet(t, s, x.ID), before) {
			t.Errorf("the manual todo changed: got %s, want %s", showTodo(got), showTodo(before))
		}
		assertAI(t, got, "")
	})

	t.Run("a failed or finished todo is left alone", func(t *testing.T) {
		s := newTestStore(t)
		failed, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Başarısız"})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		if _, ok, err := s.FailClassification(t.Context(), failed.ID, json.RawMessage(aiFailure)); err != nil || !ok {
			t.Fatalf("FailClassification = (%v, %v)", ok, err)
		}
		before := mustGet(t, s, failed.ID)

		got, applied, err := s.ApplyClassification(t.Context(), failed.ID, "purchase", json.RawMessage(aiSuccess))

		if err != nil || applied {
			t.Fatalf("ApplyClassification = (applied %v, err %v), want it not applied", applied, err)
		}
		if !sameTodo(got, before) || !sameTodo(mustGet(t, s, failed.ID), before) {
			t.Errorf("the failed todo changed: got %s, want %s", showTodo(got), showTodo(before))
		}
		if got.CategoryID != catInbox || got.Classification != ClassificationFailed {
			t.Errorf("got %s, want it to stay failed in the inbox", showTodo(got))
		}
		assertAI(t, got, aiFailure)
	})

	t.Run("a pending todo outside the inbox is left alone", func(t *testing.T) {
		s := newTestStore(t)
		x, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al"})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		// The store never produces this state; the guard checks the place as well as the state.
		if _, err := s.db.Exec(`UPDATE todos SET category_id = ?, position = 0 WHERE id = ?`, catWork, x.ID); err != nil {
			t.Fatalf("move by SQL: %v", err)
		}
		before := mustGet(t, s, x.ID)

		got, applied, err := s.ApplyClassification(t.Context(), x.ID, "purchase", json.RawMessage(aiSuccess))

		if err != nil || applied {
			t.Fatalf("ApplyClassification = (applied %v, err %v), want it not applied", applied, err)
		}
		if !sameTodo(got, before) || !sameTodo(mustGet(t, s, x.ID), before) {
			t.Errorf("the todo changed: got %s, want %s", showTodo(got), showTodo(before))
		}
	})

	t.Run("errors", func(t *testing.T) {
		s := newTestStore(t)
		x, err := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al"})
		if err != nil {
			t.Fatalf("CreateTodo: %v", err)
		}
		before := mustGet(t, s, x.ID)

		if _, _, err := s.ApplyClassification(t.Context(), 999, "purchase", json.RawMessage(aiSuccess)); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown id: err = %v, want ErrNotFound", err)
		}
		// The todo is reported first, as UpdateTodo does.
		if _, _, err := s.ApplyClassification(t.Context(), 999, "xyz", json.RawMessage(aiSuccess)); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown id and key: err = %v, want ErrNotFound", err)
		}
		// The inbox is where todos wait for the AI, not a place the AI can put them.
		for _, key := range []string{"xyz", "", InboxKey, "Purchase"} {
			if _, _, err := s.ApplyClassification(t.Context(), x.ID, key, json.RawMessage(aiSuccess)); !errors.Is(err, ErrInvalidCategory) {
				t.Errorf("key %q: err = %v, want ErrInvalidCategory", key, err)
			}
		}
		if stored := mustGet(t, s, x.ID); !sameTodo(stored, before) {
			t.Errorf("a rejected call changed the todo: got %s, want %s", showTodo(stored), showTodo(before))
		}
	})
}

func TestFailClassification(t *testing.T) {
	t.Run("a pending todo fails and stays where it is", func(t *testing.T) {
		s := newTestStore(t)
		a, _ := s.CreateTodo(t.Context(), CreateTodoParams{Title: "A", Metadata: json.RawMessage(`{"k":1}`)})
		b, _ := s.CreateTodo(t.Context(), CreateTodoParams{Title: "B"}) // on top of A
		time.Sleep(20 * time.Millisecond)

		got, applied, err := s.FailClassification(t.Context(), a.ID, json.RawMessage(aiFailure))

		if err != nil || !applied {
			t.Fatalf("FailClassification = (applied %v, err %v), want it applied", applied, err)
		}
		if got.Classification != ClassificationFailed || got.CategoryID != catInbox || got.Position != 1 {
			t.Errorf("got %s, want failed, in the inbox at position 1", showTodo(got))
		}
		assertAI(t, got, aiFailure)
		if k := metadataKeys(t, got)["k"]; string(k) != "1" {
			t.Errorf("metadata = %s, want the other keys kept", got.Metadata)
		}
		if !got.UpdatedAt.After(a.UpdatedAt) {
			t.Errorf("UpdatedAt = %v, want it after %v", got.UpdatedAt, a.UpdatedAt)
		}
		assertOrder(t, s, catInbox, "B", "A")
		if other := mustGet(t, s, b.ID); other.Classification != ClassificationPending {
			t.Errorf("B = %s, want it still pending", showTodo(other))
		}
		if stored := mustGet(t, s, a.ID); !sameTodo(stored, got) {
			t.Errorf("GetTodo = %s, want what FailClassification returned: %s", showTodo(stored), showTodo(got))
		}

		// A second failure does not overwrite the first.
		again, applied, err := s.FailClassification(t.Context(), a.ID, json.RawMessage(`{"model":"m","error":"other","at":"x"}`))
		if err != nil || applied {
			t.Errorf("second FailClassification = (applied %v, err %v), want not applied", applied, err)
		}
		if !sameTodo(again, got) {
			t.Errorf("second FailClassification returned %s, want the todo as it is: %s", showTodo(again), showTodo(got))
		}
	})

	t.Run("a todo that was moved by hand is left alone", func(t *testing.T) {
		s := newTestStore(t)
		x, _ := s.CreateTodo(t.Context(), CreateTodoParams{Title: "X"})
		if _, err := s.UpdateTodo(t.Context(), x.ID, UpdateTodoParams{CategoryID: ptr(catHome)}); err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}
		before := mustGet(t, s, x.ID)

		got, applied, err := s.FailClassification(t.Context(), x.ID, json.RawMessage(aiFailure))

		if err != nil || applied {
			t.Fatalf("FailClassification = (applied %v, err %v), want it not applied", applied, err)
		}
		if !sameTodo(got, before) || !sameTodo(mustGet(t, s, x.ID), before) {
			t.Errorf("the manual todo changed: got %s, want %s", showTodo(got), showTodo(before))
		}
		assertAI(t, got, "")
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestStore(t)

		if _, _, err := s.FailClassification(t.Context(), 999, json.RawMessage(aiFailure)); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

func TestRequeueClassification(t *testing.T) {
	t.Run("a classified todo goes back to the top of the inbox", func(t *testing.T) {
		s := newTestStore(t)
		x, _ := s.CreateTodo(t.Context(), CreateTodoParams{Title: "Süt al", Metadata: json.RawMessage(`{"k":1}`)})
		if _, ok, err := s.ApplyClassification(t.Context(), x.ID, "purchase", json.RawMessage(aiSuccess)); err != nil || !ok {
			t.Fatalf("ApplyClassification = (%v, %v)", ok, err)
		}
		createInOrder(t, s, catPurchase, "Alt") // purchase: Alt, Süt al
		createInOrder(t, s, catInbox, "I1", "I2")
		assertOrder(t, s, catPurchase, "Alt", "Süt al")
		before := mustGet(t, s, x.ID)
		time.Sleep(20 * time.Millisecond)

		got, err := s.RequeueClassification(t.Context(), x.ID)

		if err != nil {
			t.Fatalf("RequeueClassification: %v", err)
		}
		if got.CategoryID != catInbox || got.Position != 0 || got.Classification != ClassificationPending {
			t.Errorf("got %s, want the top of the inbox, pending", showTodo(got))
		}
		assertAI(t, got, "")
		if k := metadataKeys(t, got)["k"]; string(k) != "1" {
			t.Errorf("metadata = %s, want the other keys kept", got.Metadata)
		}
		if !got.UpdatedAt.After(before.UpdatedAt) {
			t.Errorf("UpdatedAt = %v, want it after %v", got.UpdatedAt, before.UpdatedAt)
		}
		assertOrder(t, s, catInbox, "Süt al", "I2", "I1")
		assertOrder(t, s, catPurchase, "Alt") // the gap is closed
		assertDensePositions(t, s)
		if stored := mustGet(t, s, x.ID); !sameTodo(stored, got) {
			t.Errorf("GetTodo = %s, want what RequeueClassification returned: %s", showTodo(stored), showTodo(got))
		}
	})

	t.Run("failed and manual todos", func(t *testing.T) {
		s := newTestStore(t)
		ids := createInOrder(t, s, catInbox, "A", "B", "C") // inbox: C, B, A
		if _, ok, err := s.FailClassification(t.Context(), ids["B"], json.RawMessage(aiFailure)); err != nil || !ok {
			t.Fatalf("FailClassification = (%v, %v)", ok, err)
		}
		if _, err := s.UpdateTodo(t.Context(), ids["A"], UpdateTodoParams{CategoryID: ptr(catWork)}); err != nil {
			t.Fatalf("UpdateTodo: %v", err)
		}

		failed, err := s.RequeueClassification(t.Context(), ids["B"])
		if err != nil {
			t.Fatalf("RequeueClassification(B): %v", err)
		}
		if failed.Classification != ClassificationPending || failed.Position != 0 {
			t.Errorf("B = %s, want pending at the top", showTodo(failed))
		}
		assertAI(t, failed, "")
		assertOrder(t, s, catInbox, "B", "C")

		manual, err := s.RequeueClassification(t.Context(), ids["A"])
		if err != nil {
			t.Fatalf("RequeueClassification(A): %v", err)
		}
		if manual.Classification != ClassificationPending || manual.CategoryID != catInbox || manual.Position != 0 {
			t.Errorf("A = %s, want pending at the top of the inbox", showTodo(manual))
		}
		assertOrder(t, s, catInbox, "A", "B", "C")
		assertOrder(t, s, catWork)
		assertDensePositions(t, s)
	})

	t.Run("a todo that is on top of the inbox already", func(t *testing.T) {
		s := newTestStore(t)
		ids := createInOrder(t, s, catInbox, "A", "B") // inbox: B, A
		if _, ok, err := s.FailClassification(t.Context(), ids["B"], json.RawMessage(aiFailure)); err != nil || !ok {
			t.Fatalf("FailClassification = (%v, %v)", ok, err)
		}

		got, err := s.RequeueClassification(t.Context(), ids["B"])

		if err != nil {
			t.Fatalf("RequeueClassification: %v", err)
		}
		if got.Classification != ClassificationPending || got.Position != 0 {
			t.Errorf("got %s, want pending at position 0", showTodo(got))
		}
		assertOrder(t, s, catInbox, "B", "A")
		assertDensePositions(t, s)
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestStore(t)

		if _, err := s.RequeueClassification(t.Context(), 999); !errors.Is(err, ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})
}

// The worker takes the pending todos at start-up, oldest first.
func TestPendingTodoIDs(t *testing.T) {
	s := newTestStore(t)

	ids, err := s.PendingTodoIDs(t.Context())
	if err != nil {
		t.Fatalf("PendingTodoIDs: %v", err)
	}
	if ids == nil || len(ids) != 0 {
		t.Errorf("PendingTodoIDs on an empty database = %#v, want an empty, non-nil slice", ids)
	}

	// Five todos; the ids run against the ages: by age they are 3, 5, 1, 2 (the
	// last two have the same age, so the id decides), and todo 4 is the oldest
	// but not pending any more.
	byTitle := createInOrder(t, s, catInbox, "t1", "t2", "t3", "t4", "t5")
	for title, createdAt := range map[string]string{
		"t1": "2026-01-01T00:00:03.000Z",
		"t2": "2026-01-01T00:00:03.000Z",
		"t3": "2026-01-01T00:00:01.000Z",
		"t4": "2026-01-01T00:00:00.000Z",
		"t5": "2026-01-01T00:00:02.000Z",
	} {
		if _, err := s.db.Exec(`UPDATE todos SET created_at = ? WHERE id = ?`, createdAt, byTitle[title]); err != nil {
			t.Fatalf("set created_at of %s: %v", title, err)
		}
	}
	if _, err := s.UpdateTodo(t.Context(), byTitle["t4"], UpdateTodoParams{CategoryID: ptr(catOther)}); err != nil { // manual
		t.Fatalf("UpdateTodo: %v", err)
	}

	got, err := s.PendingTodoIDs(t.Context())
	if err != nil {
		t.Fatalf("PendingTodoIDs: %v", err)
	}
	if want := []int64{byTitle["t3"], byTitle["t5"], byTitle["t1"], byTitle["t2"]}; !slices.Equal(got, want) {
		t.Errorf("PendingTodoIDs = %v, want %v (t3, t5, t1, t2: oldest first, then by id)", got, want)
	}

	// Failed and finished todos are not pending.
	if _, _, err := s.FailClassification(t.Context(), byTitle["t3"], json.RawMessage(aiFailure)); err != nil {
		t.Fatalf("FailClassification: %v", err)
	}
	if _, _, err := s.ApplyClassification(t.Context(), byTitle["t5"], "work", json.RawMessage(aiSuccess)); err != nil {
		t.Fatalf("ApplyClassification: %v", err)
	}
	got, err = s.PendingTodoIDs(t.Context())
	if err != nil {
		t.Fatalf("PendingTodoIDs: %v", err)
	}
	if want := []int64{byTitle["t1"], byTitle["t2"]}; !slices.Equal(got, want) {
		t.Errorf("PendingTodoIDs after a failure and a classification = %v, want %v", got, want)
	}
}
