CREATE TABLE categories (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  name       TEXT    NOT NULL UNIQUE COLLATE NOCASE CHECK (length(trim(name)) > 0),
  position   INTEGER NOT NULL,          -- sütun sırası (soldan sağa)
  created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

INSERT INTO categories (name, position) VALUES
  ('Henüz yapılmadı', 1), ('Yapılıyor', 2), ('Tamamlandı', 3);

CREATE TABLE todos (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  title       TEXT    NOT NULL CHECK (length(trim(title)) > 0),
  category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE RESTRICT,
  metadata    TEXT    NOT NULL DEFAULT '{}'
              CHECK (json_valid(metadata) AND json_type(metadata) = 'object'),
  created_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
  updated_at  TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX idx_todos_category_id ON todos(category_id);
