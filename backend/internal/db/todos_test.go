package db

import (
	"encoding/json"
	"errors"
	"fmt"
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
		string(a.Metadata) == string(b.Metadata) &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.UpdatedAt.Equal(b.UpdatedAt)
}

// showTodo formats a todo for failure messages; %+v would print the metadata as
// a list of byte values.
func showTodo(t Todo) string {
	return fmt.Sprintf("{id=%d title=%q category=%d metadata=%s created=%s updated=%s}",
		t.ID, t.Title, t.CategoryID, t.Metadata,
		t.CreatedAt.Format(time.RFC3339Nano), t.UpdatedAt.Format(time.RFC3339Nano))
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

// Sequential inserts cannot tell "created_at DESC, id DESC" from "id DESC", so
// this inserts rows whose timestamps run against the id order, and a tie.
func TestListTodosOrdersByCreatedAtThenID(t *testing.T) {
	s := newTestStore(t)
	rows := []struct{ title, createdAt string }{
		{"id1 oldest", "2026-01-01T00:00:00.000Z"},
		{"id2 newest, tied", "2026-01-03T00:00:00.000Z"},
		{"id3 newest, tied", "2026-01-03T00:00:00.000Z"},
		{"id4 middle", "2026-01-02T00:00:00.000Z"},
	}
	for _, r := range rows {
		_, err := s.db.Exec(
			`INSERT INTO todos (title, category_id, created_at, updated_at) VALUES (?, 1, ?, ?)`,
			r.title, r.createdAt, r.createdAt)
		if err != nil {
			t.Fatalf("insert %q: %v", r.title, err)
		}
	}

	todos, err := s.ListTodos(t.Context())
	if err != nil {
		t.Fatalf("ListTodos: %v", err)
	}

	var got []string
	for _, todo := range todos {
		got = append(got, todo.Title)
	}
	want := []string{"id3 newest, tied", "id2 newest, tied", "id4 middle", "id1 oldest"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %q, want %q", got, want)
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
