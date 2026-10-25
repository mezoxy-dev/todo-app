# Todo App Adım 2 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Kalıcı sütun içi sıralama ile sürükle-bırak ve üstteki minik menüden anında geçilen 5 CSS teması (Neubrutalizm, Glass, Swiss, Pano, Zen) — tek sayfa, tek işaretleme.

**Architecture:** Backend'e `position` (migration 0002) ve sıralama kuralları eklenir; API PATCH'i `position` kabul eder. Frontend tek bileşen ağacında kalır: bileşenler anlamlı sınıflar taşır, her tema `[data-theme]` altında yazılmış tek bir CSS dosyasıdır; sürükle-bırak `@hello-pangea/dnd` ile, taşıma iyimser güncelleme + geri alma ile yapılır.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`; React 19 + TypeScript + Vite 8 + Tailwind CSS v4; `@hello-pangea/dnd`; Vitest + Testing Library; `playwright-core` + Edge (doğrulama).

**Spec:** `docs/superpowers/specs/2026-10-24-todo-app-designs-dnd-design.md` — plan bu spec'e dayanır; uygulayan herkes ikisini de okur. (Adım 1 spec'i: `docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md`.)

## Global Constraints

- Branch `step-2-designs`. Windows 11, Go 1.26.1 (CGO kapalı), Node 24, npm 11.
- Go: yeni bağımlılık yok. Frontend: yalnızca `@hello-pangea/dnd` eklenir (React 19 ile uyumsuzsa `@dnd-kit/core` + `@dnd-kit/sortable`, raporlanarak).
- Sıra kuralı: her sütunda `position` boşluksuz `0..n-1`, 0 = en üst. `position` = todo kendi yerinden çıkarıldıktan sonraki listede hedef sıra; liste uzunluğundan büyükse sona. Backend ve `board.ts` bu anlamı birebir paylaşır.
- Yeni API mesajları birebir: `position must not be null`, `position must be a non-negative integer`. POST'ta `position` → 400 `unknown field "position"`.
- Arayüzden taşıma gövdesi birebir `{"category_id": <sayı>, "position": <sayı>}`; seçiciyle taşımada `position` = 0.
- Temalar birebir (id → etiket, sırayla): `neubrutalism` → `Neubrutalizm`, `glass` → `Glass`, `swiss` → `Swiss`, `skeuo` → `Pano`, `zen` → `Zen`. `localStorage` anahtarı `todo-theme`, varsayılan `neubrutalism`. Menü: `role="group"`, `aria-label="Tasarım"`, tuşlarda `aria-pressed`.
- İşaretleme sözleşmesi (spec §5.3) birebir: `.app`, `.app-header`, `.app-title`, `.theme-switcher`, `.theme-button`, `.add-form`, `.field`, `.add-input`, `.add-select`, `.button`, `.add-button`, `.board`, `.column` + `data-column="<kategori position>"`, `.column-title`, `.card-list` (+ `.is-drag-over`), `.card` (+ `.is-dragging`), `.card-body`, `.card-title`, `.card-select`, `.empty`, `.alert`, `.status`.
- Tema kuralları: her kural `[data-theme="<id>"]` altında; CSS `content` ile metin yok (yalnızca `content: ""`); `.card` üzerinde `transform` yok (efektler `.card-body`'de); hiçbir öğe gizlenmez.
- Adım 1 arayüz metinleri değişmez; taşıma hatası panonun üstünde `Todo taşınamadı: <detay>` (`role="alert"`, sınıf `.alert`).
- Google Fonts bağlantısı (`frontend/index.html`, birebir): `https://fonts.googleapis.com/css2?family=Caveat:wght@500;700&family=Inter:wght@400;600;800&family=Noto+Serif:wght@400;600&family=Space+Grotesk:wght@500;700&display=swap` (+ `fonts.googleapis.com` ve `fonts.gstatic.com` için `preconnect`).
- Agent'lar: yalnızca kendi yazma alanlarına yazar, git komutu çalıştırmaz, subagent başlatmaz, arkada süreç bırakmaz. Orkestratörün çalışan sunucuları (5173 Vite, 8080 Go) durdurulmaz/yeniden başlatılmaz; agent'lar bu portlarda sunucu açmaz.
- Curl doğrulamalarında ASCII başlık (Windows argv). Sunucu doğrulamasında `go run` değil derlenmiş exe + geçici `DB_PATH`.

## Review Focus

1. **Boş sütuna bırakma** — "Boş" yazan sütun da bırakma hedefi olmalı (bırakma alanı boşken de var ve yüksekliği olmalı). → Görev 6 testi: boş sütunda `.card-list` var ve "Boş" onun içinde; Görev 13 E2E: boş sütuna sürükleme.
2. **Aynı sütunda aşağı taşımada sıra kayması (off-by-one)** — en üstteki kartı 3 kartlık sütunda sıra 2'ye bırakınca en alta gitmeli. → Görev 2 (store) ve Görev 4 (`board.ts`) aynı vakayı aynı beklenen sırayla test eder.
3. **Tema kalıcılığı ve geçersiz kayıt** — yenilemede kayıtlı tema ilk çizimden itibaren uygulanmalı; bozuk kayıt varsayılana düşmeli. → Görev 5 testleri (`readStoredTheme`, açılış) + Görev 13 E2E (yenileme sonrası tema).
4. **Art arda hızlı taşıma** — bir taşıma kaydedilirken ikinci taşıma sırayı bozmamalı. → Görev 6 testi: bekleme sırasında tüm seçiciler pasif; hata sonrası önceki sıra birebir geri gelir.
5. **Tema sızıntısı** — temalar arasında geçince önceki temanın stili kalmamalı. → Görev 8–12 doğrulama adımı: kapsam dışı kural yok (grep); Görev 13: 5 temanın art arda ekran görüntüsü.

---

## Backend agent — Görev 1–3

### Görev 1: Migration 0002; oluşturma, listeleme ve silmede sıra

**Files:**
- Create: `backend/internal/db/migrations/0002_todo_position.sql` — spec §4.1 SQL'i birebir
- Modify: `backend/internal/db/todos.go`
- Test: `backend/internal/db/todos_test.go`, `backend/internal/db/db_test.go`

**Interfaces:**
- Consumes: adım 1 `db` paketi (`Store`, `openDB`, `migrate`, `migrationsFS`, `newTestStore`)
- Produces: `Todo.Position int64` (`json:"position"`); `ListTodos` sırası `category_id, position, id`; `CreateTodo` en üste ekler; `DeleteTodo` boşluğu kapatır; test yardımcıları `assertDensePositions(t *testing.T, s *Store)` ve `order(t *testing.T, s *Store, categoryID int64) []string` (başlıklar, position sırasıyla) — Görev 2 de kullanır

- [ ] **Step 1: Failing testleri yaz**

```go
func TestMigration0002BackfillsPositions(t *testing.T) {
	// raw, _ := openDB(temp); 0001'i tek başına uygula:
	//   init, _ := fs.ReadFile(migrationsFS, "migrations/0001_init.sql")
	//   migrate(ctx, raw, fstest.MapFS{"0001_init.sql": {Data: init}}) → user_version 1
	// doğrudan INSERT: kategori 1'e created_at '...01', '...02', '...03' olan A, B, C; kategori 3'e D ('...01'), E ('...02')
	// migrate(ctx, raw, fs.Sub(migrationsFS, "migrations")) → user_version 2
	// kategori 1 position sırası [C, B, A], kategori 3 [E, D] (en yeni üstte korunur)
}
func TestCreateTodoGoesToTop(t *testing.T) {
	// kategori 1'e A, B, C oluştur → order(1) == [C, B, A]; dönen C.Position == 0
	// kategori 3'e D → order(3) == [D]; assertDensePositions
}
func TestDeleteTodoClosesGap(t *testing.T) {
	// order(1) == [C, B, A]; B'yi sil → order(1) == [C, A]; assertDensePositions
}
func TestListTodosOrdersByCategoryThenPosition(t *testing.T) {
	// kategori 3'e X, kategori 1'e A, B → ListTodos başlıkları [B, A, X]
}
```

Adım 1 testlerinden güncellenecekler: `user_version` beklentileri 1 → 2; `TestListTodosOrdersByCreatedAtThenID` yeni sıralamayı (category_id, position, id) doğrulayan teste dönüşür.

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/db/...`
Expected: FAIL (`Position` alanı yok / migration 0002 yok)

- [ ] **Step 3: Uygula**

`todoColumns`'a `position` eklenir. CreateTodo (mevcut transaction'da): `UPDATE todos SET position = position + 1 WHERE category_id = ?`, ardından `position = 0` ile INSERT. DeleteTodo tek transaction: satırın `category_id, position`'ı okunur (yoksa `ErrNotFound`), silinir, `UPDATE todos SET position = position - 1 WHERE category_id = ? AND position > ?`.

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/db/...`
Expected: PASS

### Görev 2: UpdateTodo'da sıra

**Files:**
- Modify: `backend/internal/db/todos.go`
- Test: `backend/internal/db/todos_test.go`

**Interfaces:**
- Consumes: Görev 1 — `Todo.Position`, `assertDensePositions`, `order`
- Produces: `UpdateTodoParams.Position *int64` ve spec §4.3 davranışı

- [ ] **Step 1: Failing testleri yaz**

```go
func TestUpdateTodoPosition(t *testing.T) {
	// her alt test taze store: kategori 1 = [A, B, C] (üstten alta), kategori 2 = [D, E]
	// (sırayla C, B, A ve E, D oluşturulur). Alt testler: hareket → beklenen order(1), order(2)
	// "down within column":          A, Position 2                → [B, C, A]
	// "up within column":            C, Position 0                → [C, A, B]
	// "to the middle":               A, Position 1                → [B, A, C]
	// "across columns at index":     B, CategoryID 2, Position 1  → [A, C] / [D, B, E]
	// "across past the end":         A, CategoryID 2, Position 99 → [B, C] / [D, E, A]
	// "within past the end":         A, Position 99               → [B, C, A]
	// "category only goes to top":   C, CategoryID 2              → [A, B] / [C, D, E]
	// "same category keeps place":   B, CategoryID 1              → [A, B, C]
	// "title only keeps place":      B, Title "B2"                → [A, B2, C]
	// her alt testte: assertDensePositions; dönen todo'nun Position'ı order içindeki indeksi;
	// time.Sleep(20 * time.Millisecond) sonrası güncellemede UpdatedAt ilerler
}
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/db/...`
Expected: FAIL (`Position` alanı yok)

- [ ] **Step 3: Uygula**

Tek transaction: todo'nun mevcut `category_id, position`'ı okunur (yoksa `ErrNotFound`); kategori verilmişse varlığı kontrol edilir (yoksa `ErrInvalidCategory`). Hedef sütun = verilen kategori ya da mevcut; hedef sıra = verilen `Position`, yoksa kategori değiştiyse 0, değilse mevcut sıra. Kaynakta boşluk kapatılır (`position - 1 WHERE category_id = kaynak AND position > eski`), hedef sıra hedef sütunun (taşınan hariç) uzunluğuna sabitlenir, hedefte yer açılır (`position + 1 WHERE category_id = hedef AND position >= sıra AND id != todo`), satır yazılır (`title`/`metadata` değişiklikleri ve `updated_at` dahil).

- [ ] **Step 4: Testlerin geçtiğini gör**

Run: `go -C backend test ./internal/db/...`
Expected: PASS (adım 1 hata sırası testleri dahil)

### Görev 3: API'de position

**Files:**
- Modify: `backend/internal/api/todos.go`
- Test: `backend/internal/api/todos_test.go`

**Interfaces:**
- Consumes: Görev 2 — `UpdateTodoParams.Position`
- Produces: PATCH `position` alanı ve doğrulaması; tüm todo yanıtlarında `position`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestPatchTodoPosition(t *testing.T) {
	// kategori 1'e A, B, C oluştur (üstten alta C, B, A)
	// PATCH A {"position":0} → 200, "position":0; GET /api/todos kategori 1 sırası [A, C, B]
	// PATCH B {"category_id":3,"position":1} → 200, category_id 3, position 0 (hedef boştu → sona = 0)
	// GET /api/todos yanıtındaki her öğede "position" alanı var
}
func TestPositionValidation(t *testing.T) {
	// PATCH {"position":null}  → 400 "position must not be null"
	// PATCH {"position":-1}    → 400 "position must be a non-negative integer"
	// PATCH {"position":1.5}   → 400 "position must be a non-negative integer"
	// PATCH {"position":"1"}   → 400 "position must be a non-negative integer"
	// POST  {"title":"x","position":0} → 400 `unknown field "position"`
}
```

Adım 1 testlerinden liste sırasına dayanan beklentiler (ör. `TestCreateTodo`'daki "ilk sırada Fatura öde") yeni sıralamaya (category_id, position) göre güncellenir.

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `go -C backend test ./internal/api/...`
Expected: FAIL

- [ ] **Step 3: Uygula** — PATCH girdisine `position` (ham JSON; `null` ve tip ayrımı adım 1 desenindeki gibi); "alan yok" kontrolüne `Position` eklenir. POST, `position` alanını bilinmeyen alan olarak reddeder.

- [ ] **Step 4: Kontroller**

Run: `go -C backend vet ./... && go -C backend test ./... && gofmt -l backend`
Expected: PASS, gofmt çıktısı boş

- [ ] **Step 5: Teslim raporu** — dosyalar, TDD kanıtı, sapmalar. Commit'i orkestratör atar.

---

## Frontend core agent — Görev 4–6

### Görev 4: API tipleri ve `board.ts`

**Files:**
- Modify: `frontend/src/api.ts`, `frontend/src/test/mockApi.ts` (`makeTodo` varsayılanına `position: 0`)
- Create: `frontend/src/board.ts`
- Test: `frontend/src/board.test.ts`

**Interfaces:**
- Produces: `Todo.position: number`; `UpdateTodoInput.position?: number`;
  `export function moveTodo(todos: Todo[], todoId: number, toCategoryId: number, toIndex: number): Todo[]` (spec §5.5)

- [ ] **Step 1: Failing testleri yaz** (`board.test.ts`)

```ts
// fixture (bu dizi sırasıyla): A(cat 1), B(cat 1), C(cat 1), D(cat 2), E(cat 2)
// col(todos, cat) = todos.filter(t => t.category_id === cat).map(t => t.title)
it('moves down within a column')        // moveTodo(f, A, 1, 2) → col 1 ['B', 'C', 'A']
it('moves up within a column')          // moveTodo(f, C, 1, 0) → col 1 ['C', 'A', 'B']
it('moves across columns to an index')  // moveTodo(f, B, 2, 1) → col 1 ['A', 'C'], col 2 ['D', 'B', 'E']; B.category_id 2
it('clamps an index past the end')      // moveTodo(f, A, 2, 99) → col 2 ['D', 'E', 'A']
it('renumbers positions to match the new order') // her todo'nun position'ı sütunundaki indeksine eşit
it('does not mutate its input')
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL (`./board` yok)

- [ ] **Step 3: Uygula** — saf fonksiyon; diğer sütunların göreli sırası korunur.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS

### Görev 5: İşaretleme sözleşmesi ve tema menüsü

**Files:**
- Create: `frontend/src/themes/index.ts`, `frontend/src/components/ThemeSwitcher.tsx`
- Create: `frontend/src/themes/{neubrutalism,glass,swiss,skeuo,zen}.css` — her biri yalnızca kendi kapsamında sayfa zemini ve metin rengi (spec §5.2'deki tonlar); tam stil Görev 8–12'de
- Modify: `frontend/src/index.css` (`@import "tailwindcss";` ardından 5 tema `@import`'u), `frontend/src/main.tsx` (render'dan önce `applyTheme(readStoredTheme())`), `frontend/index.html` (Google Fonts), `frontend/src/App.tsx`, `frontend/src/components/{AddTodoForm,Board,TodoCard}.tsx` (spec §5.3 sınıfları; Tailwind yalnızca yerleşim: grid/flex/gap/boşluk; adım 1'in renk/kenarlık/gölge/köşe sınıfları kaldırılır)
- Test: `frontend/src/themes/index.test.ts`, `frontend/src/App.test.tsx`

**Interfaces:**
- Produces:

```ts
// src/themes/index.ts
export const themes = [/* Global Constraints'teki 5 tema, sırayla */] as const
export type ThemeId = (typeof themes)[number]['id']
export const DEFAULT_THEME: ThemeId // 'neubrutalism'
export const THEME_STORAGE_KEY = 'todo-theme'
export function readStoredTheme(): ThemeId      // geçersiz/okunamayan kayıt → DEFAULT_THEME
export function applyTheme(id: ThemeId): void   // <html data-theme> + localStorage
// src/components/ThemeSwitcher.tsx — props yok; aktif temayı <html data-theme>'dan okur; themes.length < 2 ise null
export function ThemeSwitcher()
```

- [ ] **Step 1: Failing testleri yaz**

```ts
it('lists the five themes in order')           // labels ['Neubrutalizm', 'Glass', 'Swiss', 'Pano', 'Zen']
it('falls back to neubrutalism for a missing or unknown stored theme') // kayıt yok → 'neubrutalism'; 'bogus' → 'neubrutalism'; 'zen' → 'zen'
it('applyTheme sets data-theme on <html> and remembers it')            // applyTheme('swiss') → dataset.theme 'swiss', localStorage 'swiss'
it('switches themes from the menu')
// App render; getByRole('group', {name: 'Tasarım'}) içinde 5 tuş; 'Neubrutalizm' aria-pressed 'true';
// 'Zen' tıkla → html data-theme 'zen', localStorage 'zen', 'Zen' pressed, 'Neubrutalizm' 'false'
it('renders the markup contract')
// veri yüklendikten sonra: .board; 3 .column ve data-column '1','2','3'; .column-title; her sütunda .card-list;
// .card > .card-body > .card-title ve .card-select; boş sütunda .empty; .add-form, .add-input, .add-select, .add-button
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL

- [ ] **Step 3: Uygula** — menü `.app-header` içinde, yükleme/hata durumlarında da görünür.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS (adım 1 davranış testleri dahil)

### Görev 6: Sürükle-bırak ve iyimser taşıma

**Files:**
- Modify: `frontend/package.json` (`npm --prefix frontend install @hello-pangea/dnd`), `frontend/src/App.tsx`, `frontend/src/components/Board.tsx`, `frontend/src/components/TodoCard.tsx`
- Test: `frontend/src/App.test.tsx`

**Interfaces:**
- Consumes: Görev 4 `moveTodo`, `updateTodo`; Görev 5 işaretleme
- Produces: `Board` props `{ categories, todos, moving: boolean, onMove(todoId: number, toCategoryId: number, toIndex: number) }`; `TodoCard` props `{ todo, categories, disabled: boolean, onMove }`; `droppableId = String(category.id)`, `draggableId = String(todo.id)`

- [ ] **Step 1: Failing testleri yaz** (adım 1'in taşıma testleri bunlarla değişir; "keeps API order ... after a move" ve kart içi hata testi kaldırılır)

```ts
it('moves a card with its select to the top of the target column')
// todos: 'Fatura öde' (id 1, cat 1), 'Yeni' (id 2, cat 2); 'Fatura öde için sütun' → 'Yapılıyor'
// PATCH /api/todos/1 gövdesi toEqual({ category_id: 2, position: 0 }); 'Yapılıyor' listitem sırası ['Fatura öde', 'Yeni']
it('shows the move before the server answers')
// PATCH yanıtı bekletilir → kart zaten 'Yapılıyor' bölgesinde; sonra yanıtla
it('disables every card select while a move is saving')
// bekletilen PATCH sırasında tüm '... için sütun' seçicileri pasif; yanıttan sonra aktif
it('reverts the move and shows the error above the board on failure')
// todos: 'A' (cat 1), 'B' (cat 1) → 'B için sütun' → 'Tamamlandı'; PATCH 500 {"error":"boom"}
// alert 'Todo taşınamadı: boom'; 'Henüz yapılmadı' sırası birebir ['A', 'B']; 'B için sütun' toHaveValue('1')
it('keeps an empty column as a drop target')
// boş sütunun .card-list'i var ve 'Boş' onun içinde
```

- [ ] **Step 2: Testlerin başarısız olduğunu gör**

Run: `npm --prefix frontend test`
Expected: FAIL

- [ ] **Step 3: Uygula** — `DragDropContext` panoda; her `.card-list` bir `Droppable` (boşken de görünür, "Boş" içinde, `placeholder` dahil); her `.card` bir `Draggable` (tüm kart tutamak; `isDragDisabled={moving}`; `snapshot.isDragging` → `.is-dragging`, `snapshot.isDraggingOver` → `.is-drag-over`). `onDragEnd`: hedef yoksa veya aynı sütun + aynı sıraysa çık, değilse `onMove`. App'te `onMove`: önceki diziyi sakla → `moveTodo` ile yerel güncelle → `moving = true` → PATCH → başarıda dönen todo'yu yerine koy; hatada önceki diziyi geri yükle + hata mesajı → `moving = false`. Kart içindeki eski hata durumu kaldırılır.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`
Expected: hepsi PASS; StrictMode'da konsol uyarısı yok

- [ ] **Step 5: Teslim raporu** — dosyalar, TDD kanıtı, kurulan `@hello-pangea/dnd` sürümü, sapmalar. Commit'i orkestratör atar.

---

### Görev 7: Görsel kontrol aracı (orkestratör)

**Files:**
- Create: `<scratchpad>/e2e/shot.mjs` (proje dışında; `playwright-core` orada kurulu)

- [ ] **Step 1: Yaz** — `node shot.mjs <themeId> <out.png> [--drag] [--width 1280]`: Edge headless; `addInitScript` ile `localStorage['todo-theme'] = themeId`; `**/api/categories` ve `**/api/todos` istekleri sabit veriyle cevaplanır (3 kategori; sütun 1'de 2 kart, sütun 2'de 3 kart — biri çok uzun başlıklı, sütun 3'te 1 kart; Türkçe karakterler); `http://localhost:5173` yüklenir, sütun başlıkları beklenir, tam sayfa ekran görüntüsü alınır. `--drag`: bir kart fareyle tutulup başka sütunun üstüne getirilir, bırakmadan görüntü alınır (`.is-dragging` / `.is-drag-over` görünür), sonra Escape.

- [ ] **Step 2: Doğrula** — `node shot.mjs neubrutalism out.png` ve `--drag` çalışır, görüntüler açılır; gerçek backend'e istek gitmez.

---

## Tema agent'ları — Görev 8–12 (her biri yalnızca kendi CSS dosyası)

| Görev | Dosya | Tasarım (spec §5.2 satırı) |
|---|---|---|
| 8 | `frontend/src/themes/neubrutalism.css` | Neubrutalizm |
| 9 | `frontend/src/themes/glass.css` | Glass |
| 10 | `frontend/src/themes/swiss.css` | Swiss |
| 11 | `frontend/src/themes/skeuo.css` | Pano |
| 12 | `frontend/src/themes/zen.css` | Zen |

Her görev aynı adımları izler:

**Interfaces:**
- Consumes: Görev 5–6 işaretleme sözleşmesi (spec §5.3) ve durum sınıfları; Görev 7 aracı
- Produces: yalnızca kendi tema dosyası

- [ ] **Step 1: Tasarımı yaz** — spec §5.2 satırındaki felsefeyi sözleşmedeki her sınıf için uygula: sayfa zemini, başlık, menü tuşları (normal / `aria-pressed="true"` / hover / `:focus-visible`), form alanları ve buton (normal / hover / `:disabled`), pano, sütunlar (`data-column` ile ayrışma isteğe bağlı), `.card-list.is-drag-over`, kart ve `.card.is-dragging .card-body`, kart başlığı (uzun başlıkta taşma yok), kart seçicisi, `.empty`, `.alert`, `.status`. Dar ekranda (390px) da düzgün görünür.

- [ ] **Step 2: Kural kontrolü**

Run (dosya için):
- `grep -nE '^[^@}/[:space:]][^{]*\{' <dosya> | grep -v 'data-theme="<id>"'` → çıktı yok (kapsam dışı kural yok)
- `grep -n 'content:' <dosya>` → yalnızca `content: ""`
- `grep -n 'transform' <dosya>` → hiçbiri `.card` seçicisinin kendisinde değil

- [ ] **Step 3: Görsel kontrol** — `node <scratchpad>/e2e/shot.mjs <id> <png>` ve `--drag` ile; ayrıca `--width 390`. Görüntüleri açıp spec §5.2 tarifine ve okunurluğa göre değerlendir; gerekirse Step 1'e dön.

- [ ] **Step 4: Kontroller**

Run: `npm --prefix frontend run build`
Expected: PASS

- [ ] **Step 5: Teslim raporu** — dosya, 3 ekran görüntüsünün yolu, tasarım kararları. Commit'i orkestratör atar.

---

### Görev 13: Entegrasyon, README, teslim (orkestratör)

**Files:**
- Modify: `README.md` (Tasarımlar bölümü: menü, tema dosyaları, bir temayı silme adımları)

- [ ] **Step 1: Otomatik kontroller** — `go -C backend vet ./... && go -C backend test ./...`; `npm --prefix frontend run lint && npm --prefix frontend run build && npm --prefix frontend test` → hepsi PASS.
- [ ] **Step 2: Backend'i yeni kodla yeniden başlat** (geçici DB) ve API'yi proxy üzerinden doğrula: adım 1 kontrat betiği + `position` PATCH ve doğrulama vakaları.
- [ ] **Step 3: Uçtan uca (Edge, gerçek fare)** — her tema için: menüden geç, sütunlar arası sürükle, sütun içinde sırala, boş sütuna bırak, sayfayı yenile (tema ve sıra korunur), konsol temiz; bir tema için backend yeniden başlatma sonrası sıra korunur; her temanın ekran görüntüsü.
- [ ] **Step 4: Gerçek DB ile çalıştır** — backend varsayılan DB ile; migration 0002'nin mevcut veriye uygulandığını (`user_version = 2`) doğrula; sunucuları çalışır bırak.
- [ ] **Step 5: README, son genel inceleme, commit, kullanıcıya rapor (5 ekran görüntüsüyle).**
