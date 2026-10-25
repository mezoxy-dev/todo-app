ALTER TABLE todos ADD COLUMN position INTEGER NOT NULL DEFAULT 0;

-- Mevcut todo'lar: her sütundaki şimdiki sıra (en yeni üstte) korunur
UPDATE todos SET position = ranked.rn
FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY category_id
                                    ORDER BY created_at DESC, id DESC) - 1 AS rn
      FROM todos) AS ranked
WHERE todos.id = ranked.id;

CREATE INDEX idx_todos_category_position ON todos(category_id, position);
