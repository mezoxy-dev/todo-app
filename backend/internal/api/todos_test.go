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

	"todoapp/internal/db"
)

// todoJSON mirrors the Todo shape of step 3 spec 7, with the timestamps as plain
// text so that their format can be checked.
type todoJSON struct {
	ID             int64           `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	CategoryID     int64           `json:"category_id"`
	Position       int64           `json:"position"`
	Status         string          `json:"status"`
	Classification string          `json:"classification"`
	Metadata       json.RawMessage `json:"metadata"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
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

// mustPatch sends body to PATCH /api/todos/{id}, expects 200, and returns the todo.
func mustPatch(t *testing.T, h http.Handler, id int64, body string) todoJSON {
	t.Helper()
	rec := do(t, h, http.MethodPatch, todoPath(id), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH %s %s: status = %d, want 200 (body %q)", todoPath(id), body, rec.Code, rec.Body.String())
	}
	return decodeBody[todoJSON](t, rec)
}

// createIn creates a todo and moves it to the top of the category. A todo is
// always created in the inbox, so this is the way to get one into another
// column; it becomes a manual one.
func createIn(t *testing.T, h http.Handler, title string, categoryID int64) todoJSON {
	t.Helper()
	todo := mustCreate(t, h, `{"title":"`+title+`"}`)
	if categoryID == catInbox {
		return todo
	}
	return mustPatch(t, h, todo.ID, `{"category_id":`+strconv.FormatInt(categoryID, 10)+`}`)
}

// bodyOfSize returns a request body of exactly n bytes: a title made of "a".
func bodyOfSize(n int) string {
	const prefix, suffix = `{"title":"`, `"}`
	return prefix + strings.Repeat("a", n-len(prefix)-len(suffix)) + suffix
}

func TestCreateTodo(t *testing.T) {
	h, _ := newTestAPI(t)

	// Only a title: the top of the inbox, to do, waiting for the AI, without a
	// description and with an empty metadata object.
	rec := do(t, h, http.MethodPost, "/api/todos", `{"title":"  Süt al  "}`)
	wantJSON(t, rec, http.StatusCreated)
	first := decodeBody[todoJSON](t, rec)
	if first.ID <= 0 {
		t.Errorf("id = %d, want a positive id", first.ID)
	}
	if first.Title != "Süt al" {
		t.Errorf("title = %q, want %q (trimmed)", first.Title, "Süt al")
	}
	if first.CategoryID != catInbox || first.Position != 0 {
		t.Errorf("category_id = %d, position = %d; want the top of the inbox (%d, 0)", first.CategoryID, first.Position, catInbox)
	}
	if first.Status != "todo" || first.Classification != "pending" || first.Description != "" {
		t.Errorf("status %q, classification %q, description %q; want todo, pending and none", first.Status, first.Classification, first.Description)
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

	// With metadata. It goes to the top of the inbox as well, and the first one moves down.
	rec = do(t, h, http.MethodPost, "/api/todos", `{"title":"Fatura öde","metadata":{"a":1}}`)
	wantJSON(t, rec, http.StatusCreated)
	second := decodeBody[todoJSON](t, rec)
	if second.Title != "Fatura öde" || second.CategoryID != catInbox || second.Position != 0 {
		t.Errorf("second todo = %v, want title %q at the top of the inbox", second, "Fatura öde")
	}
	if string(second.Metadata) != `{"a":1}` {
		t.Errorf("metadata = %s, want %s", second.Metadata, `{"a":1}`)
	}

	rec = do(t, h, http.MethodGet, "/api/todos", "")
	wantJSON(t, rec, http.StatusOK)
	list := decodeBody[[]todoJSON](t, rec)
	if len(list) != 2 || list[0].Title != "Fatura öde" || list[1].Title != "Süt al" {
		t.Errorf("GET /api/todos = %v, want [Fatura öde, Süt al]", list)
	}
	if len(list) == 2 && (list[0].Position != 0 || list[1].Position != 1) {
		t.Errorf("GET /api/todos = %v, want the positions 0 and 1", list)
	}
}

// Step 3 spec 7: a todo is created in the inbox and queued for the AI at once;
// the answer does not wait for it.
func TestCreateTodoStep3(t *testing.T) {
	env := newTestEnv(t)
	mark := env.mark()

	rec := do(t, env.h, http.MethodPost, "/api/todos", `{"title":"Süt al","description":"  2 litre  ","status":"done"}`)

	wantJSON(t, rec, http.StatusCreated)
	todo := decodeBody[todoJSON](t, rec)
	if todo.CategoryID != catInbox || todo.Position != 0 || todo.Classification != "pending" {
		t.Errorf("todo = %v, want the top of the inbox, pending", todo)
	}
	if todo.Description != "2 litre" || todo.Status != "done" {
		t.Errorf("description %q, status %q; want %q (trimmed) and done", todo.Description, todo.Status, "2 litre")
	}
	if got := env.queue.ids(); !slices.Equal(got, []int64{todo.ID}) {
		t.Errorf("queued %v, want only the new todo %d", got, todo.ID)
	}
	if got, want := env.messagesSince(t, mark), []string{"Todo eklendi: “Süt al”"}; !slices.Equal(got, want) {
		t.Errorf("log = %q, want %q", got, want)
	}
	// The entry of the request comes before the todo reaches the worker, so the
	// worker's own entries about it cannot come first.
	entry := env.since(mark)[0]
	if queued := env.queue.all(); len(queued) != 1 || queued[0].logSeq < entry.Seq {
		t.Errorf("todo was queued when the log ended at %v, want it after the entry %d", queued, entry.Seq)
	}
	stored, err := env.store.GetTodo(t.Context(), todo.ID)
	if err != nil || stored.Description != "2 litre" || stored.Status != db.StatusDone {
		t.Errorf("stored todo = %+v, %v; want what the response said", stored, err)
	}

	// Without the optional fields it is a to-do without a description.
	plain := mustCreate(t, env.h, `{"title":"Ekmek al"}`)
	if plain.Status != "todo" || plain.Description != "" {
		t.Errorf("plain todo: status %q, description %q; want todo and none", plain.Status, plain.Description)
	}

	// Every accepted status.
	for _, status := range []string{"todo", "in_progress", "done"} {
		if got := mustCreate(t, env.h, `{"title":"x","status":"`+status+`"}`); got.Status != status {
			t.Errorf("created with status %q, got %q", status, got.Status)
		}
	}

	// A rejected request stores nothing, queues nothing and logs nothing.
	queuedBefore, markBefore := len(env.queue.ids()), env.mark()
	for _, tt := range []struct{ name, body, want string }{
		{"category_id", `{"title":"x","category_id":2}`, `unknown field "category_id"`},
		{"position", `{"title":"x","position":0}`, `unknown field "position"`},
		{"unknown status", `{"title":"x","status":"bitti"}`, "status must be one of todo, in_progress, done"},
		{"description over 2000", `{"title":"x","description":"` + strings.Repeat("a", 2001) + `"}`, "description must be at most 2000 characters"},
		{"null description", `{"title":"x","description":null}`, "description must not be null"},
		{"numeric description", `{"title":"x","description":5}`, "description must be a string"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			wantError(t, do(t, env.h, http.MethodPost, "/api/todos", tt.body), http.StatusBadRequest, tt.want)
		})
	}
	if got := len(env.queue.ids()); got != queuedBefore {
		t.Errorf("%d todos queued by rejected requests", got-queuedBefore)
	}
	if got := env.since(markBefore); len(got) != 0 {
		t.Errorf("rejected requests logged %+v", got)
	}
}

// The description is trimmed, at most 2000 characters (not bytes), and may be
// empty; the status is one of three.
func TestCreateTodoDescriptionAndStatus(t *testing.T) {
	h, _ := newTestAPI(t)
	long := strings.Repeat("ğ", 2000) // 4000 bytes

	tests := []struct {
		name, body, wantDescription string
	}{
		{"empty", `{"title":"x","description":""}`, ""},
		{"only white space", `{"title":"x","description":"  \n\t "}`, ""},
		{"trimmed", `{"title":"x","description":"\n  Açıklama\t "}`, "Açıklama"},
		{"inner line breaks stay", `{"title":"x","description":"satır 1\nsatır 2"}`, "satır 1\nsatır 2"},
		{"2000 characters of 2 bytes", `{"title":"x","description":"` + long + `"}`, long},
		{"2000 characters between white space", `{"title":"x","description":"  ` + long + `  "}`, long},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/todos", tt.body)

			wantJSON(t, rec, http.StatusCreated)
			if got := decodeBody[todoJSON](t, rec).Description; got != tt.wantDescription {
				t.Errorf("description = %q, want %q", got, tt.wantDescription)
			}
		})
	}

	wantError(t, do(t, h, http.MethodPost, "/api/todos", `{"title":"x","description":"`+long+`ğ"}`),
		http.StatusBadRequest, "description must be at most 2000 characters")
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
		// Since step 3 the AI chooses the category: it cannot be sent.
		{"category_id", `{"title":"x","category_id":99}`, `unknown field "category_id"`},
		{"string category_id", `{"title":"x","category_id":"3"}`, `unknown field "category_id"`},
		{"null category_id", `{"title":"x","category_id":null}`, `unknown field "category_id"`},
		{"array metadata", `{"title":"x","metadata":[1]}`, "metadata must be a JSON object"},
		{"string metadata", `{"title":"x","metadata":"str"}`, "metadata must be a JSON object"},
		{"unknown field", `{"title":"x","extra":1}`, `unknown field "extra"`},
		{"classification is not the client's", `{"title":"x","classification":"done"}`, `unknown field "classification"`},
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
		{"null metadata", `{"title":"x","metadata":null}`, "metadata must not be null"},
		{"number metadata", `{"title":"x","metadata":5}`, "metadata must be a JSON object"},

		// The new fields.
		{"boolean description", `{"title":"x","description":true}`, "description must be a string"},
		{"array description", `{"title":"x","description":["a"]}`, "description must be a string"},
		{"status not a string", `{"title":"x","status":1}`, "status must be one of todo, in_progress, done"},
		{"empty status", `{"title":"x","status":""}`, "status must be one of todo, in_progress, done"},
		{"status in another case", `{"title":"x","status":"Done"}`, "status must be one of todo, in_progress, done"},
		{"status with white space", `{"title":"x","status":" done"}`, "status must be one of todo, in_progress, done"},
		{"null status", `{"title":"x","status":null}`, "status must not be null"},
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
	created := mustCreate(t, h, `{"title":"Süt al","description":"2 litre","status":"in_progress","metadata":{"a":1}}`)

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

// Step 3 spec 7: the fields of a todo, in every response that carries one.
func TestTodoResponsesHaveAllFields(t *testing.T) {
	env := newTestEnv(t)
	wantFields := []string{"category_id", "classification", "created_at", "description", "id", "metadata", "position", "status", "title", "updated_at"}

	checkFields := func(name string, rec *httptest.ResponseRecorder) {
		t.Helper()
		raw := rec.Body.Bytes()
		var todos []map[string]json.RawMessage
		if bytes.HasPrefix(bytes.TrimSpace(raw), []byte("[")) {
			if err := json.Unmarshal(raw, &todos); err != nil {
				t.Fatalf("%s: body %q is not a list of objects: %v", name, raw, err)
			}
		} else {
			var one map[string]json.RawMessage
			if err := json.Unmarshal(raw, &one); err != nil {
				t.Fatalf("%s: body %q is not an object: %v", name, raw, err)
			}
			todos = append(todos, one)
		}
		if len(todos) == 0 {
			t.Fatalf("%s: body %q holds no todo", name, raw)
		}
		for _, todo := range todos {
			var got []string
			for field := range todo {
				got = append(got, field)
			}
			slices.Sort(got)
			if !slices.Equal(got, wantFields) {
				t.Errorf("%s: fields = %v, want %v", name, got, wantFields)
			}
		}
	}

	rec := do(t, env.h, http.MethodPost, "/api/todos", `{"title":"A"}`)
	checkFields("POST", rec)
	id := decodeBody[todoJSON](t, rec).ID
	checkFields("GET one", do(t, env.h, http.MethodGet, todoPath(id), ""))
	checkFields("GET list", do(t, env.h, http.MethodGet, "/api/todos", ""))
	checkFields("PATCH", do(t, env.h, http.MethodPatch, todoPath(id), `{"status":"done"}`))
	checkFields("classify", do(t, env.h, http.MethodPost, todoPath(id)+"/classify", ""))
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
	if renamed.Classification != "pending" {
		t.Errorf("classification after a rename = %q, want it unchanged (pending)", renamed.Classification)
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
	rec = do(t, h, http.MethodPatch, path, `{"category_id":`+strconv.FormatInt(catResearch, 10)+`}`)
	wantJSON(t, rec, http.StatusOK)
	moved := decodeBody[todoJSON](t, rec)
	if moved.CategoryID != catResearch || moved.Title != "Yeni" {
		t.Errorf("after moving: %v, want category %d and title %q", moved, catResearch, "Yeni")
	}
	rec = do(t, h, http.MethodGet, path, "")
	if got := decodeBody[todoJSON](t, rec); got.CategoryID != catResearch {
		t.Errorf("GET after the move: category_id = %d, want %d", got.CategoryID, catResearch)
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

// Step 3 spec 7 and 8: status, description and moves, each change with a line in
// the log, and only a different category makes a todo "manual".
func TestPatchTodoStep3(t *testing.T) {
	env := newTestEnv(t)
	h := env.h
	todo := mustCreate(t, h, `{"title":"Süt al"}`)
	patch := func(body string) (todoJSON, []string) {
		t.Helper()
		mark := env.mark()
		got := mustPatch(t, h, todo.ID, body)
		return got, env.messagesSince(t, mark)
	}
	wantLog := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s: log = %q, want %q", name, got, want)
		}
	}

	// Status: all three labels.
	got, logged := patch(`{"status":"in_progress"}`)
	if got.Status != "in_progress" || got.Classification != "pending" || got.CategoryID != catInbox {
		t.Errorf("status change: %v, want in_progress, still pending in the inbox", got)
	}
	wantLog("status in_progress", logged, []string{"Durum değişti: “Süt al” → Devam ediyor"})
	_, logged = patch(`{"status":"done"}`)
	wantLog("status done", logged, []string{"Durum değişti: “Süt al” → Tamamlandı"})
	_, logged = patch(`{"status":"todo"}`)
	wantLog("status todo", logged, []string{"Durum değişti: “Süt al” → Yapılacak"})

	// Description: trimmed, and cleared with an empty string.
	got, logged = patch(`{"description":"  yeni  "}`)
	if got.Description != "yeni" || got.Status != "todo" {
		t.Errorf("description change: %v, want description %q and the status kept", got, "yeni")
	}
	wantLog("description", logged, []string{"Açıklama güncellendi: “Süt al”"})
	got, logged = patch(`{"description":""}`)
	if got.Description != "" {
		t.Errorf("description after clearing it = %q", got.Description)
	}
	wantLog("clear the description", logged, []string{"Açıklama güncellendi: “Süt al”"})

	// A title.
	got, logged = patch(`{"title":"  Yeni başlık  "}`)
	if got.Title != "Yeni başlık" {
		t.Errorf("title = %q", got.Title)
	}
	wantLog("title", logged, []string{"Başlık değişti: “Süt al” → “Yeni başlık”"})
	_, logged = patch(`{"title":"Süt al"}`)
	wantLog("title back", logged, []string{"Başlık değişti: “Yeni başlık” → “Süt al”"})

	// What changed nothing is not logged: the same status, description, title...
	_, logged = patch(`{"status":"todo","description":"","title":"Süt al"}`)
	wantLog("no change", logged, []string{})
	// ...and metadata, which has no line of its own.
	got, logged = patch(`{"metadata":{"k":1}}`)
	wantLog("metadata", logged, []string{})
	if got.Classification != "pending" {
		t.Errorf("classification = %q, want pending: no category changed", got.Classification)
	}

	// A different category is a move by hand: manual, and the category is named in the log.
	got, logged = patch(`{"category_id":` + strconv.FormatInt(catResearch, 10) + `}`)
	if got.CategoryID != catResearch || got.Classification != "manual" || got.Position != 0 {
		t.Errorf("move: %v, want the top of category %d, manual", got, catResearch)
	}
	wantLog("move", logged, []string{"Taşındı: “Süt al” → Araştırma"})
	_, logged = patch(`{"category_id":` + strconv.FormatInt(catAppointment, 10) + `,"position":0}`)
	wantLog("move with a position", logged, []string{"Taşındı: “Süt al” → Randevu & Etkinlik"})
	_, logged = patch(`{"category_id":` + strconv.FormatInt(catInbox, 10) + `}`)
	wantLog("back to the inbox", logged, []string{"Taşındı: “Süt al” → Gelen kutusu"})
	if got := mustGet(t, h, todo.ID); got.Classification != "manual" {
		t.Errorf("classification after moving back = %q, want manual", got.Classification)
	}

	// The same category with a position moves within the column: no "manual", and its own line.
	second := createIn(t, h, "İkinci", catWork)
	createIn(t, h, "Birinci", catWork) // work: Birinci, İkinci
	mark := env.mark()
	got = mustPatch(t, h, second.ID, `{"category_id":`+strconv.FormatInt(catWork, 10)+`,"position":0}`)
	if got.Position != 0 || got.CategoryID != catWork {
		t.Errorf("reorder: %v, want position 0 in category %d", got, catWork)
	}
	wantLog("reorder", env.messagesSince(t, mark), []string{"Sıra değişti: “İkinci”"})
	mark = env.mark()
	mustPatch(t, h, second.ID, `{"category_id":`+strconv.FormatInt(catWork, 10)+`,"position":0}`) // where it is already
	wantLog("same place", env.messagesSince(t, mark), []string{})
	mark = env.mark()
	mustPatch(t, h, second.ID, `{"position":1}`)
	wantLog("position only", env.messagesSince(t, mark), []string{"Sıra değişti: “İkinci”"})

	// A pending todo that is only reordered in the inbox stays pending.
	a := mustCreate(t, h, `{"title":"A"}`)
	mustCreate(t, h, `{"title":"B"}`) // inbox: B, A
	got = mustPatch(t, h, a.ID, `{"category_id":`+strconv.FormatInt(catInbox, 10)+`,"position":0}`)
	if got.Classification != "pending" || got.Position != 0 {
		t.Errorf("reorder in the inbox: %v, want it still pending at position 0", got)
	}

	// Several fields at once: one line for each, in this order.
	multi := mustCreate(t, h, `{"title":"Çok"}`)
	mark = env.mark()
	got = mustPatch(t, h, multi.ID, `{"category_id":`+strconv.FormatInt(catPayment, 10)+
		`,"status":"done","description":"d","title":"Çok alanlı"}`)
	if got.Title != "Çok alanlı" || got.Description != "d" || got.Status != "done" || got.CategoryID != catPayment || got.Classification != "manual" {
		t.Errorf("several fields: %v", got)
	}
	wantLog("several fields", env.messagesSince(t, mark), []string{
		"Başlık değişti: “Çok” → “Çok alanlı”",
		"Açıklama güncellendi: “Çok alanlı”",
		"Durum değişti: “Çok alanlı” → Tamamlandı",
		"Taşındı: “Çok alanlı” → Ödeme",
	})

	// A request that is refused logs nothing and changes nothing.
	mark = env.mark()
	before := mustGet(t, h, multi.ID)
	for _, body := range []string{`{"status":"bitti"}`, `{"title":"y","status":"bitti"}`, `{"description":null}`, `{"category_id":99,"status":"todo"}`, `{}`} {
		if rec := do(t, h, http.MethodPatch, todoPath(multi.ID), body); rec.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s: status = %d, want 400", body, rec.Code)
		}
	}
	wantLog("refused requests", env.messagesSince(t, mark), []string{})
	if after := mustGet(t, h, multi.ID); !reflect.DeepEqual(after, before) {
		t.Errorf("refused requests changed the todo: %v, want %v", after, before)
	}
}

// mustGet returns the todo with that id through the API.
func mustGet(t *testing.T, h http.Handler, id int64) todoJSON {
	t.Helper()
	rec := do(t, h, http.MethodGet, todoPath(id), "")
	wantJSON(t, rec, http.StatusOK)
	return decodeBody[todoJSON](t, rec)
}

func TestPatchTodoValidation(t *testing.T) {
	h, _ := newTestAPI(t)
	created := mustCreate(t, h, `{"title":"Süt al","description":"2 litre","metadata":{"a":1}}`)
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

		// Step 3.
		{"unknown status", `{"status":"bitti"}`, "status must be one of todo, in_progress, done"},
		{"status not a string", `{"status":true}`, "status must be one of todo, in_progress, done"},
		{"empty status", `{"status":""}`, "status must be one of todo, in_progress, done"},
		{"null status", `{"status":null}`, "status must not be null"},
		{"null description", `{"description":null}`, "description must not be null"},
		{"numeric description", `{"description":5}`, "description must be a string"},
		{"description over 2000 characters", `{"description":"` + strings.Repeat("ğ", 2001) + `"}`, "description must be at most 2000 characters"},
		{"valid status, null description", `{"status":"done","description":null}`, "description must not be null"},
		{"valid description, unknown status", `{"description":"x","status":"bitti"}`, "status must be one of todo, in_progress, done"},
		{"classification is not the client's", `{"classification":"done"}`, `unknown field "classification"`},
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
	wantError(t, do(t, h, http.MethodPatch, "/api/todos/999", `{"status":"done"}`), http.StatusNotFound, "todo not found")
	for _, id := range []string{"abc", "0", "-1"} {
		wantError(t, do(t, h, http.MethodPatch, "/api/todos/"+id, `{"title":"a"}`), http.StatusBadRequest, "invalid id")
	}
}

// Step 3 spec 7: "Kartı Gelen kutusunun en üstüne alır, classification = 'pending',
// metadata.ai silinir, kuyruğa ekler -> 202 + todo; yoksa 404."
func TestClassifyEndpoint(t *testing.T) {
	env := newTestEnv(t)
	h := env.h
	todo := mustCreate(t, h, `{"title":"Süt al","metadata":{"k":1}}`)
	// The AI failed on it once: what it said is in the metadata.
	if _, applied, err := env.store.FailClassification(t.Context(), todo.ID,
		json.RawMessage(`{"model":"m","error":"kota aşıldı (HTTP 429)","at":"2026-10-25T10:00:00.000Z"}`)); err != nil || !applied {
		t.Fatalf("FailClassification = (%v, %v)", applied, err)
	}
	mustCreate(t, h, `{"title":"Üstteki"}`) // now on top of the failed one
	if failed := mustGet(t, h, todo.ID); failed.Classification != "failed" || failed.Position != 1 || !strings.Contains(string(failed.Metadata), `"ai"`) {
		t.Fatalf("setup: %v, want a failed todo at position 1 with metadata.ai", failed)
	}
	queuedBefore := env.queue.ids()
	mark := env.mark()

	rec := do(t, h, http.MethodPost, todoPath(todo.ID)+"/classify", "")

	wantJSON(t, rec, http.StatusAccepted)
	got := decodeBody[todoJSON](t, rec)
	if got.ID != todo.ID || got.CategoryID != catInbox || got.Position != 0 || got.Classification != "pending" {
		t.Errorf("response = %v, want the todo at the top of the inbox, pending", got)
	}
	var metadata map[string]json.RawMessage
	if err := json.Unmarshal(got.Metadata, &metadata); err != nil {
		t.Fatalf("metadata %s: %v", got.Metadata, err)
	}
	if _, ok := metadata["ai"]; ok || string(metadata["k"]) != "1" {
		t.Errorf("metadata = %s, want metadata.ai gone and the other keys kept", got.Metadata)
	}
	if stored := mustGet(t, h, todo.ID); !reflect.DeepEqual(stored, got) {
		t.Errorf("GET = %v, want what POST /classify returned: %v", stored, got)
	}
	wantColumn(t, h, catInbox, "Süt al", "Üstteki")

	// It was queued, after the log entry.
	if queued := env.queue.ids()[len(queuedBefore):]; !slices.Equal(queued, []int64{todo.ID}) {
		t.Errorf("queued %v, want %d", queued, todo.ID)
	}
	if want := []string{"Yeniden sınıflandırmaya gönderildi: “Süt al”"}; !slices.Equal(env.messagesSince(t, mark), want) {
		t.Errorf("log = %q, want %q", env.messagesSince(t, mark), want)
	}
	entry := env.since(mark)[0]
	if all := env.queue.all(); all[len(all)-1].logSeq < entry.Seq {
		t.Errorf("the todo was queued before the log entry %d", entry.Seq)
	}

	// A todo that sits in another category, put there by hand, comes back too.
	moved := createIn(t, h, "Taşınmış", catWork)
	if moved.Classification != "manual" {
		t.Fatalf("setup: %v, want a manual todo", moved)
	}
	rec = do(t, h, http.MethodPost, todoPath(moved.ID)+"/classify", "")
	wantJSON(t, rec, http.StatusAccepted)
	if got := decodeBody[todoJSON](t, rec); got.CategoryID != catInbox || got.Classification != "pending" || got.Position != 0 {
		t.Errorf("response = %v, want the top of the inbox, pending", got)
	}
	wantColumn(t, h, catWork)
	wantColumn(t, h, catInbox, "Taşınmış", "Süt al", "Üstteki")

	// Errors: nothing is queued and nothing is logged.
	mark, queuedBefore = env.mark(), env.queue.ids()
	wantError(t, do(t, h, http.MethodPost, "/api/todos/9999/classify", ""), http.StatusNotFound, "todo not found")
	for _, id := range []string{"abc", "0", "-1", "1.5"} {
		wantError(t, do(t, h, http.MethodPost, "/api/todos/"+id+"/classify", ""), http.StatusBadRequest, "invalid id")
	}
	if got := env.queue.ids(); !slices.Equal(got, queuedBefore) {
		t.Errorf("queued %v after the failed requests, want %v", got, queuedBefore)
	}
	if got := env.since(mark); len(got) != 0 {
		t.Errorf("the failed requests logged %+v", got)
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
	a := createIn(t, h, "A", catPurchase)
	b := createIn(t, h, "B", catPurchase)
	createIn(t, h, "C", catPurchase)
	wantColumn(t, h, catPurchase, "C", "B", "A") // a todo moved to a column goes to the top

	// The position alone is enough for a PATCH: A goes from the bottom to the top.
	rec := do(t, h, http.MethodPatch, todoPath(a.ID), `{"position":0}`)
	wantJSON(t, rec, http.StatusOK)
	wantPositionFields(t, rec.Body.Bytes())
	if got := decodeBody[todoJSON](t, rec); got.ID != a.ID || got.CategoryID != catPurchase || got.Position != 0 {
		t.Errorf("after moving A to the top: %v, want todo %d in category %d at position 0", got, a.ID, catPurchase)
	}
	wantColumn(t, h, catPurchase, "A", "C", "B")

	// Column and position together. The column is empty, so position 1 lies past
	// its end and means the end, which is position 0.
	rec = do(t, h, http.MethodPatch, todoPath(b.ID), `{"category_id":`+strconv.FormatInt(catCommunication, 10)+`,"position":1}`)
	wantJSON(t, rec, http.StatusOK)
	wantPositionFields(t, rec.Body.Bytes())
	if got := decodeBody[todoJSON](t, rec); got.ID != b.ID || got.CategoryID != catCommunication || got.Position != 0 {
		t.Errorf("after moving B to category %d: %v, want todo %d at position 0", catCommunication, got, b.ID)
	}
	wantColumn(t, h, catPurchase, "A", "C")
	wantColumn(t, h, catCommunication, "B")

	// The category alone, as in step 1, puts the todo on top of its new column.
	rec = do(t, h, http.MethodPatch, todoPath(a.ID), `{"category_id":`+strconv.FormatInt(catCommunication, 10)+`}`)
	wantJSON(t, rec, http.StatusOK)
	if got := decodeBody[todoJSON](t, rec); got.CategoryID != catCommunication || got.Position != 0 {
		t.Errorf("after moving A to category %d: %v, want position 0", catCommunication, got)
	}
	wantColumn(t, h, catPurchase, "C")
	wantColumn(t, h, catCommunication, "A", "B")

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
		{"negative next to a category", `{"category_id":` + strconv.FormatInt(catResearch, 10) + `,"position":-1}`, "position must be a non-negative integer"},
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
	wantColumn(t, h, catInbox, "A")
	wantColumn(t, h, catResearch)
	wantColumn(t, h, catCommunication)
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
