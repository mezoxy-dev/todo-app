package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// The statuses of a todo: how far the work has got, whatever its category.
const (
	StatusTodo       = "todo"
	StatusInProgress = "in_progress"
	StatusDone       = "done"
)

// The classification of a todo says who chose its category (step 3 spec 5).
const (
	ClassificationPending = "pending" // waits for the AI: new todos, "try again", migrated todos
	ClassificationDone    = "done"    // the AI has put it in its category
	ClassificationFailed  = "failed"  // the AI could not (no key, quota, an error, an invalid answer)
	ClassificationManual  = "manual"  // the user moved it to another category; the AI never touches it
)

// Todo is one card on the board.
type Todo struct {
	ID             int64           `json:"id"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	CategoryID     int64           `json:"category_id"`
	Position       int64           `json:"position"`       // place in its column, 0 = top
	Status         string          `json:"status"`         // StatusTodo, StatusInProgress or StatusDone
	Classification string          `json:"classification"` // see the Classification constants
	Metadata       json.RawMessage `json:"metadata"`       // always a valid JSON object
	CreatedAt      time.Time       `json:"created_at"`     // UTC
	UpdatedAt      time.Time       `json:"updated_at"`     // UTC
}

// CreateTodoParams describes a new todo. It always lands at the top of the
// inbox, waiting for the AI (ClassificationPending): the category is the AI's
// to choose.
type CreateTodoParams struct {
	Title       string          // validated by the caller (trimmed, 1-500 characters)
	Description string          // validated by the caller (trimmed, at most 2000 characters)
	Status      string          // StatusTodo, StatusInProgress or StatusDone, validated by the caller; empty: StatusTodo
	Metadata    json.RawMessage // nil or empty: {}
}

// UpdateTodoParams lists the fields to change; a nil field is left as it is.
// The caller makes sure that at least one field is set.
type UpdateTodoParams struct {
	Title       *string
	Description *string         // validated by the caller; an empty string clears it
	Status      *string         // validated by the caller
	CategoryID  *int64          // a different category than the todo is in makes it ClassificationManual
	Position    *int64          // place in the column the todo ends up in (see UpdateTodo); the caller makes sure it is >= 0
	Metadata    json.RawMessage // nil or empty: unchanged; otherwise replaces the old object (no merging)
}

// todoColumns is the column list of every query that returns a todo, in the
// order scanTodo reads it.
const todoColumns = `id, title, description, category_id, position, status, classification, metadata, created_at, updated_at`

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

// updateTodoSQL takes title, description, category_id, position, status, moved,
// metadata and id. For title, description, status and metadata a NULL parameter
// means "leave this field alone": COALESCE keeps the stored value, which is the
// same as leaving the field out of SET. None of the columns is nullable, so NULL
// can never be a real new value (an empty description is the empty string, not
// NULL). category_id and position are always given: UpdateTodo has worked out
// where the todo ends up, and for a todo that stays where it is, that is the
// place it already has. moved is true for a todo that goes to another category:
// the user chose its category, so it becomes "manual". updated_at always moves.
const updateTodoSQL = `UPDATE todos SET
	title          = COALESCE(?, title),
	description    = COALESCE(?, description),
	category_id    = ?,
	position       = ?,
	status         = COALESCE(?, status),
	classification = CASE WHEN ? THEN 'manual' ELSE classification END,
	metadata       = COALESCE(?, metadata),
	updated_at     = strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE id = ?
RETURNING ` + todoColumns

// CreateTodo inserts a todo at the top of the inbox, where the todos that were
// there move down by one, and returns it as stored. It waits for the AI
// (ClassificationPending). Both timestamps are set by SQL.
func (s *Store) CreateTodo(ctx context.Context, p CreateTodoParams) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	inbox, err := inboxID(ctx, tx)
	if err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}

	metadata := "{}"
	if len(p.Metadata) > 0 {
		metadata = string(p.Metadata)
	}
	status := p.Status
	if status == "" {
		status = StatusTodo
	}

	if _, err := tx.ExecContext(ctx, makeRoomOnTopSQL, inbox); err != nil {
		return Todo{}, fmt.Errorf("create todo: make room in the inbox: %w", err)
	}

	// metadata is bound as a string so that SQLite stores it as TEXT. The
	// classification is not named: its default is ClassificationPending.
	todo, err := scanTodo(tx.QueryRowContext(ctx,
		`INSERT INTO todos (title, description, category_id, position, status, metadata) VALUES (?, ?, ?, 0, ?, ?) RETURNING `+todoColumns,
		p.Title, p.Description, inbox, status, metadata))
	if err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("create todo: %w", err)
	}
	return todo, nil
}

// inboxID returns the id of the inbox, the system column found by its key.
func inboxID(ctx context.Context, tx *sql.Tx) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE key = ?`, InboxKey).Scan(&id); err != nil {
		return 0, fmt.Errorf("find the inbox: %w", err)
	}
	return id, nil
}

// GetTodo returns the todo with the given id, or ErrNotFound.
func (s *Store) GetTodo(ctx context.Context, id int64) (Todo, error) {
	todo, err := getTodo(ctx, s.db, id)
	if errors.Is(err, ErrNotFound) {
		return Todo{}, ErrNotFound
	}
	if err != nil {
		return Todo{}, fmt.Errorf("get todo %d: %w", id, err)
	}
	return todo, nil
}

// rowQuerier is what *sql.DB and *sql.Tx have in common: getTodo reads inside a
// transaction as well as outside.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// getTodo returns the todo with the given id, or ErrNotFound.
func getTodo(ctx context.Context, q rowQuerier, id int64) (Todo, error) {
	todo, err := scanTodo(q.QueryRowContext(ctx, `SELECT `+todoColumns+` FROM todos WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, ErrNotFound
	}
	return todo, err
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
//
// A todo that goes to another category than the one it is in becomes
// ClassificationManual (step 3 spec 5): the user has decided, and the AI will
// not touch it. Naming the category it is in already, or only a position, does
// not change the classification.
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
	var title, description, status, metadata any
	if p.Title != nil {
		title = *p.Title
	}
	if p.Description != nil {
		description = *p.Description
	}
	if p.Status != nil {
		status = *p.Status
	}
	if len(p.Metadata) > 0 {
		metadata = string(p.Metadata)
	}
	moved := toCategoryID != fromCategoryID

	todo, err := scanTodo(tx.QueryRowContext(ctx, updateTodoSQL,
		title, description, toCategoryID, toPosition, status, moved, metadata, id))
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

// The statements that record the outcome of a classification (step 3 spec 4 and
// 5). metadata.ai is set with json_set, which leaves the other keys of the
// metadata alone, from a JSON value that json() parses, so that it is stored as
// JSON and not as a string. updated_at moves with every change.
const (
	// classifiedSQL takes category_id, position, the ai value and id: the AI has
	// chosen the category.
	classifiedSQL = `UPDATE todos SET
		category_id    = ?,
		position       = ?,
		classification = 'done',
		metadata       = json_set(metadata, '$.ai', json(?)),
		updated_at     = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	WHERE id = ?
	RETURNING ` + todoColumns

	// failedSQL takes the ai value and id: the AI could not choose.
	failedSQL = `UPDATE todos SET
		classification = 'failed',
		metadata       = json_set(metadata, '$.ai', json(?)),
		updated_at     = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	WHERE id = ?
	RETURNING ` + todoColumns

	// requeuedSQL takes category_id, position and id: the todo waits for the AI
	// again, and what the AI said about it before is forgotten.
	requeuedSQL = `UPDATE todos SET
		category_id    = ?,
		position       = ?,
		classification = 'pending',
		metadata       = json_remove(metadata, '$.ai'),
		updated_at     = strftime('%Y-%m-%dT%H:%M:%fZ','now')
	WHERE id = ?
	RETURNING ` + todoColumns
)

// PendingTodoIDs returns the ids of the todos that wait for the AI, the oldest
// first (by created_at, then by id). The slice is never nil.
func (s *Store) PendingTodoIDs(ctx context.Context) ([]int64, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM todos WHERE classification = ? ORDER BY created_at, id`, ClassificationPending)
	if err != nil {
		return nil, fmt.Errorf("list pending todos: %w", err)
	}
	defer rows.Close()

	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list pending todos: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list pending todos: %w", err)
	}
	return ids, nil
}

// ApplyClassification records that the AI put the todo in the category with the
// given key. If the todo exists, is still in the inbox and is still pending, it
// goes to the top of that category, becomes ClassificationDone and gets ai as
// its metadata.ai (a JSON value, an object in practice); the result is the
// todo as stored and true. Otherwise nothing changes and the result is the todo
// as it is and false: the user has moved it by hand in the meantime, or it has
// failed or been classified already, and the answer of the AI must not undo
// that (step 3 spec 5). The check and the move are one transaction, so a move
// by hand is either entirely before or entirely after.
//
// It returns ErrNotFound if the todo does not exist and, only after that check,
// ErrInvalidCategory if no category has that key. The inbox is not a category
// the AI can choose, so its key is invalid as well.
func (s *Store) ApplyClassification(ctx context.Context, id int64, categoryKey string, ai json.RawMessage) (Todo, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	todo, err := getTodo(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return Todo{}, false, ErrNotFound
	}
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}

	if categoryKey == InboxKey {
		return Todo{}, false, ErrInvalidCategory
	}
	var target int64
	err = tx.QueryRowContext(ctx, `SELECT id FROM categories WHERE key = ?`, categoryKey).Scan(&target)
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, false, ErrInvalidCategory
	}
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: find category %q: %w", id, categoryKey, err)
	}
	inbox, err := inboxID(ctx, tx)
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}

	if todo.Classification != ClassificationPending || todo.CategoryID != inbox {
		return todo, false, nil
	}
	position, err := placeTodo(ctx, tx, id, inbox, todo.Position, target, 0)
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}
	// ai is bound as a string so that json() parses it.
	todo, err = scanTodo(tx.QueryRowContext(ctx, classifiedSQL, target, position, string(ai), id))
	if err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, false, fmt.Errorf("apply classification of todo %d: %w", id, err)
	}
	return todo, true, nil
}

// FailClassification records that the AI could not classify the todo. If the
// todo is still pending it becomes ClassificationFailed, stays in the inbox
// where it is, and gets ai as its metadata.ai; the result is the todo as stored
// and true. Otherwise (the user has moved it, it has failed or been classified
// already) nothing changes and the result is the todo as it is and false. It
// returns ErrNotFound if the todo does not exist.
func (s *Store) FailClassification(ctx context.Context, id int64, ai json.RawMessage) (Todo, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, false, fmt.Errorf("fail classification of todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	todo, err := getTodo(ctx, tx, id)
	if errors.Is(err, ErrNotFound) {
		return Todo{}, false, ErrNotFound
	}
	if err != nil {
		return Todo{}, false, fmt.Errorf("fail classification of todo %d: %w", id, err)
	}
	if todo.Classification != ClassificationPending {
		return todo, false, nil
	}
	todo, err = scanTodo(tx.QueryRowContext(ctx, failedSQL, string(ai), id))
	if err != nil {
		return Todo{}, false, fmt.Errorf("fail classification of todo %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, false, fmt.Errorf("fail classification of todo %d: %w", id, err)
	}
	return todo, true, nil
}

// RequeueClassification sends the todo back to the AI, whatever its state: it
// goes to the top of the inbox, becomes ClassificationPending, and what the AI
// said about it (metadata.ai) is removed. It returns the todo as stored, or
// ErrNotFound if it does not exist.
func (s *Store) RequeueClassification(ctx context.Context, id int64) (Todo, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}
	defer tx.Rollback() // no-op once the transaction has been committed

	var fromCategoryID, fromPosition int64
	err = tx.QueryRowContext(ctx, `SELECT category_id, position FROM todos WHERE id = ?`, id).
		Scan(&fromCategoryID, &fromPosition)
	if errors.Is(err, sql.ErrNoRows) {
		return Todo{}, ErrNotFound
	}
	if err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}
	inbox, err := inboxID(ctx, tx)
	if err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}

	position, err := placeTodo(ctx, tx, id, fromCategoryID, fromPosition, inbox, 0)
	if err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}
	todo, err := scanTodo(tx.QueryRowContext(ctx, requeuedSQL, inbox, position, id))
	if err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return Todo{}, fmt.Errorf("requeue todo %d: %w", id, err)
	}
	return todo, nil
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
	err := row.Scan(&todo.ID, &todo.Title, &todo.Description, &todo.CategoryID, &todo.Position,
		&todo.Status, &todo.Classification, &metadata, &createdAt, &updatedAt)
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
