# Todo App Adım 1 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** SQLite + Go API + React arayüzünü uçtan uca çalışır hale getirmek: 3 sütunlu pano, istenen sütuna todo ekleme, kartı sütunlar arasında taşıma.

**Architecture:** Go sunucusu (`backend/`) gömülü SQL migration'larla SQLite şemasını açılışta kurar; `internal/db` veri erişimini, `internal/api` HTTP katmanını sağlar. React uygulaması (`frontend/`) Vite dev server'da çalışır ve `/api` isteklerini Go'ya proxy'ler.

**Tech Stack:** Go 1.26 (`net/http`, `database/sql`, `embed`, `log/slog`), `modernc.org/sqlite`; Vite + React + TypeScript, Tailwind CSS v4; Vitest + React Testing Library.

**Spec:** `docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md` — plan bu spec'e dayanır; uygulayan herkes ikisini de okur.

**Yürütme (kullanıcı tarafından belirlendi):** Tam olarak 3 subagent — **SQL agent** (Görev 1–3), **API agent** (Görev 4–7), **Frontend agent** (Görev 8–11). Görev 0 ve 12'yi ve her teslimin incelemesini orkestratör (ana thread) yapar. SQL ve Frontend agent paralel başlar; API agent, SQL agent'ın teslimi incelenip commit'lendikten sonra başlar. Agent'lar git komutu çalıştırmaz; her teslimden sonra orkestratör commit'ler. Düzeltme gerekirse aynı agent'a geri gönderilir.

## Global Constraints

- Platform: Windows 11, Go 1.26.1, Node 24, npm 11. CGO kapalı → SQLite sürücüsü yalnızca `modernc.org/sqlite`.
- Go modülü `todoapp`, kökü `backend/`. API agent yalnızca standart kütüphane kullanır.
- Sunucu ayarları: `ADDR` varsayılan `127.0.0.1:8080`; `DB_PATH` varsayılan `data/todos.db` (çalışma dizinine göre).
- Vite: port `5173`, `strictPort: true`, proxy `/api` → `http://127.0.0.1:8080`.
- Başlangıç kategorileri birebir (id, name, position): `(1, "Henüz yapılmadı", 1)`, `(2, "Yapılıyor", 2)`, `(3, "Tamamlandı", 3)`.
- Zaman damgalarını yalnızca SQL üretir: `strftime('%Y-%m-%dT%H:%M:%fZ','now')`. Go zaman damgası yazmaz.
- API hata gövdesi `{"error": "<mesaj>"}`. Mesajlar birebir: `title is required`, `title must be at most 500 characters`, `title must be a string`, `category_id must be an integer`, `category does not exist`, `metadata must be a JSON object`, `<alan> must not be null`, `no fields to update`, `invalid JSON body`, `unknown field "<alan>"`, `request body too large`, `invalid id`, `todo not found`, `internal server error`.
- Arayüz metinleri birebir: sayfa başlığı `Todo'lar`; `Yükleniyor...`; başlık alanı placeholder `Yeni todo yaz...`, etiketi `Todo başlığı`; form seçici etiketi `Sütun`; buton `Ekle`; sütun başlığı `<ad> (<sayı>)`; boş sütun `Boş`; kart seçici etiketi `<başlık> için sütun`; açılış hatası `Sunucuya ulaşılamadı.` + buton `Tekrar dene`; `Todo eklenemedi: <detay>`; `Todo taşınamadı: <detay>`.
- `api.ts` hata detayı: `fetch` reddederse `sunucuya ulaşılamadı`; hata gövdesi JSON değilse `sunucu hatası (HTTP <durum>)`; aksi halde API'nin `error` alanı.
- Frontend bağımlılıkları: runtime `react`, `react-dom`; stil `tailwindcss`, `@tailwindcss/vite`; test `vitest`, `@testing-library/react`, `@testing-library/user-event`, `@testing-library/jest-dom`, `jsdom`. Başka paket yok.
- Yazma alanları: SQL agent → `backend/internal/db/`, `backend/go.mod`, `backend/go.sum`; API agent → `backend/internal/api/`, `backend/cmd/server/`; Frontend agent → `frontend/`. Dışarıda değişiklik gerekirse yapılmaz, raporlanır. Agent'lar git komutu çalıştırmaz.
- Sunucuyu elle doğrularken `go run` yerine derlenmiş exe çalıştırılır ve iş bitince kapatılır (Windows'ta `go run` öldürülünce derlenen alt süreç açık kalıp portu tutabilir). Hiçbir agent arkasında çalışan süreç (sunucu, dev server, watch modu) bırakmaz.
- Windows'ta curl komut satırı argümanlarındaki Türkçe karakterler bozulabilir; elle curl doğrulamasında ASCII başlık kullanılır. Türkçe metin testlerle doğrulanır.

## Review Focus

1. **Boşluk içeren yol** — proje `claude testing` klasöründe, yani `DB_PATH`'in mutlak hali boşluk içerir; DB yine açılmalı. → Görev 1 açılış testi `"nested dir"` alt klasörünü kullanır; DSN'de `file:` öneki kullanılmaz.
2. **`<select>` değerleri string'dir** — `category_id` API'ye sayı olarak gitmeli; `"3"` gönderilirse API 400 döner. → Görev 10 ve 11 testleri istek gövdesini sayıyla birebir karşılaştırır; Görev 5 `"3"` için 400'ü doğrular.
3. **Türkçe metin ve karakter sınırı** — seed adları ve Türkçe başlıklar bozulmadan dönmeli; 500 sınırı byte değil karakterdir. → Görev 1 (adlar birebir), Görev 2 `TestTurkishTextRoundTrip`, Görev 5 `TestCreateTodoRuneLimit` (500×`ğ` kabul, 501 red).
4. **Çift gönderim** — Enter'a iki kez basmak veya çift tıklamak tek todo oluşturmalı. → Görev 10 `sends only one POST on double submit`.
5. **Başarısız taşıma** — kart da seçicisi de eski sütunda kalmalı. → Görev 11 hata testi seçicinin değerini de doğrular.

---

### Görev 0: Depo iskeleti (orkestratör)

**Files:**
- Create: `.gitignore`
- Create: `backend/go.mod`

- [ ] **Step 1: `.gitignore` oluştur**

```gitignore
# SQLite veritabanı (çalışınca oluşur)
backend/data/
*.db
*.db-shm
*.db-wal
```

- [ ] **Step 2: Go modülünü başlat**

Run: `cd backend && go mod init todoapp`, ardından `go -C backend list -m`
Expected: `todoapp`

- [ ] **Step 3: Commit** — `.gitignore`, `backend/go.mod`

---

## SQL agent — Görev 1–3 (`backend/internal/db/`)

### Görev 1: Bağlantı, migration'lar ve kategoriler

**Files:**
- Create: `backend/internal/db/migrations/0001_init.sql` — spec §5.1'deki SQL birebir
- Create: `backend/internal/db/db.go` — `Store`, `Open`, `Close`, `openDB`, hata değişkenleri
- Create: `backend/internal/db/migrate.go` — `//go:embed migrations/*.sql` ve `migrate`
- Create: `backend/internal/db/categories.go` — `Category`, `ListCategories`
- Test: `backend/internal/db/db_test.go`

**Interfaces:**
- Consumes: —
- Produces:
  - `type Store struct{ db *sql.DB }`
  - `func Open(path string) (*Store, error)`, `func (s *Store) Close() error`
  - `type Category struct { ID int64 \`json:"id"\`; Name string \`json:"name"\`; Position int64 \`json:"position"\` }`
  - `func (s *Store) ListCategories(ctx context.Context) ([]Category, error)`
  - `var ErrNotFound = errors.New("not found")`, `var ErrInvalidCategory = errors.New("invalid category")`
  - iç: `func openDB(path string) (*sql.DB, error)`, `func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error`
  - test yardımcısı: `func newTestStore(t *testing.T) *Store` (Görev 2–3 de kullanır)

- [ ] **Step 1: Failing testleri yaz** (`db_test.go`)

```go
var wantCategories = []Category{
	{ID: 1, Name: "Henüz yapılmadı", Position: 1},
	{ID: 2, Name: "Yapılıyor", Position: 2},
	{ID: 3, Name: "Tamamlandı", Position: 3},
}

// newTestStore: Open(filepath.Join(t.TempDir(), "test.db")), t.Cleanup ile Close.
// userVersion(t, s): PRAGMA user_version değerini döndürür.

func TestOpenCreatesDirAndAppliesMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested dir", "todos.db") // olmayan ve boşluk içeren klasör
	// Open(path) → err == nil; os.Stat(path) başarılı; userVersion == 1
	// ListCategories → reflect.DeepEqual(got, wantCategories)
}

func TestOpenIsIdempotent(t *testing.T) {
	// aynı yol: Open → Close → Open; userVersion == 1; ListCategories == wantCategories (tekrar kayıt yok)
}

func TestOpenRelativePath(t *testing.T) {
	t.Chdir(t.TempDir())
	// Open("data/todos.db") → err == nil; os.Stat("data/todos.db") başarılı
}

func TestForeignKeysEnabledOnEveryConnection(t *testing.T) {
	// s.db.Conn(ctx) ile aynı anda 3 bağlantı tut; her birinde "PRAGMA foreign_keys" == 1
}

func TestMigrateRollsBackFailedMigration(t *testing.T) {
	// raw, _ := openDB(filepath.Join(t.TempDir(), "m.db"))
	fsys := fstest.MapFS{
		"0001_ok.sql":  {Data: []byte("CREATE TABLE a (id INTEGER);")},
		"0002_bad.sql": {Data: []byte("CREATE TABLE b (id INTEGER); SELECT * FROM missing_table;")},
	}
	// migrate(ctx, raw, fsys) → hata; user_version == 1; tablo a var, tablo b yok (sqlite_master)
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/db/...`
Expected: FAIL (derleme hatası: `Open` tanımsız)

- [ ] **Step 3: Uygula**

- `go -C backend get modernc.org/sqlite@latest`
- `openDB`: `os.MkdirAll(filepath.Dir(path), 0o755)`; `sql.Open("sqlite", path+"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate")`. `file:` öneki yok. Havuz tek bağlantıya düşürülmez (`SetMaxOpenConns(1)` yok); eşzamanlılık `_txlock=immediate` + `busy_timeout` ile çözülür. DSN parametrelerini `go doc modernc.org/sqlite` ile doğrula.
- `migrate`: `fsys` kökündeki dosyalar `^\d{4}_.+\.sql$` desenine uymalı (uymayan veya tekrar eden sürüm → hata); sürüme göre sırala; `PRAGMA user_version`'dan büyük olanların her birini tek transaction'da `Exec(sql)` + `PRAGMA user_version = N` ile uygula; hata → rollback ve dosya adını içeren sarılmış hata.
- `Open`: `openDB` + `migrate(ctx, db, fs.Sub(migrationsFS, "migrations"))`; hata olursa açılan DB'yi kapat.
- `ListCategories`: `ORDER BY position, id`.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/db/...`
Expected: PASS

### Görev 2: Todo oluşturma, getirme, listeleme

**Files:**
- Create: `backend/internal/db/todos.go`
- Test: `backend/internal/db/todos_test.go`

**Interfaces:**
- Consumes: Görev 1 — `Store`, `newTestStore`, `ErrNotFound`, `ErrInvalidCategory`
- Produces: `Todo` ve `CreateTodoParams` (spec §5.4 birebir, json etiketleriyle); `func (s *Store) CreateTodo(ctx context.Context, p CreateTodoParams) (Todo, error)`; `func (s *Store) GetTodo(ctx context.Context, id int64) (Todo, error)`; `func (s *Store) ListTodos(ctx context.Context) ([]Todo, error)`

- [ ] **Step 1: Failing testleri yaz** (`todos_test.go`)

```go
func ptr[T any](v T) *T { return &v }

func TestCreateTodoDefaultsToFirstCategory(t *testing.T) {
	// CreateTodo({Title: "Süt al"}) → ID > 0; CategoryID == 1; string(Metadata) == "{}"
	// CreatedAt sıfır değil, CreatedAt.Location() == time.UTC; UpdatedAt.Equal(CreatedAt)
}
func TestCreateTodoWithCategoryAndMetadata(t *testing.T) {
	// {Title: "Fatura öde", CategoryID: ptr(int64(3)), Metadata: json.RawMessage(`{"source":"test","n":1}`)}
	// → CategoryID == 3; Metadata çözülünce map{"source":"test","n":1} ile DeepEqual
}
func TestCreateTodoInvalidCategory(t *testing.T) {
	// CategoryID: ptr(int64(99)) → errors.Is(err, ErrInvalidCategory)
}
func TestTurkishTextRoundTrip(t *testing.T) {
	// title := "Süt al — çğıöşü İĞÜŞÖÇ"; CreateTodo → GetTodo(id).Title == title
}
func TestGetTodoNotFound(t *testing.T)      { /* GetTodo(999) → errors.Is(err, ErrNotFound) */ }
func TestListTodosEmpty(t *testing.T)       { /* err == nil; todos != nil; len == 0 */ }
func TestListTodosNewestFirst(t *testing.T) { /* A, B, C sırayla oluştur → ID sırası [C, B, A] */ }
func TestConstraints(t *testing.T) {
	// s.db.Exec(`INSERT INTO todos (title, category_id, metadata) VALUES ('x', 1, '[]')`) → hata
	// kategori 1'de bir todo varken s.db.Exec(`DELETE FROM categories WHERE id = 1`) → hata
}
func TestConcurrentCreates(t *testing.T) {
	// 20 goroutine: CreateTodo({Title: fmt.Sprintf("t%d", i)}) → hiçbiri hata dönmez; ListTodos len == 20
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/db/...`
Expected: FAIL (`CreateTodo` tanımsız)

- [ ] **Step 3: Uygula** (`todos.go`)

CreateTodo tek transaction: kategori verilmemişse `SELECT id FROM categories ORDER BY position, id LIMIT 1`; verilmişse varlığını kontrol et (yoksa `ErrInvalidCategory`); metadata nil/boşsa `{}`; `INSERT ... RETURNING`. Zaman damgalarını kolon varsayılanları üretir. Okurken `metadata` ve zaman kolonlarını `string`'e tara; `json.RawMessage(s)` ve `time.Parse(time.RFC3339Nano, s)` ile çevir. ListTodos: `ORDER BY created_at DESC, id DESC`; sonuç yoksa `[]Todo{}`.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/db/...`
Expected: PASS

### Görev 3: Todo güncelleme ve silme

**Files:**
- Modify: `backend/internal/db/todos.go`
- Test: `backend/internal/db/todos_test.go`

**Interfaces:**
- Consumes: Görev 2 — `Todo`, `CreateTodo`, `GetTodo`, `ptr`
- Produces: `UpdateTodoParams` (spec §5.4 birebir); `func (s *Store) UpdateTodo(ctx context.Context, id int64, p UpdateTodoParams) (Todo, error)`; `func (s *Store) DeleteTodo(ctx context.Context, id int64) error`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestUpdateTodoFields(t *testing.T) {
	// alt testler: "title" / "category" / "metadata". Her birinde:
	// CreateTodo({Title: "Eski", Metadata: `{"a":1}`}); time.Sleep(20 * time.Millisecond); UpdateTodo(...)
	// → değişen alan yeni değerde; diğer alanlar aynı; CreatedAt aynı; UpdatedAt.After(önceki UpdatedAt)
	// "metadata": {Metadata: `{"b":2}`} → sonuç tam olarak {"b":2} (birleştirme yok)
}
func TestUpdateTodoNotFound(t *testing.T) {
	// UpdateTodo(999, {CategoryID: ptr(int64(99))}) → errors.Is(err, ErrNotFound) (kategori kontrolünden önce)
}
func TestUpdateTodoInvalidCategory(t *testing.T) {
	// var olan todo + CategoryID 99 → errors.Is(err, ErrInvalidCategory); GetTodo ile todo değişmemiş
}
func TestDeleteTodo(t *testing.T) {
	// DeleteTodo → nil; GetTodo → ErrNotFound; tekrar DeleteTodo → ErrNotFound
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/db/...`
Expected: FAIL (`UpdateTodo` tanımsız)

- [ ] **Step 3: Uygula**

UpdateTodo tek transaction: todo yoksa `ErrNotFound`; `CategoryID` verilmişse var olmalı (`ErrInvalidCategory`); yalnızca nil olmayan alanları `SET` et, her zaman `updated_at = strftime('%Y-%m-%dT%H:%M:%fZ','now')`; güncel satırı döndür. DeleteTodo: etkilenen satır 0 ise `ErrNotFound`.

- [ ] **Step 4: Tüm paket kontrolleri**

Run: `go -C backend mod tidy && go -C backend vet ./... && go -C backend test ./internal/db/...`
Expected: PASS, vet çıktısı boş

- [ ] **Step 5: Teslim raporu** — oluşturulan dosyalar, test çıktısı, spec'ten sapmalar. Commit'i orkestratör atar.

---

## API agent — Görev 4–7 (`backend/internal/api/`, `backend/cmd/server/`)

### Görev 4: Handler iskeleti, JSON yardımcıları, okuma uç noktaları

**Files:**
- Create: `backend/internal/api/api.go` — `New`, rotalar, istek logu
- Create: `backend/internal/api/json.go` — `writeJSON`, `writeError`, `decodeJSON`
- Test: `backend/internal/api/api_test.go`

**Interfaces:**
- Consumes: `db.Open`, `(*db.Store).ListCategories`, `(*db.Store).ListTodos`
- Produces:
  - `func New(store *db.Store) http.Handler`
  - iç: `writeJSON(w http.ResponseWriter, status int, v any)`, `writeError(w http.ResponseWriter, status int, msg string)`, `decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error` — 1 MiB `http.MaxBytesReader`, `DisallowUnknownFields`, gövdede tek JSON değeri; dönen hatanın `Error()` metni doğrudan istemciye verilebilir: `invalid JSON body`, `unknown field "<alan>"`, `request body too large`
  - test yardımcıları: `newTestAPI(t *testing.T) (http.Handler, *db.Store)`, `do(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder`

- [ ] **Step 1: Failing testleri yaz** (`api_test.go`)

```go
func TestHealth(t *testing.T) {
	// GET /api/health → 200; Header "Content-Type" == "application/json; charset=utf-8"; gövde {"status":"ok"}
}
func TestListCategories(t *testing.T) {
	// GET /api/categories → 200; gövde çözülünce
	// [{"id":1,"name":"Henüz yapılmadı","position":1},{"id":2,"name":"Yapılıyor","position":2},{"id":3,"name":"Tamamlandı","position":3}]
}
func TestListTodosEmptyIsArray(t *testing.T) {
	// GET /api/todos → 200; strings.TrimSpace(body) == "[]"
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/api/...`
Expected: FAIL (`New` tanımsız)

- [ ] **Step 3: Uygula**

Rotalar Go 1.22+ desenleriyle (`mux.HandleFunc("GET /api/health", ...)`). İstek logu: durum kodunu yakalayan `ResponseWriter` sarmalayıcısı + `slog.Info("request", "method", ..., "path", ..., "status", ..., "duration", ...)`. Beklenmeyen hata: `slog.Error` + 500 `internal server error`. `unknown field` mesajı için `encoding/json` hatasındaki `json: ` öneki atılır.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/api/...`
Expected: PASS

### Görev 5: Todo oluşturma ve id ile getirme

**Files:**
- Create: `backend/internal/api/todos.go`
- Test: `backend/internal/api/todos_test.go`

**Interfaces:**
- Consumes: `(*db.Store).CreateTodo`, `GetTodo`, `db.CreateTodoParams`, `db.ErrNotFound`, `db.ErrInvalidCategory`; Görev 4 yardımcıları
- Produces: `POST /api/todos`, `GET /api/todos/{id}`; iç: `type todoInput struct { Title, CategoryID, Metadata json.RawMessage }` (json: `title`, `category_id`, `metadata`) ve alan doğrulaması (JSON `null` → `<alan> must not be null`); `parseID(r *http.Request) (int64, bool)` — Görev 6 bunları yeniden kullanır

- [ ] **Step 1: Failing testleri yaz** (`todos_test.go`)

```go
func TestCreateTodo(t *testing.T) {
	// POST {"title":"  Süt al  "} → 201; title "Süt al"; category_id 1; metadata map'e çözülür ve boştur;
	//   created_at time.Parse(time.RFC3339Nano, ...) ile çözülür
	// POST {"title":"Fatura öde","category_id":3,"metadata":{"a":1}} → 201; category_id 3; metadata {"a":1}
	// GET /api/todos → 2 todo, ilk sırada "Fatura öde"
}
func TestCreateTodoRuneLimit(t *testing.T) {
	// title strings.Repeat("ğ", 500) → 201 ve dönen title birebir aynı
	// title strings.Repeat("ğ", 501) → 400 "title must be at most 500 characters"
}
func TestCreateTodoValidation(t *testing.T) {
	// tablo testi: gövde → 400 ve error mesajı
	// `{}`                               → "title is required"
	// `{"title":"   "}`                  → "title is required"
	// `{"title":null}`                   → "title must not be null"
	// `{"title":123}`                    → "title must be a string"
	// `{"title":"x","category_id":99}`   → "category does not exist"
	// `{"title":"x","category_id":"3"}`  → "category_id must be an integer"
	// `{"title":"x","category_id":null}` → "category_id must not be null"
	// `{"title":"x","metadata":[1]}`     → "metadata must be a JSON object"
	// `{"title":"x","metadata":"str"}`   → "metadata must be a JSON object"
	// `{"title":"x","extra":1}`          → strings.HasPrefix(msg, "unknown field")
	// `{"title":`                        → "invalid JSON body"
	// 1 MiB'tan büyük gövde               → "request body too large"
}
func TestGetTodoByID(t *testing.T) {
	// oluştur → GET /api/todos/{id} → 200, aynı todo
	// GET /api/todos/999 → 404 "todo not found"; /api/todos/abc, /0, /-1 → 400 "invalid id"
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/api/...`
Expected: FAIL

- [ ] **Step 3: Uygula**

Başlık: `strings.TrimSpace`; sınır `utf8.RuneCountInString(title) > 500`. Metadata: `map[string]any`'e çözülebiliyorsa nesnedir; `json.Compact` uygulanmış ham bayt saklanır. Hata eşlemesi: `db.ErrInvalidCategory` → 400 `category does not exist`; `db.ErrNotFound` → 404 `todo not found`; diğerleri → 500.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/api/...`
Expected: PASS

### Görev 6: Todo güncelleme (taşıma) ve silme

**Files:**
- Modify: `backend/internal/api/todos.go`
- Test: `backend/internal/api/todos_test.go`

**Interfaces:**
- Consumes: `(*db.Store).UpdateTodo`, `DeleteTodo`, `db.UpdateTodoParams`; Görev 5 — `todoInput` doğrulaması, `parseID`
- Produces: `PATCH /api/todos/{id}`, `DELETE /api/todos/{id}`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestPatchTodo(t *testing.T) {
	// {"title":"  Yeni  "} → 200; title "Yeni"; category_id değişmedi
	// {"category_id":2} → 200; category_id 2; GET /api/todos/{id} de 2 döner
	// metadata {"a":1} olan todo'ya {"metadata":{"b":2}} → metadata tam olarak {"b":2}
}
func TestPatchTodoValidation(t *testing.T) {
	// `{}` → 400 "no fields to update"; `{"category_id":99}` → 400 "category does not exist"
	// `{"category_id":null}` → 400 "category_id must not be null"; `{"title":""}` → 400 "title is required"
	// `{"x":1}` → 400, prefix "unknown field"
	// PATCH /api/todos/999 `{"title":"a"}` → 404 "todo not found"; /api/todos/abc → 400 "invalid id"
}
func TestDeleteTodo(t *testing.T) {
	// DELETE → 204 ve boş gövde; ardından GET → 404; tekrar DELETE → 404; /api/todos/abc → 400 "invalid id"
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/api/...`
Expected: FAIL

- [ ] **Step 3: Uygula** — PATCH, Görev 5'in alan doğrulamasını kullanır; hiçbir alan yoksa 400 `no fields to update`.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/api/...`
Expected: PASS

### Görev 7: Sunucu giriş noktası

**Files:**
- Create: `backend/cmd/server/main.go`

**Interfaces:**
- Consumes: `db.Open`, `(*db.Store).Close`, `api.New`
- Produces: çalıştırılabilir sunucu; `func run() error`, `func envOr(key, def string) string`

- [ ] **Step 1: Uygula**

`ADDR` (varsayılan `127.0.0.1:8080`), `DB_PATH` (varsayılan `data/todos.db`). Açılışta `slog.Info("listening", "addr", "http://"+addr, "db", dbPath)`. `http.Server{ReadHeaderTimeout: 5 * time.Second}`. `signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` → `Shutdown` (5 sn zaman aşımı) → `store.Close()`. `http.ErrServerClosed` hata sayılmaz; diğer hatalarda `main` 1 koduyla çıkar.

- [ ] **Step 2: Doğrula**

```bash
go -C backend vet ./... && go -C backend test ./...
go -C backend build -o "<geçici klasör>/todo-server.exe" ./cmd/server
# arka planda: DB_PATH="<geçici klasör>/todos.db" "<geçici klasör>/todo-server.exe"
curl -s http://127.0.0.1:8080/api/health                                    # {"status":"ok"}
curl -s http://127.0.0.1:8080/api/categories                                # 3 kategori
curl -s -X POST http://127.0.0.1:8080/api/todos -d '{"title":"Milk","category_id":3}'  # 201 + todo
curl -s http://127.0.0.1:8080/api/todos                                     # [Milk]
# sunucuyu kapat: taskkill //IM todo-server.exe //F  → port 8080 boş kalmalı
```

Expected: vet/test PASS; yorumlardaki yanıtlar; sunucu kapatıldıktan sonra 8080 dinlenmiyor.

- [ ] **Step 3: Teslim raporu** — dosyalar, test ve curl çıktıları, sapmalar. Commit'i orkestratör atar.

---

## Frontend agent — Görev 8–11 (`frontend/`)

### Görev 8: Kurulum ve API istemcisi

**Files:**
- Create (şablon): `frontend/`
- Modify: `frontend/vite.config.ts`, `frontend/src/index.css`, `frontend/index.html` (`lang="tr"`, `<title>Todo App</title>`), `frontend/package.json` (`"test": "vitest run"`)
- Create: `frontend/src/test/setup.ts`, `frontend/src/api.ts`
- Test: `frontend/src/api.test.ts`

**Interfaces:**
- Produces (`src/api.ts`):

```ts
export type Category = { id: number; name: string; position: number }
export type Todo = {
  id: number; title: string; category_id: number
  metadata: Record<string, unknown>; created_at: string; updated_at: string
}
export type CreateTodoInput = { title: string; category_id?: number; metadata?: Record<string, unknown> }
export type UpdateTodoInput = { title?: string; category_id?: number; metadata?: Record<string, unknown> }
export function getCategories(): Promise<Category[]>
export function getTodos(): Promise<Todo[]>
export function createTodo(input: CreateTodoInput): Promise<Todo>
export function updateTodo(id: number, patch: UpdateTodoInput): Promise<Todo>
```

- [ ] **Step 1: Şablonu ve bağımlılıkları kur** (proje kökünde)

```bash
npm create vite@latest frontend -- --template react-ts --no-interactive
cd frontend && npm install
npm install tailwindcss @tailwindcss/vite
npm install -D vitest @testing-library/react @testing-library/user-event @testing-library/jest-dom jsdom
```

CLI yine de soru sorarsa kurulum/başlatma tekliflerine "hayır" de; dev server başlatma. Peer dependency uyumsuzluğu çıkarsa uyumlu en yeni Vite sürümüne sabitle ve raporla.

`vite.config.ts`:

```ts
/// <reference types="vitest/config" />
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: { port: 5173, strictPort: true, proxy: { '/api': 'http://127.0.0.1:8080' } },
  test: { environment: 'jsdom', setupFiles: './src/test/setup.ts' },
})
```

`src/index.css` içeriği yalnızca `@import "tailwindcss";`. `src/test/setup.ts`: `import '@testing-library/jest-dom/vitest'` ve `afterEach(() => { cleanup(); vi.unstubAllGlobals() })`.

- [ ] **Step 2: Failing testleri yaz** (`api.test.ts`; `fetch`, `vi.stubGlobal('fetch', vi.fn(...))` ile taklit edilir)

```ts
it('getCategories GETs /api/categories and returns the parsed body')
// fetch ilk argümanı '/api/categories'; dönen değer 3 kategori
it('createTodo POSTs JSON and returns the created todo')
// fetch('/api/todos', { method: 'POST', headers: { 'Content-Type': 'application/json' },
//   body: JSON.stringify({ title: 'Süt al', category_id: 3 }) })
it('updateTodo PATCHes /api/todos/7')
// method 'PATCH'; body JSON.stringify({ category_id: 2 })
it('rejects with the API error message on non-2xx')
// 400 {"error":"title is required"} → rejects.toThrow('title is required')
it('rejects with "sunucu hatası (HTTP 500)" when the error body is not JSON')
it('rejects with "sunucuya ulaşılamadı" when fetch itself fails')
```

- [ ] **Step 3: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL (`./api` bulunamadı)

- [ ] **Step 4: Uygula** — tek bir `request<T>(method: string, path: string, body?: unknown): Promise<T>` yardımcısı; 204 → `undefined`.

- [ ] **Step 5: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS

### Görev 9: Pano — yükleme, sütunlar, hata ve tekrar deneme

**Files:**
- Modify: `frontend/src/App.tsx` (şablon içeriğinin tamamı değişir)
- Delete: `frontend/src/App.css` ve kullanılmayan şablon asset'leri (`index.html`'deki referanslar dahil)
- Create: `frontend/src/components/Board.tsx`, `frontend/src/components/TodoCard.tsx` (bu görevde yalnızca başlık), `frontend/src/test/mockApi.ts`
- Test: `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: Görev 8 — `getCategories`, `getTodos`, `Category`, `Todo`
- Produces:

```ts
// src/components/Board.tsx — her kategori: <section aria-label={name}> içinde <h2>{name} ({sayı})</h2> ve kart listesi (<ul>/<li>) ya da "Boş"
export function Board(props: { categories: Category[]; todos: Todo[]; onUpdated: (todo: Todo) => void })
// src/components/TodoCard.tsx
export function TodoCard(props: { todo: Todo; categories: Category[]; onUpdated: (todo: Todo) => void })
// src/test/mockApi.ts — global fetch'i taklit eder; anahtar "METHOD /yol"; tanımsız rota testi düşürür
export type MockReply = { status?: number; body?: unknown }
export function mockApi(routes: Record<string, (body: unknown) => MockReply | Promise<MockReply>>): Mock
export const seedCategories: Category[] // 3 başlangıç kategorisi
export function makeTodo(overrides: Partial<Todo>): Todo
```

- [ ] **Step 1: Failing testleri yaz** (`App.test.tsx`)

```ts
it('shows "Yükleniyor..." and then one column per category with counts')
// todos: [makeTodo({ id: 1, title: 'Fatura öde', category_id: 1 }), makeTodo({ id: 2, title: 'Süt al', category_id: 3 })]
// önce 'Yükleniyor...'; sonra heading'ler 'Henüz yapılmadı (1)', 'Yapılıyor (0)', 'Tamamlandı (1)'
it('puts each todo in its column in API order and shows "Boş" in empty columns')
// within(getByRole('region', { name: 'Henüz yapılmadı' })).getAllByRole('listitem') metinleri API sırasıyla
// within(getByRole('region', { name: 'Yapılıyor' })).getByText('Boş')
it('shows "Sunucuya ulaşılamadı." and reloads on "Tekrar dene"')
// ilk GET /api/todos → 500; alert 'Sunucuya ulaşılamadı.'; ikinci çağrı 200 → 'Tekrar dene' → sütunlar görünür
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL

- [ ] **Step 3: Uygula** — App: `Promise.all` ile yükleme; yükleniyor/hata/hazır durumları; `handleUpdated` id'si eşleşen todo'yu listede yerinde değiştirir. Board: geniş ekranda `md:grid-cols-3`, dar ekranda tek sütun. Kart başlığı uzun metinde taşmasın (`break-words`).

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS

### Görev 10: Ekleme formu

**Files:**
- Create: `frontend/src/components/AddTodoForm.tsx`
- Modify: `frontend/src/App.tsx`
- Test: `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: Görev 8 `createTodo`; Görev 9 `mockApi`, `seedCategories`, `makeTodo`
- Produces: `export function AddTodoForm(props: { categories: Category[]; onCreated: (todo: Todo) => void })`; App'te `handleCreated` yeni todo'yu listenin başına ekler

- [ ] **Step 1: Failing testleri yaz**

```ts
it('adds to the default column with a trimmed title')
// 'Todo başlığı' alanına '  Fatura öde  ' yaz, 'Ekle' → POST gövdesi toEqual({ title: 'Fatura öde', category_id: 1 })
// 'Henüz yapılmadı' bölgesindeki ilk listitem 'Fatura öde'; başlık alanı boş
it('adds directly to Tamamlandı when selected')
// selectOptions('Sütun', 'Tamamlandı'); 'Süt al' + {Enter} → gövde toEqual({ title: 'Süt al', category_id: 3 })
// heading 'Tamamlandı (1)'; 'Sütun' seçicisi toHaveValue('3')
it('disables "Ekle" while the trimmed title is empty')
// başlangıçta pasif; '   ' yazılınca pasif; 'a' yazılınca aktif
it('sends only one POST on double submit')
// POST yanıtı bekletilir; {Enter}{Enter} + 'Ekle' tıklaması → POST handler 1 kez çağrıldı
it('shows "Todo eklenemedi: <detay>" and keeps the input on failure')
// POST 400 {"error":"title is required"} → alert 'Todo eklenemedi: title is required'; başlık alanı değeri korunur
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL

- [ ] **Step 3: Uygula** — gönderim kilidi `useRef` ile senkron (state güncellenmeden gelen ikinci gönderim de engellenir); `submitting` state butonu pasifler; seçici değeri `Number(...)` ile sayıya çevrilir; varsayılan seçim ilk kategori.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS

### Görev 11: Kartı sütunlar arasında taşıma

**Files:**
- Modify: `frontend/src/components/TodoCard.tsx`
- Test: `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: Görev 8 `updateTodo`; Görev 9 `onUpdated` akışı ve test yardımcıları
- Produces: kart seçicisi — `aria-label={\`${todo.title} için sütun\`}`, kontrollü `value={todo.category_id}`

- [ ] **Step 1: Failing testleri yaz**

```ts
it('moves a card and PATCHes a numeric category_id')
// 'Fatura öde için sütun' → 'Yapılıyor' → PATCH /api/todos/1 gövdesi toEqual({ category_id: 2 })
// kart 'Yapılıyor' bölgesinde; heading 'Henüz yapılmadı (0)'
it('keeps API order (newest first) in the target column after a move')
// todos: [makeTodo({ id: 2, title: 'Yeni', category_id: 2 }), makeTodo({ id: 1, title: 'Eski', category_id: 1 })]
// 'Eski' → 'Yapılıyor' → 'Yapılıyor' bölgesindeki listitem sırası ['Yeni', 'Eski']
it('disables the card select while the PATCH is pending')
it('shows "Todo taşınamadı: <detay>" and leaves card and select on the old column on failure')
// PATCH 500 {"error":"boom"} → alert 'Todo taşınamadı: boom'; kart 'Henüz yapılmadı'da; seçici toHaveValue('1')
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL

- [ ] **Step 3: Uygula** — `updateTodo(todo.id, { category_id: Number(value) })`; istek sürerken seçici pasif; başarıda `onUpdated(dönen todo)`; hatada mesaj, yerel durum değişmez.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS

- [ ] **Step 5: Teslim raporu** — dosyalar, test/build/lint çıktıları, sapmalar. Commit'i orkestratör atar.

---

### Görev 12: Entegrasyon doğrulaması, README ve teslim (orkestratör)

**Files:**
- Create: `README.md` — gereksinimler, çalıştırma (`cd backend && go run ./cmd/server`, `cd frontend && npm install && npm run dev`), testler, ortam değişkenleri

- [ ] **Step 1: Otomatik kontroller**

Run: `go -C backend vet ./... && go -C backend test ./...` ve `npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend test`
Expected: hepsi PASS

- [ ] **Step 2: API'yi proxy üzerinden doğrula** — backend exe geçici DB ile (`DB_PATH=<scratchpad>/e2e/todos.db`), `npm --prefix frontend run dev`; `http://localhost:5173/api/...` üzerinden curl: health, categories, POST 201, POST hataları 400, GET/PATCH/DELETE 200/204/404/400.

- [ ] **Step 3: Headless tarayıcı** — scratchpad'de `playwright-core`, `chromium.launch({ channel: 'msedge' })`: spec §9 adım 2–5 (3 sütun → varsayılan sütuna ekleme → doğrudan "Tamamlandı"ya ekleme → taşıma → sayfa yenileme → backend yeniden başlatma → durum aynı); konsol hatası yok; ekran görüntüsü.

- [ ] **Step 4: Gerçek çalıştırma** — geçici backend'i kapat; varsayılan DB ile backend'i başlat; `backend/data/todos.db` oluştuğunu ve `/api/categories`'in 3 kategori döndürdüğünü doğrula; iki sunucuyu çalışır halde bırak.

- [ ] **Step 5: Commit ve kullanıcıya rapor** — `README.md` + son düzeltmeler.
