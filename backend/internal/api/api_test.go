package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"todoapp/internal/applog"
	"todoapp/internal/db"
)

const jsonContentType = "application/json; charset=utf-8"

// The ids migration 0003 gives the categories in a fresh database (the db tests
// have the same list). TestCategoriesHaveKeys checks them against the API.
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

func TestMain(m *testing.M) {
	// Every request is logged. Keep that out of the test output; the tests that
	// look at the logs install their own handler with captureLogs.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// fakeQueue is the Enqueuer of the tests. It remembers which todos were queued
// and how much of the log there was at that moment, to show that a todo is
// logged before it is queued: the worker's first entry about it must not come
// before the entry of the request.
type fakeQueue struct {
	logs *applog.Buffer
	mu   sync.Mutex
	got  []enqueued
}

type enqueued struct {
	id     int64
	logSeq int64 // Seq of the newest log entry when the todo was queued
}

func (q *fakeQueue) Enqueue(id int64) {
	_, lastSeq, _ := q.logs.After(0, 1)
	q.mu.Lock()
	defer q.mu.Unlock()
	q.got = append(q.got, enqueued{id: id, logSeq: lastSeq})
}

func (q *fakeQueue) all() []enqueued {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.got)
}

// ids returns the ids that were queued, in order.
func (q *fakeQueue) ids() []int64 {
	ids := []int64{}
	for _, e := range q.all() {
		ids = append(ids, e.id)
	}
	return ids
}

// testEnv is the API on top of a new database in a temp directory, with the log
// buffer and the queue it reports to.
type testEnv struct {
	h     http.Handler
	store *db.Store
	logs  *applog.Buffer
	queue *fakeQueue
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("store.Close: %v", err)
		}
	})
	logs := applog.New(500)
	queue := &fakeQueue{logs: logs}
	return &testEnv{h: New(store, logs, queue), store: store, logs: logs, queue: queue}
}

// newTestAPI returns the API handler on top of a new database in a temp
// directory, and the store behind it.
func newTestAPI(t *testing.T) (http.Handler, *db.Store) {
	t.Helper()
	env := newTestEnv(t)
	return env.h, env.store
}

// mark returns the Seq of the newest log entry, to look later at what came after.
func (e *testEnv) mark() int64 {
	_, lastSeq, _ := e.logs.After(0, 1)
	return lastSeq
}

// since returns the log entries added after mark.
func (e *testEnv) since(mark int64) []applog.Entry {
	entries, _, _ := e.logs.After(mark, 500)
	return entries
}

// messagesSince returns the messages of the log entries added after mark; every
// entry the API makes is an info entry from "api", which is checked here.
func (e *testEnv) messagesSince(t *testing.T, mark int64) []string {
	t.Helper()
	messages := []string{}
	for _, entry := range e.since(mark) {
		if entry.Level != applog.LevelInfo || entry.Source != applog.SourceAPI || entry.Details != "" {
			t.Errorf("entry %q: level %s, source %s, details %q; want an info entry from api without details",
				entry.Message, entry.Level, entry.Source, entry.Details)
		}
		messages = append(messages, entry.Message)
	}
	return messages
}

// do sends a request straight to h. An empty body means no body at all.
func do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = strings.NewReader(body)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, path, r))
	return rec
}

// captureLogs sends the default logger to the returned buffer, as JSON lines,
// until the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return &buf
}

// wantJSON checks the status and the content type of a JSON response.
func wantJSON(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %q)", rec.Code, status, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != jsonContentType {
		t.Errorf("Content-Type = %q, want %q", got, jsonContentType)
	}
}

// wantError checks that rec is the error response {"error": msg} with the given status.
func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, msg string) {
	t.Helper()
	wantJSON(t, rec, status)
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Errorf("error body %q is not a JSON object of strings: %v", rec.Body.String(), err)
		return
	}
	if len(body) != 1 || body["error"] != msg {
		t.Errorf("error body = %q, want {\"error\":%q}", rec.Body.String(), msg)
	}
}

// decodeBody decodes the JSON response body into a T.
func decodeBody[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("response body %q does not decode into %T: %v", rec.Body.String(), v, err)
	}
	return v
}

// categoryJSON mirrors the Category shape of step 3 spec 7. The tests keep their
// own copy of the wire format, so renaming a json tag in the db package cannot
// go unnoticed.
type categoryJSON struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"`
	Name     string `json:"name"`
	Position int64  `json:"position"`
}

func TestHealth(t *testing.T) {
	h, _ := newTestAPI(t)

	rec := do(t, h, http.MethodGet, "/api/health", "")

	wantJSON(t, rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Errorf("body = %q, want %q", got, `{"status":"ok"}`)
	}
}

// Step 3 spec 7: the nine categories with their keys, in column order. The
// description is for the AI and is not part of the API.
func TestCategoriesHaveKeys(t *testing.T) {
	h, _ := newTestAPI(t)

	rec := do(t, h, http.MethodGet, "/api/categories", "")

	wantJSON(t, rec, http.StatusOK)
	want := []categoryJSON{
		{ID: catInbox, Key: "inbox", Name: "Gelen kutusu", Position: 0},
		{ID: catPurchase, Key: "purchase", Name: "Satın alım", Position: 1},
		{ID: catResearch, Key: "research", Name: "Araştırma", Position: 2},
		{ID: catCommunication, Key: "communication", Name: "İletişim", Position: 3},
		{ID: catPayment, Key: "payment", Name: "Ödeme", Position: 4},
		{ID: catAppointment, Key: "appointment", Name: "Randevu & Etkinlik", Position: 5},
		{ID: catWork, Key: "work", Name: "İş", Position: 6},
		{ID: catHome, Key: "home", Name: "Ev & Kişisel", Position: 7},
		{ID: catOther, Key: "other", Name: "Diğer", Position: 8},
	}
	if got := decodeBody[[]categoryJSON](t, rec); !slices.Equal(got, want) {
		t.Errorf("categories = %+v, want %+v", got, want)
	}

	// Each category has these four fields and no others.
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("body is not a list of objects: %v", err)
	}
	for _, category := range raw {
		keys := []string{}
		for k := range category {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		if !slices.Equal(keys, []string{"id", "key", "name", "position"}) {
			t.Errorf("category fields = %v, want id, key, name and position", keys)
		}
	}
}

func TestListTodosEmptyIsArray(t *testing.T) {
	h, _ := newTestAPI(t)

	rec := do(t, h, http.MethodGet, "/api/todos", "")

	wantJSON(t, rec, http.StatusOK)
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("body = %q, want %q (an empty list must not be null)", got, "[]")
	}
}

// Spec 6.4: every request is logged with method, path, status and duration.
// The unknown path shows that the status of a response the mux writes itself
// is captured too, not just a default of 200.
func TestRequestLog(t *testing.T) {
	h, _ := newTestAPI(t)
	logs := captureLogs(t)

	do(t, h, http.MethodGet, "/api/health", "")
	do(t, h, http.MethodGet, "/api/nope", "")

	var got []string
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry struct {
			Level    string `json:"level"`
			Msg      string `json:"msg"`
			Method   string `json:"method"`
			Path     string `json:"path"`
			Status   int    `json:"status"`
			Duration *int64 `json:"duration"`
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q is not JSON: %v", line, err)
		}
		if entry.Level != "INFO" || entry.Msg != "request" || entry.Duration == nil {
			t.Errorf("log line %q: want an INFO \"request\" record with a duration", line)
		}
		got = append(got, fmt.Sprintf("%s %s %d", entry.Method, entry.Path, entry.Status))
	}
	want := []string{"GET /api/health 200", "GET /api/nope 404"}
	if !slices.Equal(got, want) {
		t.Errorf("logged requests = %q, want %q", got, want)
	}
}

// Spec 6.3: an unexpected error is a 500 with a fixed message. The cause is
// logged and never sent to the client. A closed store makes every query fail.
func TestInternalErrorIsLoggedNotLeaked(t *testing.T) {
	h, store := newTestAPI(t)
	logs := captureLogs(t)
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	tests := []struct {
		name, method, path, body string
	}{
		{"list categories", http.MethodGet, "/api/categories", ""},
		{"list todos", http.MethodGet, "/api/todos", ""},
		{"create todo", http.MethodPost, "/api/todos", `{"title":"x"}`},
		{"get todo", http.MethodGet, "/api/todos/1", ""},
		{"patch todo", http.MethodPatch, "/api/todos/1", `{"title":"x"}`},
		{"delete todo", http.MethodDelete, "/api/todos/1", ""},
		{"classify todo", http.MethodPost, "/api/todos/1/classify", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs.Reset()

			rec := do(t, h, tt.method, tt.path, tt.body)

			wantError(t, rec, http.StatusInternalServerError, "internal server error")
			if got := logs.String(); !strings.Contains(got, `"level":"ERROR"`) || !strings.Contains(got, "database is closed") {
				t.Errorf("the cause was not logged as an error; log: %s", got)
			}
		})
	}
}
