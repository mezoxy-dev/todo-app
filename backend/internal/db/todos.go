package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Todo is one card on the board.
type Todo struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	CategoryID int64           `json:"category_id"`
	Metadata   json.RawMessage `json:"metadata"`   // always a valid JSON object
	CreatedAt  time.Time       `json:"created_at"` // UTC
	UpdatedAt  time.Time       `json:"updated_at"` // UTC
}

// CreateTodoParams describes a new todo.
type CreateTodoParams struct {
	Title      string          // validated by the caller (trimmed, 1-500 characters)
	CategoryID *int64          // nil: the category with the smallest position
	Metadata   json.RawMessage // nil or empty: {}
}

// UpdateTodoParams lists the fields to change; a nil field is left as it is.
// The caller makes sure that at least one field is set.
type UpdateTodoParams struct {
	Title      *string
	CategoryID *int64
	Metadata   json.RawMessage // nil or empty: unchanged; otherwise replaces the old object (no merging)
}

// todoColumns is the column list of every query that returns a todo, in the
// order scanTodo reads it.
const todoColumns = `id, title, category_id, metadata, created_at, updated_at`

const (
	todoExistsSQL     = `SELECT EXISTS (SELECT 1 FROM todos WHERE id = ?)`
	categoryExistsSQL = `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`
)

// updateTodoSQL takes title, category_id, metadata and id. A NULL parameter
// means "leave this field alone": COALESCE keeps the stored value, which is
// the same as leaving the field out of SET. None of the three columns is
// nullable, so NULL can never be a real new value. updated_at always moves.
const updateTodoSQL = `UPDATE todos SET
	title       = COALESCE(?, title),
	category_id = COALESCE(?, category_id),
	metadata    = COALESCE(?, metadata),
	updated_at  = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = ?
RETURNING ` + todoColumns

// CreateTodo inserts a todo and returns it as stored. A CategoryID that does
// not exist yields ErrInvalidCategory. Both timestamps are set by SQL.
func (s *Store) CreateTodo(ctx context.Context, p CreateTodoParams) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	var categoryID int64
	if p.CategoryID == nil {
		err := tx.QueryRowContext(ctx, `SELECT id FROM categories ORDER BY position, id LIMIT 1`).Scan(&categoryID)
		if err != nil {
			return Todo{}, fmt.Errorf("create todo: default category: %w", err)
		}
	} else {
		categoryID = *p.CategoryID
		found, err := exists(ctx, tx, categoryExistsSQL, categoryID)
		if err != nil {
			return Todo{}, fmt.Errorf("create todo: check category %d: %w", categoryID, err)
		}
		if !found {
			return Todo{}, ErrInvalidCategory
		}
	}

	metadata := "{}"
	if len(p.Metadata) > 0 {
		metadata = string(p.Metadata)
	}

	// metadata is bound as a string so that SQLite stores it as TEXT.
	todo, err := scanTodo(tx.QueryRowContext(ctx,
		`INSERT INTO todos (title, category_id, metadata) VALUES (?, ?, ?) RETURNING `+todoColumns,
		p.Title, categoryID, metadata))
	if err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	return todo, nil
}

// GetTodo returns the todo with the given id, or ErrNotFound.
func (s *Store) GetTodo(ctx context.Context, id int64) (Todo, error) {
	todo, err := scanTodo(s.db.QueryRowContext(ctx, `SELECT `+todoColumns+` FROM todos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, ErrNotFound
	}
	if err != nil {
		return Todo{}, fmt.Errorf("get todo %d: %w", id, err)
	}
	return todo, nil
}

// ListTodos returns all todos, newest first. The slice is never nil.
func (s *Store) ListTodos(ctx context.Context) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+todoColumns+` FROM todos ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	defer rows.Close()

	todos := []Todo{}
	for rows.Next() {
		todo, err := scanTodo(rows)
		if err != nil {
			return nil, fmt.Errorf("list todos: %w", err)
		}
		todos = append(todos, todo)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list todos: %w", err)
	}
	return todos, nil
}

// UpdateTodo changes the fields of the todo that are set in p and returns the
// todo as stored. It returns ErrNotFound if the todo does not exist, and, only
// after that check, ErrInvalidCategory if p.CategoryID names no category. A
// rejected update changes nothing.
func (s *Store) UpdateTodo(ctx context.Context, id int64, p UpdateTodoParams) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	found, err := exists(ctx, tx, todoExistsSQL, id)
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	if !found {
		return Todo{}, ErrNotFound
	}
	if p.CategoryID != nil {
		found, err := exists(ctx, tx, categoryExistsSQL, *p.CategoryID)
		if err != nil {
			return Todo{}, fmt.Errorf("update todo %d: check category %d: %w", id, *p.CategoryID, err)
		}
		if !found {
			return Todo{}, ErrInvalidCategory
		}
	}

	// A nil interface value is bound as NULL, i.e. "keep the stored value" (see
	// updateTodoSQL). metadata is bound as a string so that it is stored as TEXT.
	var title, categoryID, metadata any
	if p.Title != nil {
		title = *p.Title
	}
	if p.CategoryID != nil {
		categoryID = *p.CategoryID
	}
	if len(p.Metadata) > 0 {
		metadata = string(p.Metadata)
	}

	todo, err := scanTodo(tx.QueryRowContext(ctx, updateTodoSQL, title, categoryID, metadata, id))
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	return todo, nil
}

// DeleteTodo deletes the todo with the given id, or returns ErrNotFound.
func (s *Store) DeleteTodo(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM todos WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete todo %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete todo %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// exists runs a SELECT EXISTS(...) query that takes an id.
func exists(ctx context.Context, tx *sql.Tx, query string, id int64) (bool, error) {
	var found bool
	if err := tx.QueryRowContext(ctx, query, id).Scan(&found); err != nil {
		return false, err
	}
	return found, nil
}

// scanner is implemented by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// scanTodo reads one row of todoColumns. The metadata and the timestamps are
// scanned as strings, exactly as stored, and converted here.
func scanTodo(row scanner) (Todo, error) {
	var (
		todo                           Todo
		metadata, createdAt, updatedAt string
	)
	err := row.Scan(&todo.ID, &todo.Title, &todo.CategoryID, &metadata, &createdAt, &updatedAt)
	if err != nil {
		return Todo{}, err
	}
	todo.Metadata = json.RawMessage(metadata)
	if todo.CreatedAt, err = parseTimestamp(createdAt); err != nil {
		return Todo{}, fmt.Errorf("created_at: %w", err)
	}
	if todo.UpdatedAt, err = parseTimestamp(updatedAt); err != nil {
		return Todo{}, fmt.Errorf("updated_at: %w", err)
	}
	return todo, nil
}

// parseTimestamp parses a timestamp written by SQL as
// strftime('%Y-%m-%dT%H:%M:%fZ','now'), e.g. 2026-10-24T12:00:00.123Z.
func parseTimestamp(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}
