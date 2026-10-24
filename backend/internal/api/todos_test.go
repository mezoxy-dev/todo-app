package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// todoJSON mirrors the Todo shape of spec 6.2 and step 2 spec 4.5, with the
// timestamps as plain text so that their format can be checked.
type todoJSON struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	CategoryID int64           `json:"category_id"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
	Position   int64           `json:"position"`
}

// String makes failure messages readable: the metadata prints as JSON, not as bytes.
func (td todoJSON) String() string {
	b, _ := json.Marshal(td)
	return string(b)
}

// mustCreate posts body to /api/todos, expects 201, and returns the todo.
func mustCreate(t *testing.T, h http.Handler, body string) todoJSON {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/todos", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/todos %s: status = %d, want 201 (body %q)", body, rec.Code, rec.Body.String())
	}
	return decodeBody[todoJSON](t, rec)
}

// bodyOfSize returns a request body of exactly n bytes: a title made of "a".
func bodyOfSize(n int) string {
	const prefix, suffix = `{"title":"`, `"}`
	return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
}

func TestCreateTodo(t *testing.T) {
	h, _ := newTestAPI(t)

	// Neither category nor metadata: the first column and an empty object.
	rec := do(t, h, http.MethodPost, "/api/todos", `{"title":"  Süt al  "}`)
	wantJSON(t, rec, http.StatusCreated)
	first := decodeBody[todoJSON](t, rec)
	if first.ID <= 0 {
		t.Errorf("id = %d, want a positive id", first.ID)
	}
	if first.Title != "Süt al" {
		t.Errorf("title = %q, want %q (trimmed)", first.Title, "Süt al")
	}
	if first.CategoryID != 1 {
		t.Errorf("category_id = %d, want 1 (the first column)", first.CategoryID)
	}
	if first.Position != 0 {
		t.Errorf("position = %d, want 0 (the top of its column)", first.Position)
	}
	var metadata map[string]any
	if err := json.Unmarshal(first.Metadata, &metadata); err != nil || metadata == nil || len(metadata) != 0 {
		t.Errorf("metadata = %s, want an empty JSON object", first.Metadata)
	}
	for name, ts := range map[string]string{"created_at": first.CreatedAt, "updated_at": first.UpdatedAt} {
		if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
			t.Errorf("%s = %q is not an RFC 3339 timestamp: %v", name, ts, err)
		}
		if !strings.HasSuffix(ts, "Z") {
			t.Errorf("%s = %q, want UTC (a trailing Z)", name, ts)
		}
	}

	// With a category and metadata.
	rec = do(t, h, http.MethodPost, "/api/todos", `{"title":"Fatura öde","category_id":3,"metadata":{"a":1}}`)
	wantJSON(t, rec, http.StatusCreated)
	second := decodeBody[todoJSON](t, rec)
	if second.Title != "Fatura öde" || second.CategoryID != 3 {
		t.Errorf("second todo = %v, want title %q in category 3", second, "Fatura öde")
	}
	if string(second.Metadata) != `{"a":1}` {
		t.Errorf("metadata = %s, want %s", second.Metadata, `{"a":1}`)
	}

	// Both are listed, column by column (step 2 spec 4.3): Süt al is in the first
	// column, Fatura öde in the third, and each is the top of its own column.
	rec = do(t, h, http.MethodGet, "/api/todos", "")
	wantJSON(t, rec, http.StatusOK)
	list := decodeBody[[]todoJSON](t, rec)
	if len(list) != 2 || list[0].Title != "Süt al" || list[1].Title != "Fatura öde" {
		t.Errorf("GET /api/todos = %v, want [Süt al, Fatura öde]", list)
	}
	if len(list) == 2 && (list[0].Position != 0 || list[1].Position != 0) {
		t.Errorf("GET /api/todos = %v, want both todos at position 0", list)
	}
}

// The object that was sent is stored in its compact form (json.Compact). The
// response cannot show that, since encoding/json compacts raw JSON on its way
// out, so the stored text is read from the store.
func TestCreateTodoStoresCompactMetadata(t *testing.T) {
	h, store := newTestAPI(t)

	created := mustCreate(t, h, `{"title":"x","metadata":{ "k" : [1, 2],  "s" : "a b" }}`)

	stored, err := store.GetTodo(t.Context(), created.ID)
	if err != nil {
		t.Fatalf("GetTodo: %v", err)
	}
	if want := `{"k":[1,2],"s":"a b"}`; string(stored.Metadata) != want {
		t.Errorf("stored metadata = %s, want %s", stored.Metadata, want)
	}
}

// Spec 6.3: the limit of 500 counts characters, not bytes. 500 times "ğ" is
// 1000 bytes.
func TestCreateTodoRuneLimit(t *testing.T) {
	h, _ := newTestAPI(t)

	title := strings.Repeat("ğ", 500)
	rec := do(t, h, http.MethodPost, "/api/todos", `{"title":"`+title+`"}`)
	wantJSON(t, rec, http.StatusCreated)
	if got := decodeBody[todoJSON](t, rec).Title; got != title {
		t.Errorf("title of 500 characters came back changed: got %d characters", len([]rune(got)))
	}

	// The limit applies to the trimmed title.
	rec = do(t, h, http.MethodPost, "/api/todos", `{"title":"  `+title+`  "}`)
	wantJSON(t, rec, http.StatusCreated)
	if got := decodeBody[todoJSON](t, rec).Title; got != title {
		t.Errorf("padded title of 500 characters came back as %d characters", len([]rune(got)))
	}

	rec = do(t, h, http.MethodPost, "/api/todos", `{"title":"`+title+`ğ"}`)
	wantError(t, rec, http.StatusBadRequest, "title must be at most 500 characters")
}

func TestCreateTodoValidation(t *testing.T) {
	h, _ := newTestAPI(t)

	tests := []struct {
		name, body, want string
	}{
		{"no title", `{}`, "title is required"},
		{"blank title", `{"title":"   "}`, "title is required"},
		{"null title", `{"title":null}`, "title must not be null"},
		{"numeric title", `{"title":123}`, "title must be a string"},
		{"unknown category", `{"title":"x","category_id":99}`, "category does not exist"},
		{"string category_id", `{"title":"x","category_id":"3"}`, "category_id must be an integer"},
		{"null category_id", `{"title":"x","category_id":null}`, "category_id must not be null"},
		{"array metadata", `{"title":"x","metadata":[1]}`, "metadata must be a JSON object"},
		{"string metadata", `{"title":"x","metadata":"str"}`, "metadata must be a JSON object"},
		{"unknown field", `{"title":"x","extra":1}`, `unknown field "extra"`},
		{"truncated JSON", `{"title":`, "invalid JSON body"},
		{"body of exactly 1 MiB", bodyOfSize(1 << 20), "title must be at most 500 characters"},
		{"body over 1 MiB", bodyOfSize(1<<20 + 1), "request body too large"},

		// Beyond the cases of the brief: what decodeJSON and the field checks promise.
		{"empty body", ``, "invalid JSON body"},
		{"two objects", `{"title":"a"}{"title":"b"}`, "invalid JSON body"},
		{"garbage after the object", `{"title":"a"} x`, "invalid JSON body"},
		{"null body", `null`, "invalid JSON body"},
		{"array body", `[{"title":"a"}]`, "invalid JSON body"},
		{"string body", `"a"`, "invalid JSON body"},
		{"boolean title", `{"title":true}`, "title must be a string"},
		{"fractional category_id", `{"title":"x","category_id":1.5}`, "category_id must be an integer"},
		{"boolean category_id", `{"title":"x","category_id":true}`, "category_id must be an integer"},
		{"category_id zero", `{"title":"x","category_id":0}`, "category does not exist"},
		{"null metadata", `{"title":"x","metadata":null}`, "metadata must not be null"},
		{"number metadata", `{"title":"x","metadata":5}`, "metadata must be a JSON object"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/todos", tt.body)

			wantError(t, rec, http.StatusBadRequest, tt.want)
		})
	}

	// None of the rejected requests stored anything.
	rec := do(t, h, http.MethodGet, "/api/todos", "")
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("GET /api/todos after the rejected requests = %s, want []", got)
	}
}

// An upload over the limit must still get its 400. The server closes the
// connection when a body is not read to its end, and a close that leaves
// unread data behind can reset the connection and take the response with it.
// http.MaxBytesReader has the server close in an orderly way, but only if it is
// given the server's own ResponseWriter, not the wrapper that logs requests.
// That cannot be seen with httptest.ResponseRecorder, so this test uses a real
// connection, and announces the body with Expect: 100-continue as curl does for
// bodies over 1 MiB.
func TestOversizedUploadGetsItsResponse(t *testing.T) {
	h, _ := newTestAPI(t)
	srv := httptest.NewServer(h)
	defer srv.Close()

	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))

	const size = 4 << 20
	fmt.Fprintf(conn, "POST /api/todos HTTP/1.1\r\nHost: test\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n", size)
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil || resp.StatusCode != http.StatusContinue {
		t.Fatalf("answer to the headers: %v, %v; want 100 Continue", resp, err)
	}

	go func() {
		// Errors are fine: the server may hang up before the whole body is sent.
		chunk := bytes.Repeat([]byte("a"), 32<<10)
		for sent := 0; sent < size; sent += len(chunk) {
			if _, err := conn.Write(chunk); err != nil {
				return
			}
		}
	}()

	// A client that is not waiting in a read when the answer arrives loses it to
	// the reset. The orderly close keeps the connection readable for 500 ms.
	time.Sleep(150 * time.Millisecond)
	resp, err = http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("reading the answer to an oversized upload: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading the body of the answer: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest || strings.TrimSpace(string(body)) != `{"error":"request body too large"}` {
		t.Errorf("answer = %d %q, want 400 and the error %q", resp.StatusCode, body, "request body too large")
	}
}

func TestGetTodoByID(t *testing.T) {
	h, _ := newTestAPI(t)
	created := mustCreate(t, h, `{"title":"Süt al","category_id":2,"metadata":{"a":1}}`)

	rec := do(t, h, http.MethodGet, todoPath(created.ID), "")

	wantJSON(t, rec, http.StatusOK)
	if got := decodeBody[todoJSON](t, rec); !reflect.DeepEqual(got, created) {
		t.Errorf("GET returned %v, want the todo that was created: %v", got, created)
	}

	wantError(t, do(t, h, http.MethodGet, "/api/todos/999", ""), http.StatusNotFound, "todo not found")
	for _, id := range []string{"abc", "0", "-1", "1.5", "99999999999999999999"} {
		wantError(t, do(t, h, http.MethodGet, "/api/todos/"+id, ""), http.StatusBadRequest, "invalid id")
	}
}

func TestPatchTodo(t *testing.T) {
	h, _ := newTestAPI(t)
	created := mustCreate(t, h, `{"title":"Süt al"}`)
	path := todoPath(created.ID)

	// Timestamps have a resolution of a millisecond; let updated_at move.
	time.Sleep(5 * time.Millisecond)

	// Only the title: it is trimmed, the category stays.
	rec := do(t, h, http.MethodPatch, path, `{"title":"  Yeni  "}`)
	wantJSON(t, rec, http.StatusOK)
	renamed := decodeBody[todoJSON](t, rec)
	if renamed.ID != created.ID || renamed.Title != "Yeni" || renamed.CategoryID != created.CategoryID {
		t.Errorf("after renaming: %v, want todo %d titled %q in category %d", renamed, created.ID, "Yeni", created.CategoryID)
	}
	if renamed.CreatedAt != created.CreatedAt {
		t.Errorf("created_at changed from %s to %s", created.CreatedAt, renamed.CreatedAt)
	}
	createdAt, err1 := time.Parse(time.RFC3339Nano, created.CreatedAt)
	updatedAt, err2 := time.Parse(time.RFC3339Nano, renamed.UpdatedAt)
	if err1 != nil || err2 != nil || !updatedAt.After(createdAt) {
		t.Errorf("updated_at = %s, want a time after created_at = %s", renamed.UpdatedAt, created.CreatedAt)
	}

	// Only the category (a move): the title stays, and a later GET agrees.
	rec = do(t, h, http.MethodPatch, path, `{"category_id":2}`)
	wantJSON(t, rec, http.StatusOK)
	moved := decodeBody[todoJSON](t, rec)
	if moved.CategoryID != 2 || moved.Title != "Yeni" {
		t.Errorf("after moving: %v, want category 2 and title %q", moved, "Yeni")
	}
	rec = do(t, h, http.MethodGet, path, "")
	if got := decodeBody[todoJSON](t, rec); got.CategoryID != 2 {
		t.Errorf("GET after the move: category_id = %d, want 2", got.CategoryID)
	}

	// Metadata replaces the old object as a whole; it is not merged into it.
	withMetadata := mustCreate(t, h, `{"title":"m","metadata":{"a":1}}`)
	rec = do(t, h, http.MethodPatch, todoPath(withMetadata.ID), `{"metadata":{"b":2}}`)
	wantJSON(t, rec, http.StatusOK)
	replaced := decodeBody[todoJSON](t, rec)
	if string(replaced.Metadata) != `{"b":2}` || replaced.Title != "m" {
		t.Errorf("after replacing the metadata: %v, want metadata %s and title %q", replaced, `{"b":2}`, "m")
	}
}

func TestPatchTodoValidation(t *testing.T) {
	h, _ := newTestAPI(t)
	created := mustCreate(t, h, `{"title":"Süt al","metadata":{"a":1}}`)
	path := todoPath(created.ID)

	tests := []struct {
		name, body, want string
	}{
		{"no fields", `{}`, "no fields to update"},
		{"unknown category", `{"category_id":99}`, "category does not exist"},
		{"null category_id", `{"category_id":null}`, "category_id must not be null"},
		{"empty title", `{"title":""}`, "title is required"},
		{"unknown field", `{"x":1}`, `unknown field "x"`},

		// Beyond the cases of the brief: PATCH shares the checks of POST.
		{"blank title", `{"title":"   "}`, "title is required"},
		{"null title", `{"title":null}`, "title must not be null"},
		{"numeric title", `{"title":5}`, "title must be a string"},
		{"title over 500 characters", `{"title":"` + strings.Repeat("ğ", 501) + `"}`, "title must be at most 500 characters"},
		{"string category_id", `{"category_id":"2"}`, "category_id must be an integer"},
		{"null metadata", `{"metadata":null}`, "metadata must not be null"},
		{"array metadata", `{"metadata":[1]}`, "metadata must be a JSON object"},
		{"truncated JSON", `{"title":`, "invalid JSON body"},
		{"empty body", ``, "invalid JSON body"},
		{"null body", `null`, "invalid JSON body"},
		// A valid field does not rescue the request: nothing is changed.
		{"valid title, unknown category", `{"title":"ok","category_id":99}`, "category does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, path, tt.body)

			wantError(t, rec, http.StatusBadRequest, tt.want)
		})
	}

	rec := do(t, h, http.MethodGet, path, "")
	if got := decodeBody[todoJSON](t, rec); !reflect.DeepEqual(got, created) {
		t.Errorf("after the rejected requests the todo is %v, want it unchanged: %v", got, created)
	}

	wantError(t, do(t, h, http.MethodPatch, "/api/todos/999", `{"title":"a"}`), http.StatusNotFound, "todo not found")
	for _, id := range []string{"abc", "0", "-1"} {
		wantError(t, do(t, h, http.MethodPatch, "/api/todos/"+id, `{"title":"a"}`), http.StatusBadRequest, "invalid id")
	}
}

// wantColumn checks that GET /api/todos lists exactly the given titles in the
// column, top to bottom, and that their positions are 0..n-1.
func wantColumn(t *testing.T, h http.Handler, categoryID int64, want ...string) {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/todos", "")
	wantJSON(t, rec, http.StatusOK)
	got := []string{}
	for _, todo := range decodeBody[[]todoJSON](t, rec) {
		if todo.CategoryID != categoryID {
			continue
		}
		if todo.Position != int64(len(got)) {
			t.Errorf("column %d: %s has position %d, want %d (0..n-1)", categoryID, todo.Title, todo.Position, len(got))
		}
		got = append(got, todo.Title)
	}
	if !slices.Equal(got, want) {
		t.Errorf("column %d = %q, want %q (top to bottom)", categoryID, got, want)
	}
}

// wantPositionFields checks that every todo in body, one JSON object or an
// array of them, has a "position" field that holds an integer. Decoding into
// todoJSON could not tell a missing field from the position 0.
func wantPositionFields(t *testing.T, body []byte) {
	t.Helper()
	var todos []map[string]json.RawMessage
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		if err := json.Unmarshal(body, &todos); err != nil {
			t.Fatalf("body %q is not an array of objects: %v", body, err)
		}
	} else {
		var todo map[string]json.RawMessage
		if err := json.Unmarshal(body, &todo); err != nil {
			t.Fatalf("body %q is not an object: %v", body, err)
		}
		todos = append(todos, todo)
	}
	if len(todos) == 0 {
		t.Fatalf("body %q holds no todo", body)
	}
	for _, todo := range todos {
		raw, ok := todo["position"]
		if !ok {
			t.Errorf(`todo %s has no "position" field`, todo["id"])
			continue
		}
		if _, err := strconv.ParseInt(string(raw), 10, 64); err != nil {
			t.Errorf(`todo %s: "position" = %s, want an integer`, todo["id"], raw)
		}
	}
}

// Every response that carries a todo has its position (step 2 spec 4.5).
func TestTodoResponsesHavePosition(t *testing.T) {
	h, _ := newTestAPI(t)

	rec := do(t, h, http.MethodPost, "/api/todos", `{"title":"A"}`)
	wantJSON(t, rec, http.StatusCreated)
	wantPositionFields(t, rec.Body.Bytes())
	path := todoPath(decodeBody[todoJSON](t, rec).ID)

	for _, tt := range []struct{ name, method, path, body string }{
		{"GET one", http.MethodGet, path, ""},
		{"PATCH", http.MethodPatch, path, `{"title":"A2"}`},
		{"GET list", http.MethodGet, "/api/todos", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.path, tt.body)

			wantJSON(t, rec, http.StatusOK)
			wantPositionFields(t, rec.Body.Bytes())
		})
	}
}

func TestPatchTodoPosition(t *testing.T) {
	h, _ := newTestAPI(t)
	a := mustCreate(t, h, `{"title":"A","category_id":1}`)
	b := mustCreate(t, h, `{"title":"B","category_id":1}`)
	mustCreate(t, h, `{"title":"C","category_id":1}`)
	wantColumn(t, h, 1, "C", "B", "A") // a new todo goes to the top

	// The position alone is enough for a PATCH: A goes from the bottom to the top.
	rec := do(t, h, http.MethodPatch, todoPath(a.ID), `{"position":0}`)
	wantJSON(t, rec, http.StatusOK)
	wantPositionFields(t, rec.Body.Bytes())
	if got := decodeBody[todoJSON](t, rec); got.ID != a.ID || got.CategoryID != 1 || got.Position != 0 {
		t.Errorf("after moving A to the top: %v, want todo %d in category 1 at position 0", got, a.ID)
	}
	wantColumn(t, h, 1, "A", "C", "B")

	// Column and position together. Column 3 is empty, so position 1 lies past
	// its end and means the end, which is position 0.
	rec = do(t, h, http.MethodPatch, todoPath(b.ID), `{"category_id":3,"position":1}`)
	wantJSON(t, rec, http.StatusOK)
	wantPositionFields(t, rec.Body.Bytes())
	if got := decodeBody[todoJSON](t, rec); got.ID != b.ID || got.CategoryID != 3 || got.Position != 0 {
		t.Errorf("after moving B to column 3: %v, want todo %d in category 3 at position 0", got, b.ID)
	}
	wantColumn(t, h, 1, "A", "C")
	wantColumn(t, h, 3, "B")

	// The category alone, as in step 1, puts the todo on top of its new column.
	rec = do(t, h, http.MethodPatch, todoPath(a.ID), `{"category_id":3}`)
	wantJSON(t, rec, http.StatusOK)
	if got := decodeBody[todoJSON](t, rec); got.CategoryID != 3 || got.Position != 0 {
		t.Errorf("after moving A to column 3: %v, want category 3 at position 0", got)
	}
	wantColumn(t, h, 1, "C")
	wantColumn(t, h, 3, "A", "B")

	// Every todo of the list has a position.
	rec = do(t, h, http.MethodGet, "/api/todos", "")
	wantJSON(t, rec, http.StatusOK)
	wantPositionFields(t, rec.Body.Bytes())
}

func TestPositionValidation(t *testing.T) {
	h, _ := newTestAPI(t)
	created := mustCreate(t, h, `{"title":"A"}`)
	path := todoPath(created.ID)

	tests := []struct {
		name, body, want string
	}{
		{"null", `{"position":null}`, "position must not be null"},
		{"negative", `{"position":-1}`, "position must be a non-negative integer"},
		{"fraction", `{"position":1.5}`, "position must be a non-negative integer"},
		{"string", `{"position":"1"}`, "position must be a non-negative integer"},

		// Beyond the cases of the brief: the position shares the checks of the
		// other fields, and does not get around them.
		{"boolean", `{"position":true}`, "position must be a non-negative integer"},
		{"array", `{"position":[0]}`, "position must be a non-negative integer"},
		{"too large for an integer", `{"position":99999999999999999999}`, "position must be a non-negative integer"},
		{"negative next to a category", `{"category_id":2,"position":-1}`, "position must be a non-negative integer"},
		{"valid position, unknown category", `{"category_id":99,"position":0}`, "category does not exist"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPatch, path, tt.body)

			wantError(t, rec, http.StatusBadRequest, tt.want)
		})
	}

	// A new todo always goes to the top, so POST has no position to take.
	rec := do(t, h, http.MethodPost, "/api/todos", `{"title":"x","position":0}`)
	wantError(t, rec, http.StatusBadRequest, `unknown field "position"`)

	// A todo that does not exist is still a 404, whatever the position is.
	wantError(t, do(t, h, http.MethodPatch, "/api/todos/999", `{"position":0}`), http.StatusNotFound, "todo not found")

	// None of the rejected requests changed or stored anything.
	rec = do(t, h, http.MethodGet, path, "")
	if got := decodeBody[todoJSON](t, rec); !reflect.DeepEqual(got, created) {
		t.Errorf("after the rejected requests the todo is %v, want it unchanged: %v", got, created)
	}
	wantColumn(t, h, 1, "A")
	wantColumn(t, h, 2)
	wantColumn(t, h, 3)
}

func TestDeleteTodo(t *testing.T) {
	h, _ := newTestAPI(t)
	doomed := mustCreate(t, h, `{"title":"Sil beni"}`)
	kept := mustCreate(t, h, `{"title":"Kalsın"}`)
	path := todoPath(doomed.ID)

	rec := do(t, h, http.MethodDelete, path, "")
	if rec.Code != http.StatusNoContent {
		t.Errorf("DELETE status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("DELETE body = %q, want no body", rec.Body.String())
	}

	wantError(t, do(t, h, http.MethodGet, path, ""), http.StatusNotFound, "todo not found")
	wantError(t, do(t, h, http.MethodDelete, path, ""), http.StatusNotFound, "todo not found")
	for _, id := range []string{"abc", "0", "-1"} {
		wantError(t, do(t, h, http.MethodDelete, "/api/todos/"+id, ""), http.StatusBadRequest, "invalid id")
	}

	// Only the deleted todo is gone.
	list := decodeBody[[]todoJSON](t, do(t, h, http.MethodGet, "/api/todos", ""))
	if len(list) != 1 || list[0].ID != kept.ID {
		t.Errorf("GET /api/todos = %v, want only the todo that was kept: %v", list, kept)
	}
}

// todoPath returns the path of the todo with the given id.
func todoPath(id int64) string {
	return "/api/todos/" + strconv.FormatInt(id, 10)
}
