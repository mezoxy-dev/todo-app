package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"todoapp/internal/applog"
	"todoapp/internal/db"
)

const (
	// maxTitleRunes and maxDescriptionRunes are the longest title and
	// description, counted in characters, not bytes.
	maxTitleRunes       = 500
	maxDescriptionRunes = 2000
)

const msgInvalidID = "invalid id"

var (
	errTitleRequired = errors.New("title is required")
	errInvalidStatus = errors.New("status must be one of todo, in_progress, done")
)

// statusLabels are the names of the statuses in the log, as the UI shows them.
var statusLabels = map[string]string{
	db.StatusTodo:       "Yapılacak",
	db.StatusInProgress: "Devam ediyor",
	db.StatusDone:       "Tamamlandı",
}

// createInput is the request body of POST /api/todos, and the fields that the
// body of PATCH /api/todos/{id} has in common with it. Its fields stay raw JSON,
// so that a field that was not sent (nil), one that was sent as null (the text
// "null") and one of the wrong type can be told apart and answered with the
// right message. There is no category_id and no position: a new todo always goes
// to the top of the inbox and the AI chooses its category, so for POST they are
// unknown fields.
type createInput struct {
	Title       json.RawMessage `json:"title"`
	Description json.RawMessage `json:"description"`
	Status      json.RawMessage `json:"status"`
	Metadata    json.RawMessage `json:"metadata"`
}

// updateInput is the request body of PATCH /api/todos/{id}: the fields of
// createInput, the category the todo moves to and its position in its column.
type updateInput struct {
	createInput
	CategoryID json.RawMessage `json:"category_id"`
	Position   json.RawMessage `json:"position"`
}

// params validates the fields that were sent and returns them. A field that
// was not sent stays nil, which is what db.UpdateTodoParams means by "leave it
// alone"; POST uses the same type and then needs the title to be set. The text
// of the error is meant for the client.
func (in createInput) params() (db.UpdateTodoParams, error) {
	var p db.UpdateTodoParams
	if in.Title != nil {
		title, err := parseTitle(in.Title)
		if err != nil {
			return p, err
		}
		p.Title = &title
	}
	if in.Description != nil {
		description, err := parseDescription(in.Description)
		if err != nil {
			return p, err
		}
		p.Description = &description
	}
	if in.Status != nil {
		status, err := parseStatus(in.Status)
		if err != nil {
			return p, err
		}
		p.Status = &status
	}
	if in.Metadata != nil {
		metadata, err := parseMetadata(in.Metadata)
		if err != nil {
			return p, err
		}
		p.Metadata = metadata
	}
	return p, nil
}

// params is createInput.params and, if they were sent, the category and the
// position. The text of the error is meant for the client.
func (in updateInput) params() (db.UpdateTodoParams, error) {
	p, err := in.createInput.params()
	if err != nil {
		return p, err
	}
	if in.CategoryID != nil {
		id, err := parseCategoryID(in.CategoryID)
		if err != nil {
			return p, err
		}
		p.CategoryID = &id
	}
	if in.Position != nil {
		position, err := parsePosition(in.Position)
		if err != nil {
			return p, err
		}
		p.Position = &position
	}
	return p, nil
}

// isNull reports whether raw is the JSON value null.
func isNull(raw json.RawMessage) bool {
	return string(raw) == "null"
}

// parseTitle returns the trimmed title. It must be a string of 1 to
// maxTitleRunes characters after trimming.
func parseTitle(raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "", errors.New("title must not be null")
	}
	var title string
	if err := json.Unmarshal(raw, &title); err != nil {
		return "", errors.New("title must be a string")
	}
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		return "", errTitleRequired
	case utf8.RuneCountInString(title) > maxTitleRunes:
		return "", errors.New("title must be at most 500 characters")
	}
	return title, nil
}

// parseDescription returns the trimmed description. It must be a string of at
// most maxDescriptionRunes characters after trimming; an empty one is fine, and
// clears the description of a todo.
func parseDescription(raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "", errors.New("description must not be null")
	}
	var description string
	if err := json.Unmarshal(raw, &description); err != nil {
		return "", errors.New("description must be a string")
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > maxDescriptionRunes {
		return "", errors.New("description must be at most 2000 characters")
	}
	return description, nil
}

// parseStatus returns the status, which must be one of the three there are.
func parseStatus(raw json.RawMessage) (string, error) {
	if isNull(raw) {
		return "", errors.New("status must not be null")
	}
	var status string
	if err := json.Unmarshal(raw, &status); err != nil {
		return "", errInvalidStatus
	}
	switch status {
	case db.StatusTodo, db.StatusInProgress, db.StatusDone:
		return status, nil
	}
	return "", errInvalidStatus
}

// parseCategoryID returns the category id, which must be a JSON integer. That
// the category exists is up to the store.
func parseCategoryID(raw json.RawMessage) (int64, error) {
	if isNull(raw) {
		return 0, errors.New("category_id must not be null")
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0, errors.New("category_id must be an integer")
	}
	return id, nil
}

// parsePosition returns the position in the column, which must be a JSON
// integer of at least 0. A position past the end of the column is fine: the
// store takes it to mean the end.
func parsePosition(raw json.RawMessage) (int64, error) {
	if isNull(raw) {
		return 0, errors.New("position must not be null")
	}
	var position int64
	if err := json.Unmarshal(raw, &position); err != nil || position < 0 {
		return 0, errors.New("position must be a non-negative integer")
	}
	return position, nil
}

// parseMetadata returns the metadata in compact form. It must be a JSON object.
func parseMetadata(raw json.RawMessage) (json.RawMessage, error) {
	if isNull(raw) {
		return nil, errors.New("metadata must not be null")
	}
	// The body is known to be valid JSON and a decoded RawMessage starts at its
	// first byte, so that byte tells whether the value is an object. (Decoding
	// into a map instead would turn down an object that holds a number too big
	// for a float64.)
	var compact bytes.Buffer
	if !bytes.HasPrefix(raw, []byte("{")) || json.Compact(&compact, raw) != nil {
		return nil, errors.New("metadata must be a JSON object")
	}
	return compact.Bytes(), nil
}

// parseID reads the {id} of the path. ok is false unless it is a positive integer.
func parseID(r *http.Request) (id int64, ok bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

// createTodo stores the todo at the top of the inbox and answers at once; the
// AI is asked later, by the worker. The log line comes before the todo is
// queued, so that the lines of the worker about it follow it.
func (s *server) createTodo(w http.ResponseWriter, r *http.Request) {
	var in createInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Title == nil {
		writeError(w, http.StatusBadRequest, errTitleRequired.Error())
		return
	}
	p, err := in.params()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	create := db.CreateTodoParams{Title: *p.Title, Metadata: p.Metadata}
	if p.Description != nil {
		create.Description = *p.Description
	}
	if p.Status != nil {
		create.Status = *p.Status
	}
	todo, err := s.store.CreateTodo(r.Context(), create)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.logs.Add(applog.LevelInfo, applog.SourceAPI, "Todo eklendi: "+applog.Quote(todo.Title), "")
	s.queue.Enqueue(todo.ID)
	writeJSON(w, http.StatusCreated, todo)
}

func (s *server) getTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, msgInvalidID)
		return
	}
	todo, err := s.store.GetTodo(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, todo)
}

// updateTodo changes the fields that the body names; moving a todo to another
// column is a change of category_id, and position puts it at a place in its
// column. Each thing that changed gets a line in the log.
func (s *server) updateTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, msgInvalidID)
		return
	}
	var in updateInput
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p, err := in.params()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// The store would still bump updated_at for an update without fields.
	if p.Title == nil && p.Description == nil && p.Status == nil && p.CategoryID == nil && p.Position == nil && p.Metadata == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	// The todo as it was tells which of the fields really changed. The log is
	// all it is for, so a todo that changes in between only makes a line less
	// exact.
	before, err := s.store.GetTodo(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	todo, err := s.store.UpdateTodo(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.logChanges(r.Context(), before, todo, p)
	writeJSON(w, http.StatusOK, todo)
}

// logChanges writes one line to the log for each change that the update made, in
// this order: the title, the description, the status, and the move or the new
// place in the column (step 3 spec 8). A field that was sent with the value it
// had already changed nothing and gets no line, and neither does the metadata.
func (s *server) logChanges(ctx context.Context, before, after db.Todo, p db.UpdateTodoParams) {
	add := func(message string) { s.logs.Add(applog.LevelInfo, applog.SourceAPI, message, "") }
	title := applog.Quote(after.Title)

	if p.Title != nil && after.Title != before.Title {
		add("Başlık değişti: " + applog.Quote(before.Title) + " → " + title)
	}
	if p.Description != nil && after.Description != before.Description {
		add("Açıklama güncellendi: " + title)
	}
	if p.Status != nil && after.Status != before.Status {
		add("Durum değişti: " + title + " → " + statusLabels[after.Status])
	}
	switch {
	case p.CategoryID != nil && after.CategoryID != before.CategoryID:
		add("Taşındı: " + title + " → " + s.categoryName(ctx, after.CategoryID))
	case p.Position != nil && after.Position != before.Position:
		add("Sıra değişti: " + title)
	}
}

// categoryName returns the name of the category for a line of the log. If the
// categories cannot be read, the id stands in for the name.
func (s *server) categoryName(ctx context.Context, id int64) string {
	if categories, err := s.store.ListCategories(ctx); err == nil {
		for _, c := range categories {
			if c.ID == id {
				return c.Name
			}
		}
	}
	return "kategori " + strconv.FormatInt(id, 10)
}

// classifyTodo sends a todo back to the AI ("Tekrar dene"): the store puts it on
// top of the inbox, pending, without what the AI said before, and the worker
// is asked to take it. The answer is 202, because the classification itself
// happens later.
func (s *server) classifyTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, msgInvalidID)
		return
	}
	todo, err := s.store.RequeueClassification(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.logs.Add(applog.LevelInfo, applog.SourceAPI, "Yeniden sınıflandırmaya gönderildi: "+applog.Quote(todo.Title), "")
	s.queue.Enqueue(todo.ID)
	writeJSON(w, http.StatusAccepted, todo)
}

func (s *server) deleteTodo(w http.ResponseWriter, r *http.Request) {
	id, ok := parseID(r)
	if !ok {
		writeError(w, http.StatusBadRequest, msgInvalidID)
		return
	}
	if err := s.store.DeleteTodo(r.Context(), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
