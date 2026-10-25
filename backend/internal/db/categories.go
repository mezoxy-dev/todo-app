package db

import (
	"context"
	"fmt"
)

// InboxKey is the key of the system column that holds the todos which wait for
// the AI or could not be classified. A new todo always starts there.
const InboxKey = "inbox"

// Category is a board column. Since step 3 the columns are the intents the AI
// sorts the todos into, plus the system column InboxKey.
type Category struct {
	ID       int64  `json:"id"`
	Key      string `json:"key"` // stable name: the AI answers with it, the inbox is found by it
	Name     string `json:"name"`
	Position int64  `json:"position"`
	// Description tells the AI what belongs in the category. It is not part of
	// the API.
	Description string `json:"-"`
}

// ListCategories returns all categories ordered by position (left to right
// on the board), then id. The slice is never nil.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, key, name, position, description FROM categories ORDER BY position, id`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	categories := []Category{}
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Key, &c.Name, &c.Position, &c.Description); err != nil {
			return nil, fmt.Errorf("list categories: %w", err)
		}
		categories = append(categories, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	return categories, nil
}
