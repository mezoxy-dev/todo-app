// Package api is the HTTP layer of the todo app: the JSON endpoints under /api,
// built on the db package.
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"todoapp/internal/applog"
	"todoapp/internal/db"
)

// Enqueuer takes the ids of the todos that wait for the AI. The server hands it
// a todo as soon as it is created or sent back with "classify"; *classify.Worker
// is the one it is used with.
type Enqueuer interface {
	Enqueue(id int64)
}

// server holds what the handlers need.
type server struct {
	store *db.Store
	logs  *applog.Buffer // what happens to the todos, for the log panel (step 3 spec 8)
	queue Enqueuer
}

// New returns the handler that serves the API on top of store. Every request
// is logged to the console; the changes to the todos are also written to logs,
// and todos that need the AI are handed to queue.
func New(store *db.Store, logs *applog.Buffer, queue Enqueuer) http.Handler {
	s := &server{store: store, logs: logs, queue: queue}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", health)
	mux.HandleFunc("GET /api/categories", s.listCategories)
	mux.HandleFunc("GET /api/todos", s.listTodos)
	mux.HandleFunc("POST /api/todos", s.createTodo)
	mux.HandleFunc("GET /api/todos/{id}", s.getTodo)
	mux.HandleFunc("PATCH /api/todos/{id}", s.updateTodo)
	mux.HandleFunc("DELETE /api/todos/{id}", s.deleteTodo)
	mux.HandleFunc("POST /api/todos/{id}/classify", s.classifyTodo)
	mux.HandleFunc("GET /api/logs", s.listLogs)
	return logRequests(mux)
}

func health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *server) listCategories(w http.ResponseWriter, r *http.Request) {
	categories, err := s.store.ListCategories(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, categories)
}

func (s *server) listTodos(w http.ResponseWriter, r *http.Request) {
	todos, err := s.store.ListTodos(r.Context())
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, todos)
}

// writeStoreError answers a failed store call: the two errors of the db
// contract have their own responses, anything else is logged and becomes a 500
// that does not reveal the cause.
func writeStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "todo not found")
	case errors.Is(err, db.ErrInvalidCategory):
		writeError(w, http.StatusBadRequest, "category does not exist")
	default:
		slog.Error("request failed", "method", r.Method, "path", r.URL.Path, "error", err)
		writeError(w, http.StatusInternalServerError, msgInternal)
	}
}

// logRequests logs the method, path, status and duration of every request
// that next handles.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start),
		)
	})
}

// statusRecorder remembers the status code a handler sends. A handler that
// never calls WriteHeader sends 200.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// Unwrap lets http.ResponseController reach the underlying ResponseWriter.
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}
