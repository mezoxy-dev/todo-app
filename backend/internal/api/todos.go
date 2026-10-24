package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"todoapp/internal/db"
)

// maxTitleRunes is the longest title, counted in characters, not bytes.
const maxTitleRunes = 500

const msgInvalidID = "invalid id"

var errTitleRequired = errors.New("title is required")

// todoInput is the request body of POST /api/todos, and the fields that the body
// of PATCH /api/todos/{id} has in common with it. Its fields stay raw JSON, so
// that a field that was not sent (nil), one that was sent as null (the text
// "null") and one of the wrong type can be told apart and answered with the
// right message.
type todoInput struct {
	Title      json.RawMessage `json:"title"`
	CategoryID json.RawMessage `json:"category_id"`
	Metadata   json.RawMessage `json:"metadata"`
}

// updateInput is the request body of PATCH /api/todos/{id}: the fields of
// todoInput and the todo's position in its column. POST is decoded into a
// todoInput, which has no position: a new todo always goes to the top of its
// column, so for POST the position is an unknown field.
type updateInput struct {
	todoInput
	Position json.RawMessage `json:"position"`
}

// params validates the fields that were sent and returns them. A field that
// was not sent stays nil, which is what db.UpdateTodoParams means by "leave it
// alone"; POST uses the same type and then needs the title to be set. The text
// of the error is meant for the client.
func (in todoInput) params() (db.UpdateTodoParams, error) {
	var p db.UpdateTodoParams
	if in.Title != nil {
		title, err := parseTitle(in.Title)
		if err != nil {
			return p, err
		}
		p.Title = &title
	}
	if in.CategoryID != nil {
		id, err := parseCategoryID(in.CategoryID)
		if err != nil {
			return p, err
		}
		p.CategoryID = &id
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

// params is todoInput.params and, if a position was sent, the position. The
// text of the error is meant for the client.
func (in updateInput) params() (db.UpdateTodoParams, error) {
	p, err := in.todoInput.params()
	if err != nil {
		return p, err
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

func (s *server) createTodo(w http.ResponseWriter, r *http.Request) {
	var in todoInput
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

	todo, err := s.store.CreateTodo(r.Context(), db.CreateTodoParams{
		Title:      *p.Title,
		CategoryID: p.CategoryID,
		Metadata:   p.Metadata,
	})
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
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
// column.
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
	if p.Title == nil && p.CategoryID == nil && p.Position == nil && p.Metadata == nil {
		writeError(w, http.StatusBadRequest, "no fields to update")
		return
	}

	todo, err := s.store.UpdateTodo(r.Context(), id, p)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, todo)
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
