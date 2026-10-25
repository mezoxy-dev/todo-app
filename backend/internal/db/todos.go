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
	Position   int64           `json:"position"`   // place in its column, 0 = top
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
	Position   *int64          // place in the column the todo ends up in (see UpdateTodo); the caller makes sure it is >= 0
	Metadata   json.RawMessage // nil or empty: unchanged; otherwise replaces the old object (no merging)
}

// todoColumns is the column list of every query that returns a todo, in the
// order scanTodo reads it.
const todoColumns = `id, title, category_id, metadata, created_at, updated_at, position`

const categoryExistsSQL = `SELECT EXISTS (SELECT 1 FROM categories WHERE id = ?)`

// The statements that keep the positions of a column at 0..n-1 (step 2 spec
// 4.2). Each one runs in the transaction of the change that needs it. There is
// no UNIQUE constraint on (category_id, position): SQLite checks such a
// constraint row by row, so renumbering a column would run into false conflicts
// halfway.
const (
	// makeRoomOnTopSQL takes a category_id: a todo is about to be inserted at
	// position 0 of that column.
	makeRoomOnTopSQL = `UPDATE todos SET position = position + 1 WHERE category_id = ?`

	// closeGapSQL takes a category_id and a position: the todo that was at that
	// position has left the column, deleted or moved to another one.
	closeGapSQL = `UPDATE todos SET position = position - 1 WHERE category_id = ? AND position > ?`

	// makeRoomSQL takes a category_id, a position and the id of the todo that is
	// being placed there: the todos of the column from that position down move
	// down by one. The todo itself is left out, since it may still be in this
	// column at its old position.
	makeRoomSQL = `UPDATE todos SET position = position + 1 WHERE category_id = ? AND position >= ? AND id != ?`
)

// updateTodoSQL takes title, category_id, position, metadata and id. For title
// and metadata a NULL parameter means "leave this field alone": COALESCE keeps
// the stored value, which is the same as leaving the field out of SET. Neither
// column is nullable, so NULL can never be a real new value. category_id and
// position are always given: UpdateTodo has worked out where the todo ends up,
// and for a todo that stays where it is, that is the place it already has.
// updated_at always moves.
const updateTodoSQL = `UPDATE todos SET
	title       = COALESCE(?, title),
	category_id = ?,
	position    = ?,
	metadata    = COALESCE(?, metadata),
	updated_at  = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = ?
RETURNING ` + todoColumns

// CreateTodo inserts a todo at the top of its column, where the todos that were
// there move down by one, and returns it as stored. A CategoryID that does not
// exist yields ErrInvalidCategory. Both timestamps are set by SQL.
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

	if _, err := tx.ExecContext(ctx, makeRoomOnTopSQL, categoryID); err != nil {
		return Todo{}, fmt.Errorf("create todo: make room in category %d: %w", categoryID, err)
	}

	// metadata is bound as a string so that SQLite stores it as TEXT.
	todo, err := scanTodo(tx.QueryRowContext(ctx,
		`INSERT INTO todos (title, category_id, position, metadata) VALUES (?, ?, 0, ?) RETURNING `+todoColumns,
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

// ListTodos returns all todos, column by column (by category id), each column
// from top to bottom. The slice is never nil.
func (s *Store) ListTodos(ctx context.Context) ([]Todo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+todoColumns+` FROM todos ORDER BY category_id, position, id`)
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
//
// Where the todo ends up (step 2 spec 4.3): p.Position is its place in the
// column it ends up in, counted in the list of that column without the todo
// itself, so a position past the end of the list means the end. A todo that is
// moved to another column without a position goes to the top of it. With
// neither a position nor another column the todo keeps its place. The other
// todos of the columns concerned are renumbered, so that the positions of every
// column stay 0..n-1.
func (s *Store) UpdateTodo(ctx context.Context, id int64, p UpdateTodoParams) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	var fromCategoryID, fromPosition int64
	err = tx.QueryRowContext(ctx, `SELECT category_id, position FROM todos WHERE id = ?`, id).
		Scan(&fromCategoryID, &fromPosition)
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, ErrNotFound
	}
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
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

	toCategoryID, toPosition := fromCategoryID, fromPosition
	if p.CategoryID != nil {
		toCategoryID = *p.CategoryID
	}
	if p.Position != nil || toCategoryID != fromCategoryID {
		wanted := int64(0) // another column and no position: the top
		if p.Position != nil {
			wanted = *p.Position
		}
		toPosition, err = placeTodo(ctx, tx, id, fromCategoryID, fromPosition, toCategoryID, wanted)
		if err != nil {
			return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
		}
	}

	// A nil interface value is bound as NULL, i.e. "keep the stored value" (see
	// updateTodoSQL). metadata is bound as a string so that it is stored as TEXT.
	var title, metadata any
	if p.Title != nil {
		title = *p.Title
	}
	if len(p.Metadata) > 0 {
		metadata = string(p.Metadata)
	}

	todo, err := scanTodo(tx.QueryRowContext(ctx, updateTodoSQL, title, toCategoryID, toPosition, metadata, id))
	if err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("update todo %d: %w", id, err)
	}
	return todo, nil
}

// placeTodo prepares the other todos for the todo with the given id to take the
// position wanted in the column toCategoryID: the gap the todo leaves at
// (fromCategoryID, fromPosition) is closed and room is made at the new place.
// It returns the position the todo takes: wanted, or the end of the column if
// wanted lies past it. The caller writes the row of the todo itself.
func placeTodo(ctx context.Context, tx *sql.Tx, id, fromCategoryID, fromPosition, toCategoryID, wanted int64) (int64, error) {
	// wanted counts in the column without the todo itself. The end of that list
	// is its length, which is the largest place there is.
	var others int64
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM todos WHERE category_id = ? AND id != ?`, toCategoryID, id).
		Scan(&others)
	if err != nil {
		return 0, fmt.Errorf("count todos of category %d: %w", toCategoryID, err)
	}
	position := min(wanted, others)

	if toCategoryID == fromCategoryID && position == fromPosition {
		return position, nil // it stays where it is
	}
	if _, err := tx.ExecContext(ctx, closeGapSQL, fromCategoryID, fromPosition); err != nil {
		return 0, fmt.Errorf("close gap in category %d: %w", fromCategoryID, err)
	}
	if _, err := tx.ExecContext(ctx, makeRoomSQL, toCategoryID, position, id); err != nil {
		return 0, fmt.Errorf("make room in category %d: %w", toCategoryID, err)
	}
	return position, nil
}

// DeleteTodo deletes the todo with the given id, or returns ErrNotFound. The
// todos below it in its column move up by one.
func (s *Store) DeleteTodo(ctx context.Context, id int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	// RETURNING says where the todo was, so that the gap it leaves can be closed.
	var categoryID, position int64
	err = tx.QueryRowContext(ctx, `DELETE FROM todos WHERE id = ? RETURNING category_id, position`, id).
		Scan(&categoryID, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("delete todo %d: %w", id, err)
	}
	if _, err := tx.ExecContext(ctx, closeGapSQL, categoryID, position); err != nil {
		return fmt.Errorf("delete todo %d: close gap in category %d: %w", id, categoryID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete todo %d: %w", id, err)
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
	err := row.Scan(&todo.ID, &todo.Title, &todo.CategoryID, &metadata, &createdAt, &updatedAt, &todo.Position)
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
