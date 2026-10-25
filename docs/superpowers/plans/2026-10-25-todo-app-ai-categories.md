# Todo App Adım 3 — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Sütunları yapay zekânın (ücretsiz Gemini) niyetten belirlediği kategorilere çevirmek; kartlara durum ve açıklama eklemek; kısa aralıklı polling ve varsayılan gizli bir log paneli eklemek.

**Architecture:** Migration 0003 veri modelini dönüştürür. Backend'de `applog` (bellek içi log tamponu), `ai` (Gemini sınıflandırıcı, yalnızca `net/http`) ve `classify` (tek, sıralı işçi) paketleri eklenir; API yeni alanları, `classify` ve `logs` uç noktalarını sunar. Frontend 9 sütunlu yatay pano, kartta durum/açıklama/rozetler, ekleme formunda açıklama/durum, bekleyen kartlar için polling ve log panelini ekler. Pano teması yeni öğeleri stillendirir.

**Tech Stack:** Go 1.26 (`net/http`, `database/sql`, `log/slog`), `modernc.org/sqlite`, Gemini API (REST, ücretsiz katman); React 19 + TypeScript + Vite 8 + Tailwind v4, `@hello-pangea/dnd`; Vitest + Testing Library; `playwright-core` + Edge.

**Spec:** `docs/superpowers/specs/2026-10-25-todo-app-ai-categories-design.md` — plan bu spec'e dayanır; uygulayan herkes ikisini de okur. (Önceki adımlar: `2026-10-24-todo-app-foundation-design.md`, `2026-10-24-todo-app-designs-dnd-design.md`.)

## Global Constraints

- Branch `step-3-ai-categories`. Windows 11, Go 1.26.1 (CGO kapalı), Node 24, npm 11.
- **Yeni bağımlılık yok** (Go ve npm). Gemini çağrıları yalnızca `net/http` ile.
- Kategoriler birebir (key → ad, position): `inbox` → `Gelen kutusu` (0), `purchase` → `Satın alım` (1), `research` → `Araştırma` (2), `communication` → `İletişim` (3), `payment` → `Ödeme` (4), `appointment` → `Randevu & Etkinlik` (5), `work` → `İş` (6), `home` → `Ev & Kişisel` (7), `other` → `Diğer` (8). Açıklamalar spec §4 SQL'iyle birebir.
- Durum değerleri `todo` / `in_progress` / `done` → etiketler `Yapılacak` / `Devam ediyor` / `Tamamlandı`. Sınıflandırma değerleri `pending` / `done` / `failed` / `manual`.
- Ortam değişkenleri ve varsayılanlar: `GEMINI_API_KEY` (yok), `AI_MODEL` = `gemini-3.5-flash-lite`, `AI_TIMEOUT` = `20s`, `AI_MIN_INTERVAL` = `4s`, `GEMINI_BASE_URL` = `https://generativelanguage.googleapis.com`.
- Gemini isteği: `POST {GEMINI_BASE_URL}/v1beta/models/{AI_MODEL}:generateContent`, anahtar `x-goog-api-key` başlığında; `generationConfig.temperature = 0`, JSON şeması (`category` enum + `reason`). Alan adları https://ai.google.dev/gemini-api/docs/structured-output ve generateContent REST referansından doğrulanır.
- **API anahtarı** hiçbir log kaydında, hata mesajında, yanıtta, frontend'de veya git'te yer almaz.
- Yeni API mesajları birebir: `description must be a string`, `description must not be null`, `description must be at most 2000 characters`, `status must be one of todo, in_progress, done`, `status must not be null`, `invalid after`, `invalid limit`; POST'ta `category_id`/`position` → `unknown field "<alan>"`.
- Log mesajları spec §8 tablosuyla birebir (Türkçe tırnak `“…”`, süre `0,4` biçiminde).
- Arayüz metinleri spec §9.2 ve §9.5 ile birebir; işaretleme sınıfları spec §9.6 ile birebir (adım 2 sözleşmesi geçerli).
- Polling aralığı 1000 ms; yalnızca bekleyen kart varken (`GET /api/todos`) ve log paneli açıkken (`GET /api/logs`); aynı türden istekler üst üste binmez.
- Agent'lar yalnızca kendi yazma alanlarına yazar, git komutu çalıştırmaz, subagent başlatmaz, arkada süreç bırakmaz. Orkestratörün sunucuları (5173 Vite, 8080 Go, canlı DB `backend/data/`) durdurulmaz ve kullanılmaz; testler geçici DB ve sahte sunucularla çalışır. Gerçek Gemini çağrısı yalnızca orkestratörün adımlarında (Görev 14) yapılır.
- Sunucu doğrulamasında `go run` değil derlenmiş exe + geçici `DB_PATH` + 8080 dışı `ADDR`; curl'de ASCII başlık.

## Review Focus

1. **Yarış: kart Gelen kutusundan elle taşınırken yapay zekâ cevabı gelir** — cevap kartı geri taşımamalı. → Görev 2 store testi (`ApplyClassification` yalnızca inbox + `pending` iken), Görev 5 işçi testi, Görev 10 polling testi (taşıma kaydedilirken polling sonucu uygulanmaz).
2. **Kota / anahtar yok / bozuk cevap** — kart Gelen kutusunda `failed`, log ayrıntılı, işçi takılmaz ve döngüye girmez. → Görev 4 ve Görev 5 testleri.
3. **Gerçek veride migration** — adım 2 biçimindeki DB'de durum eşlemesi, Gelen kutusunda sıra kuralı, `user_version = 3`. → Görev 1 testi.
4. **Anahtarın sızmaması** — sahte sunucu 400/500 gövdesi döndürse bile log ayrıntısı ve hata metni anahtarı içermez. → Görev 4 ve Görev 5 testleri.
5. **Log paneli** — gizliyken istek yok; yalnızca ayrıntılı kayıt açılır; `after` ile kaldığı yerden devam eder; uzun mesaj tek satırda kalır. → Görev 11 testleri.

---

## Backend agent — Görev 1–7

### Görev 1: Migration 0003 ve todo/kategori alanları

**Files:**
- Create: `backend/internal/db/migrations/0003_ai_categories.sql` — spec §4 SQL'i birebir
- Modify: `backend/internal/db/todos.go`, `backend/internal/db/categories.go`
- Test: `backend/internal/db/db_test.go`, `backend/internal/db/todos_test.go`

**Interfaces:**
- Produces:
  - `type Category struct { ID int64 \`json:"id"\`; Key string \`json:"key"\`; Name string \`json:"name"\`; Position int64 \`json:"position"\`; Description string \`json:"-"\` }`
  - `Todo`'ya `Description string \`json:"description"\``, `Status string \`json:"status"\``, `Classification string \`json:"classification"\``
  - Sabitler: `InboxKey = "inbox"`; `StatusTodo/StatusInProgress/StatusDone`; `ClassificationPending/ClassificationDone/ClassificationFailed/ClassificationManual`
  - `CreateTodoParams{Title, Description, Status string; Metadata json.RawMessage}` (`CategoryID` kalkar; `Status` boşsa `todo`) → kart Gelen kutusunun en üstüne, `pending`
  - `UpdateTodoParams`'a `Description, Status *string`; farklı bir `CategoryID` → `classification = 'manual'`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestMigration0003ConvertsStep2Data(t *testing.T) {
	// raw := openDB(temp); 0001+0002'yi uygula (embedded dosyalardan MapFS)
	// adım 2 verisi: "A" (kat 1, pos 0), "B" (kat 1, pos 1), "C" (kat 2), "D" (kat 3)
	// tüm migration'ları uygula → user_version 3
	// ListCategories anahtarları sırayla [inbox purchase research communication payment appointment work home other]
	// durumlar: A,B todo; C in_progress; D done; hepsi inbox'ta, classification pending
	// inbox sırası [A, B, C, D] (eski sütun, sonra eski sıra); assertDensePositions
}
func TestCreateTodoGoesToInboxPending(t *testing.T) {
	// CreateTodo({Title: "Süt al", Description: "2 litre", Status: "done"}) → inbox, position 0,
	//   classification "pending", status "done", description "2 litre"
	// Status boş → "todo"
}
func TestUpdateTodoStatusDescriptionAndManual(t *testing.T) {
	// Status → "in_progress"; Description → "yeni"; kategori aynı kalınca classification değişmez
	// farklı CategoryID → classification "manual"; aynı CategoryID + Position → değişmez
}
```

Adım 1–2 testlerinden kategori id'lerine (1,2,3) ve `CreateTodoParams.CategoryID`'ye dayananlar yeni modele göre güncellenir.
Not: `CreateTodoParams.CategoryID` kalktığı için `api` paketi ve `cmd/server` Görev 6'ya kadar derlenmez; Görev 1–5'te
yalnızca ilgili paketin testleri çalıştırılır (`./internal/db/...`, `./internal/applog/...`, `./internal/ai/...`, `./internal/classify/...`).

- [ ] **Step 2: Testlerin başarısız olduğunu gör** — `go -C backend test ./internal/db/...` → FAIL
- [ ] **Step 3: Uygula** — tüm sorgular yeni kolonları okur/yazar; status/description doğrulaması çağıranda (API).
- [ ] **Step 4: Testlerin geçtiğini gör** — `go -C backend test ./internal/db/...` → PASS

### Görev 2: Sınıflandırma için store işlemleri

**Files:**
- Modify: `backend/internal/db/todos.go`
- Test: `backend/internal/db/todos_test.go`

**Interfaces:**
- Consumes: Görev 1
- Produces:
  - `func (s *Store) PendingTodoIDs(ctx context.Context) ([]int64, error)` — `pending`, `created_at, id` sırasıyla
  - `func (s *Store) ApplyClassification(ctx context.Context, id int64, categoryKey string, ai json.RawMessage) (Todo, bool, error)` — tek transaction: todo var **ve** `pending` **ve** inbox'ta ise hedef kategorinin en üstüne taşır, `classification = 'done'`, `metadata.ai = ai` (json_set) → `(todo, true, nil)`; koşul sağlanmazsa hiçbir şey değişmez → `(todo, false, nil)`; todo yoksa `ErrNotFound`; anahtar yoksa `ErrInvalidCategory`
  - `func (s *Store) FailClassification(ctx context.Context, id int64, ai json.RawMessage) (Todo, bool, error)` — yalnızca `pending` iken `failed` + `metadata.ai`
  - `func (s *Store) RequeueClassification(ctx context.Context, id int64) (Todo, error)` — inbox'ın en üstüne, `pending`, `metadata.ai` silinir; yoksa `ErrNotFound`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestApplyClassification(t *testing.T) {
	// pending inbox kartı → ApplyClassification(id, "purchase", `{"model":"m","category":"purchase","reason":"r","duration_ms":5,"at":"…"}`)
	//   → applied true; kategori purchase, position 0, classification "done"; metadata.ai == verilen; assertDensePositions
	// purchase'ta zaten bir kart varken yeni kart en üste gelir
	// kart önce elle research'e taşındıysa (manual) → applied false; kart research'te, classification "manual"
	// failed kart → applied false
	// olmayan id → ErrNotFound; olmayan anahtar → ErrInvalidCategory
}
func TestFailClassification(t *testing.T)     { /* pending → failed + metadata.ai.error; manual kartta değişiklik yok (false) */ }
func TestRequeueClassification(t *testing.T)  { /* done kart (purchase) → inbox top, pending, metadata.ai yok; ErrNotFound */ }
func TestPendingTodoIDs(t *testing.T)          { /* yalnız pending, en eski önce */ }
```

- [ ] **Step 2: FAIL gör** — `go -C backend test ./internal/db/...`
- [ ] **Step 3: Uygula** — taşıma, adım 2'deki sıra yardımcılarıyla (boşluğu kapat, yer aç).
- [ ] **Step 4: PASS gör** — `go -C backend test ./internal/db/...`

### Görev 3: Log tamponu (`applog`)

**Files:**
- Create: `backend/internal/applog/applog.go`
- Test: `backend/internal/applog/applog_test.go`

**Interfaces:**
- Produces: `Entry` (spec §8 birebir); sabitler `LevelInfo/LevelWarn/LevelError`, `SourceAPI/SourceAI/SourceSystem`;
  `func New(capacity int) *Buffer`; `func (b *Buffer) Add(level, source, message, details string) Entry` (ayrıca `slog`);
  `func (b *Buffer) After(after int64, limit int) (entries []Entry, lastSeq int64, truncated bool)`

- [ ] **Step 1: Failing testleri yaz**

```go
func TestAddAssignsIncreasingSeq(t *testing.T) // 1, 2, 3; Time UTC ve sıfır değil
func TestAfterAndLimit(t *testing.T)
// 10 kayıt: After(0, 200) → 10 kayıt eskiden yeniye, lastSeq 10, truncated false
// After(7, 200) → seq 8,9,10; After(0, 3) → seq 8,9,10 (en yeni 3), truncated true; After(10, 5) → boş, lastSeq 10
func TestCapacityDropsOldest(t *testing.T) // New(500); 501 Add → After(0, 500) seq 2..501
func TestConcurrentAdd(t *testing.T)       // 20 goroutine × 25 Add → 500 benzersiz seq, hata yok
```

- [ ] **Step 2: FAIL gör** — `go -C backend test ./internal/applog/...`
- [ ] **Step 3: Uygula**
- [ ] **Step 4: PASS gör**

### Görev 4: Gemini sınıflandırıcı (`ai`)

**Files:**
- Create: `backend/internal/ai/ai.go` (arayüz, tipler, hatalar, talimat), `backend/internal/ai/gemini.go`
- Test: `backend/internal/ai/gemini_test.go`

**Interfaces:**
- Produces:
  - `type Category struct{ Key, Name, Description string }`; `type Input struct{ Title, Description string; Categories []Category }`
  - `type Result struct{ Key, Reason, Model, Raw string; Duration time.Duration }`
  - `type Classifier interface{ Classify(ctx context.Context, in Input) (Result, error) }`
  - `var ErrBadResponse`; `type QuotaError struct{ RetryAfter string }`; `type HTTPError struct{ Status int; Body string }` (gövde kısaltılmış; anahtar asla)
  - `func BuildSystemPrompt(cats []Category) string` — spec §6.3 metni birebir
  - `type GeminiConfig struct{ APIKey, Model, BaseURL string; Timeout time.Duration }`; `func NewGemini(cfg GeminiConfig) *Gemini` (`Classifier`)

- [ ] **Step 1: Failing testleri yaz** (`httptest.Server` sahte Gemini)

```go
func TestGeminiRequestShape(t *testing.T)
// yol "/v1beta/models/gemini-3.5-flash-lite:generateContent"; başlık x-goog-api-key = "test-key"; URL'de "key=" yok
// gövdede: sistem talimatı BuildSystemPrompt(...) ile aynı; kullanıcı içeriği "Başlık: Süt al\nAçıklama: 2 litre"
//   (açıklama boşken "Açıklama" satırı yok); temperature 0; JSON MIME tipi; şemada category enum = 8 anahtar
//   (inbox yok) ve reason; ikisi zorunlu
func TestGeminiParsesResult(t *testing.T)
// cevap metni {"category":"purchase","reason":"Market alışverişi."} → Key "purchase", Reason, Model, Raw, Duration > 0
func TestGeminiErrors(t *testing.T)
// 429 + Retry-After: 30 → errors.As QuotaError{RetryAfter: "30"}; 500 → HTTPError{Status: 500}
// JSON olmayan metin → ErrBadResponse; listede olmayan "inbox"/"xyz" → ErrBadResponse
// zaman aşımı (sunucu Timeout'tan uzun bekler) → context deadline hatası
// hiçbir hata metninde ve HTTPError.Body'de "test-key" geçmez
func TestBuildSystemPrompt(t *testing.T) // kurallar 1–3 ve "- purchase (Satın alım): …" satırları position sırasıyla
```

- [ ] **Step 2: FAIL gör** — `go -C backend test ./internal/ai/...`
- [ ] **Step 3: Uygula** — istek gövdesi alan adlarını Gemini dokümanından doğrula (Global Constraints'teki bağlantılar); model düşünme ayarı destekliyorsa en düşük düzey.
- [ ] **Step 4: PASS gör**

### Görev 5: Sınıflandırma işçisi (`classify`) ve sunucu bağlantısı

**Files:**
- Create: `backend/internal/classify/worker.go`
- Test: `backend/internal/classify/worker_test.go`

**Interfaces:**
- Consumes: Görev 2 store işlemleri, Görev 3 `applog`, Görev 4 `ai`
- Produces:
  - `type Config struct{ Classifier ai.Classifier /* nil: anahtar yok */; Model string; Timeout, MinInterval time.Duration }`
  - `func NewWorker(store *db.Store, logs *applog.Buffer, cfg Config) *Worker`; `func (w *Worker) Enqueue(id int64)` (tekrarları ayıklar); `func (w *Worker) Run(ctx context.Context)` (açılışta `PendingTodoIDs`'i sıraya alır ve `<n> kart sınıflandırma kuyruğuna alındı` loglar; ctx bitince döner)
  - (Sunucuya bağlanması Görev 6'da.)

- [ ] **Step 1: Failing testleri yaz** (sahte `Classifier`, geçici DB, kısa aralıklar)

```go
func TestWorkerClassifiesPendingInOrder(t *testing.T)
// 3 pending kart; sahte classifier anahtar döndürür → hepsi hedef kategoride "done"; çağrı sırası en eski önce
// loglar: "Sınıflandırılıyor: “…”", "Sınıflandırıldı: “…” → Satın alım (0,0 sn)" + ayrıntıda model/giden metin/JSON/gerekçe
func TestWorkerRespectsMinInterval(t *testing.T)    // MinInterval 80ms → ardışık çağrılar arası ≥ 80ms
func TestWorkerWithoutClassifierFailsCards(t *testing.T) // nil classifier → kart failed, ayrıntı "API anahtarı yok"
func TestWorkerQuotaAndErrors(t *testing.T)
// QuotaError{RetryAfter:"30"} → failed, log error ayrıntısında "429" ve "30"; HTTPError ve ErrBadResponse → failed
// işçi sonraki karta devam eder; aynı kart için tekrar çağrı yapılmaz
func TestWorkerDoesNotOverrideManualMove(t *testing.T)
// classifier çağrısı sürerken kart elle başka kategoriye taşınır → sonuç uygulanmaz,
// "Öneri uygulanmadı: “…” (kart elle taşınmıştı)" (warn) loglanır
func TestWorkerNeverLogsAPIKey(t *testing.T)        // classifier hatası anahtarı içerse bile log ayrıntısında maskelenmiş/yok
func TestWorkerStopsOnContextCancel(t *testing.T)
```

- [ ] **Step 2: FAIL gör** — `go -C backend test ./internal/classify/...`
- [ ] **Step 3: Uygula** — `metadata.ai` spec §4 biçiminde; süre bir ondalık, Türkçe virgül.
- [ ] **Step 4: PASS gör**

### Görev 6: API değişiklikleri

**Files:**
- Modify: `backend/internal/api/api.go`, `backend/internal/api/todos.go`, `backend/cmd/server/main.go`
- Create: `backend/internal/api/logs.go`
- Test: `backend/internal/api/todos_test.go`, `backend/internal/api/logs_test.go`

**Interfaces:**
- Consumes: Görev 1–3, Görev 5 (`NewWorker`, `Enqueue`, `Run`)
- Produces: `type Enqueuer interface{ Enqueue(id int64) }`; `func New(store *db.Store, logs *applog.Buffer, queue Enqueuer) http.Handler`; uç noktalar spec §7; api logları spec §8;
  `main.go`: ortam değişkenleri (Global Constraints), `applog.New(500)`, anahtar yoksa `Classifier` nil + `Yapay zekâ kapalı: GEMINI_API_KEY tanımlı değil` (warn), `Sunucu başladı (model: <model>)`, işçiyi sunucunun ctx'iyle başlatır, `api.New(store, logs, worker)`; kapanışta önce HTTP sunucusu, sonra işçi, en son DB

- [ ] **Step 1: Failing testleri yaz**

```go
func TestCreateTodoStep3(t *testing.T)
// POST {"title":"Süt al","description":"  2 litre  ","status":"done"} → 201; inbox, pending, description "2 litre",
//   status "done"; queue.Enqueue(id) çağrıldı; log "Todo eklendi: “Süt al”"
// {"title":"x","category_id":2} → 400 `unknown field "category_id"`; {"title":"x","status":"bitti"} → 400 status mesajı
// description 2001 karakter → 400; description null → 400; description 5 → 400 "description must be a string"
func TestPatchTodoStep3(t *testing.T)
// status → "in_progress" + log "Durum değişti: “…” → Devam ediyor"; description → log "Açıklama güncellendi: “…”"
// farklı category_id → classification "manual" + log "Taşındı: “…” → Araştırma"; aynı kategori + position → "Sıra değişti: “…”"
// birden fazla alan → her biri ayrı log satırı
func TestClassifyEndpoint(t *testing.T) // 202 + todo (inbox, pending); Enqueue çağrıldı; log; 404; "abc" → 400 invalid id
func TestLogsEndpoint(t *testing.T)
// 3 kayıt → GET /api/logs → entries 3, last_seq 3, truncated false; ?after=2 → 1 kayıt; ?limit=2 → en yeni 2, truncated true
// ?after=-1 / ?after=x → 400 "invalid after"; ?limit=0 / 501 / x → 400 "invalid limit"
// GET istekleri (logs, todos) log tamponuna kayıt eklemez
func TestCategoriesHaveKeys(t *testing.T) // GET /api/categories → 9 kayıt, key ve name birebir, position sırasıyla
```

- [ ] **Step 2: FAIL gör** — `go -C backend test ./internal/api/...`
- [ ] **Step 3: Uygula**
- [ ] **Step 4: PASS gör**

### Görev 7: Model ölçüm aracı (`cmd/aibench`)

**Files:**
- Create: `backend/cmd/aibench/main.go`, `backend/internal/ai/testdata/benchmark.json`

**Interfaces:**
- Consumes: `db.Open` + `ListCategories` (geçici DB), `ai.NewGemini`
- Produces: `go run ./cmd/aibench` raporu (spec §6.6)

- [ ] **Step 1: Test setini yaz** — ~40 kayıt `{"title", "description"?, "expected"}`; her kategoriden ≥ 4; belirsiz / eksik bilgi → `research` örnekleri (ör. "hediye", "tatil", "laptop karşılaştır"); Türkçe karakterli.
- [ ] **Step 2: Aracı yaz** — anahtar yoksa açık bir mesajla çıkar (anahtarı yazdırmaz); `AI_MIN_INTERVAL` ile sırayla gönderir; her örnek için beklenen/bulunan/gerekçe, toplam isabet, kategori karışıklık tablosu, medyan süre.
- [ ] **Step 3: Kontroller** — `go -C backend vet ./... && go -C backend test ./... && gofmt -l backend` → PASS, boş. (Aracın gerçek çalıştırması Görev 14'te orkestratörde.)
- [ ] **Step 4: Teslim raporu** — commit'i orkestratör atar.

---

## Frontend core agent — Görev 8–11

### Görev 8: API istemcisi ve `placeOnTop`

**Files:**
- Modify: `frontend/src/api.ts`, `frontend/src/test/mockApi.ts` (`makeTodo`'ya `status: 'todo'`, `description: ''`, `classification: 'done'`; `seedCategories` → 9 kategori, key'leriyle)
- Modify: `frontend/src/board.ts`
- Test: `frontend/src/api.test.ts`, `frontend/src/board.test.ts`

**Interfaces:**
- Produces:

```ts
export type Status = 'todo' | 'in_progress' | 'done'
export type Classification = 'pending' | 'done' | 'failed' | 'manual'
export type Category = { id: number; key: string; name: string; position: number }
// Todo += description: string; status: Status; classification: Classification
export type CreateTodoInput = { title: string; description?: string; status?: Status; metadata?: Record<string, unknown> }
// UpdateTodoInput += description?: string; status?: Status
export function classifyTodo(id: number): Promise<Todo>            // POST /api/todos/{id}/classify
export type LogEntry = { seq: number; time: string; level: 'info' | 'warn' | 'error'; source: 'api' | 'ai' | 'system'; message: string; details?: string }
export function getLogs(after?: number, limit?: number): Promise<{ entries: LogEntry[]; last_seq: number; truncated: boolean }>
// board.ts
export function placeOnTop(todos: Todo[], todo: Todo): Todo[]      // id'yi çıkarır, kendi kategorisinin en üstüne koyar, position'ları yeniden numaralar
```

- [ ] **Step 1: Failing testleri yaz** — `classifyTodo` POST yolu; `getLogs()` → `/api/logs`, `getLogs(5, 200)` → `/api/logs?after=5&limit=200`; `placeOnTop`: inbox'tan purchase'ın en üstüne, diğer sütunlar etkilenmez, position'lar sütun içi indeks, girdi değişmez.
- [ ] **Step 2: FAIL gör** — `npm --prefix frontend test`
- [ ] **Step 3: Uygula**
- [ ] **Step 4: Kontroller** — `npm --prefix frontend test && npm --prefix frontend run build && npm --prefix frontend run lint`

### Görev 9: Pano, kart ve ekleme formu

**Files:**
- Modify: `frontend/src/components/Board.tsx`, `TodoCard.tsx`, `AddTodoForm.tsx`, `frontend/src/App.tsx`
- Test: `frontend/src/App.test.tsx` (ve gerekirse bileşen testleri)

**Interfaces:**
- Consumes: Görev 8
- Produces: spec §9.1, §9.2, §9.5, §9.6 (sınıflar, etiketler, metinler birebir)

- [ ] **Step 1: Failing testleri yaz**

```ts
it('renders 9 columns in order with Gelen kutusu first')          // heading'ler "Gelen kutusu (n)", ..., "Diğer (n)"; .column[data-key]
it('adds a todo with description and status')                      // "Açıklama ekle" → "Açıklama" alanı; Durum "Tamamlandı"
// POST gövdesi toEqual({ title: 'Süt al', description: '2 litre', status: 'done' }); kart Gelen kutusunun en üstünde, "sınıflandırılıyor…"
it('has no category selector in the add form')
it('changes a card status')                                        // "<başlık> için durum" → "Devam ediyor" → PATCH {status:'in_progress'}; .card[data-status]
it('shows an error and keeps the old status when the change fails') // "Durum değiştirilemedi: <detay>"
it('edits a description')                                          // "Açıklamayı düzenle" → "<başlık> açıklaması" → Kaydet → PATCH {description}; Vazgeç değişiklik göndermez
it('moves a card with its category selector to the top of the target') // "<başlık> için kategori" → PATCH {category_id, position: 0}
it('shows badges and retries a failed classification')
// pending → "sınıflandırılıyor…"; failed → "sınıflandırılamadı" + "<başlık> için tekrar dene" → POST /classify → kart Gelen kutusunun üstünde pending
```

Adım 2 testlerinden 3 sütuna, `Sütun` seçicisine ve `... için sütun` etiketine dayananlar yeni yapıya göre güncellenir.

- [ ] **Step 2: FAIL gör**
- [ ] **Step 3: Uygula** — pano yatay kaydırma kabı (`.board`); sürükle-bırak adım 2'deki gibi.
- [ ] **Step 4: Kontroller** — test, build, lint

### Görev 10: Bekleyen kartlar için polling

**Files:**
- Create: `frontend/src/usePendingPolling.ts`
- Modify: `frontend/src/App.tsx`
- Test: `frontend/src/App.polling.test.tsx`

**Interfaces:**
- Consumes: Görev 8 (`getTodos`, `placeOnTop`), App'in `moving` durumu
- Produces: spec §9.3 (bekleyen kartlar kısmı)

- [ ] **Step 1: Failing testleri yaz** (`vi.useFakeTimers()`)

```ts
it('polls every second while a card is pending and moves it when classified')
// ilk GET: kart inbox pending; 1 sn sonra GET: purchase done → kart "Satın alım" sütununun en üstünde, rozet yok
it('stops polling when nothing is pending')                        // sınıflandıktan sonra 3 sn ilerlet → yeni GET yok
it('keeps a failed card in Gelen kutusu with its badge')
it('does not touch cards that were not pending')                   // diğer kartların yerel sırası korunur
it('never overlaps requests')                                      // GET bekletilir; 3 sn ilerlet → tek istek
it('does not apply a polling result while a move is being saved')  // Review Focus #1
```

- [ ] **Step 2: FAIL gör**
- [ ] **Step 3: Uygula**
- [ ] **Step 4: Kontroller** — test, build, lint

### Görev 11: Log paneli

**Files:**
- Create: `frontend/src/components/LogOverlay.tsx`
- Modify: `frontend/src/App.tsx`
- Test: `frontend/src/components/LogOverlay.test.tsx`

**Interfaces:**
- Consumes: Görev 8 (`getLogs`, `LogEntry`)
- Produces: spec §9.4 (sınıflar §9.6)

- [ ] **Step 1: Failing testleri yaz** (`vi.useFakeTimers()`)

```ts
it('is hidden by default and makes no request')                    // "Loglar" düğmesi aria-expanded "false"; /api/logs çağrılmadı
it('opens, loads recent logs and polls with after')                // tıkla → GET /api/logs?limit=200; 1 sn → ?after=<last_seq>
it('shows each entry on one line: time, source, message')          // "12:00:05", "ai"/"api"/"sistem", mesaj; .log-entry[data-level]
it('expands and collapses only entries that have details')         // ayrıntılı kayıt düğme, aria-expanded; tıklayınca .log-details görünür; ayrıntısız kayıt düğme değil
it('hides with Gizle and stops polling')
it('shows "Henüz log yok." when empty')
```

- [ ] **Step 2: FAIL gör**
- [ ] **Step 3: Uygula** — en yeni altta; kullanıcı en alttayken yeni kayıtta aşağı kaydır; panel `role="log"`.
- [ ] **Step 4: Kontroller** — test, build, lint
- [ ] **Step 5: Teslim raporu** — commit'i orkestratör atar.

---

### Görev 12: Pano temasının yeni öğeler için genişletilmesi (tema agent'ı)

**Files:**
- Modify: `frontend/src/themes/skeuo.css` (yalnızca bu dosya)

**Interfaces:**
- Consumes: Görev 9–11 işaretlemesi (spec §9.6), Görev 13 aracı
- Produces: yatay mantar pano (9 sütun), kartta açıklama/durum/kategori seçicileri, rozetler ve Tekrar dene, Tamamlandı kartın soluk + üstü çizili görünümü, ekleme formunda açıklama/durum, log paneli ve düğmesi (Pano havasında: klipsli not defteri/kâğıt)

- [ ] **Step 1: Tasarımı yaz** — adım 2 tema kuralları geçerli (kapsam `[data-theme="skeuo"]`, `content` ile metin yok, `.card`'a `transform` yok, kart atalarında containing block yok; `.board` yatay `overflow-x: auto` serbest).
- [ ] **Step 2: Kural kontrolü** — adım 2 Görev 8–12 Step 2 grep'leri.
- [ ] **Step 3: Görsel kontrol** — `shot.mjs skeuo` (masaüstü, `--drag`, `--width 390`, `--logs`); görüntüleri aç, değerlendir.
- [ ] **Step 4: Build** — `npm --prefix frontend run build` → PASS
- [ ] **Step 5: Teslim raporu**

---

## Orkestratör — Görev 13–14

### Görev 13: Görsel kontrol aracının güncellenmesi (Görev 12'den önce)

- [ ] `shot.mjs`: sahte API'yi adım 3 şekline getir (9 kategori `key`'leriyle; todo'larda `status`, `description`, `classification` — en az bir `pending`, bir `failed`, bir `done` durumlu kart; `GET /api/logs` için ayrıntılı ve ayrıntısız örnek kayıtlar); `--logs` bayrağı paneli açıp bir ayrıntılı kaydı genişletir; bekleyen kart için `GET /api/todos` sabit kalır (polling görüntüyü değiştirmez).

### Görev 14: Entegrasyon, ölçüm, README, teslim

- [ ] **Step 1: Otomatik kontroller** — `go vet`, `go test`, `gofmt`; `npm run lint/build/test`.
- [ ] **Step 2: Canlı DB yedeği** ve yeni kodla geçici kopya üzerinde migration doğrulaması (`user_version = 3`, "bakkala gitmek" → Tamamlandı + Gelen kutusu).
- [ ] **Step 3: Anahtar** — kullanıcının `GEMINI_API_KEY`'i (kullanıcı ortam değişkeni) ekrana basılmadan sürece aktarılır; yoksa kullanıcıdan istenir (sohbete yazılmadan).
- [ ] **Step 4: Ölçüm** — `go run ./cmd/aibench`; isabet ≥ %85 değilse açıklamalar iyileştirilip (0003 içinde, canlı DB'ye uygulanmadan önce) tekrar.
- [ ] **Step 5: Uçtan uca (Edge, gerçek Gemini, geçici DB)** — spec §10 son madde ve §11.
- [ ] **Step 6: Canlı çalıştırma** — canlı DB yedeklendikten sonra yeni kodla backend; açılışta sınıflandırma; sunucular çalışır kalır.
- [ ] **Step 7: README** (yapay zekâ ayarları, anahtar kurulumu, log paneli), son genel inceleme, commit, rapor.
