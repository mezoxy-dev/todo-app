# Todo App — Adım 1: Temel Altyapı (Tasarım Spec'i)

- **Tarih:** 2026-10-24
- **Durum:** Tasarım sohbette bölüm bölüm onaylandı; bu yazılı spec kullanıcı incelemesinde
- **Kapsam:** SQLite + Go API + temel React arayüzü, uçtan uca birlikte çalışır halde

## 1. Amaç

Todo'ları toplamak, kategorilere ayırmak ve arayüzden yönetmek için tek kullanıcılı, yerelde
çalışan bir uygulama. Proje adım adım büyüyecek: her adımda önce işlevsellik gelir ve her çekirdek
özellik doğrulanarak ilerlenir.

**Bu adımın hedefi:** Veritabanı, API ve temel arayüz ayakta ve birlikte çalışıyor. Uygulama
açılınca (giriş/kimlik doğrulama yok) kategori sütunlarından oluşan pano ve todo'lar doğrudan
görünür. Arayüzden istenen sütuna todo eklenebilir ve kartlar sütunlar arasında taşınabilir.

## 2. Kararlar

| # | Karar | Gerekçe |
|---|---|---|
| K1 | Kategoriler = panodaki sütunlar. Başlangıçta 3 kategori: "Henüz yapılmadı", "Yapılıyor", "Tamamlandı". | Kullanıcı isteği. |
| K2 | Her todo tam olarak bir kategoridedir (`todos.category_id NOT NULL`). | Sütunlar birbirini dışlar; bir todo aynı anda iki sütunda olamaz. İlk soruda "birden çok kategori" seçilmişti; kategorilerin sütun olduğu netleşince kullanıcı DB kararını bize bıraktı ve bu gerekçeyle tekli yapıya geçildi. Etiket gibi çoklu sınıflandırma gerekirse ileride ayrı bir "tags" yapısı eklenir. |
| K3 | `completed` alanı yok. | "Tamamlandı" sütunu bu bilgiyi taşır; iki ayrı kaynak birbiriyle çelişebilirdi. |
| K4 | Kategoriler tabloda veri olarak durur; arayüz sütunları API'den okur. | Sütun eklemek veya yeniden adlandırmak kod değişikliği gerektirmez. |
| K5 | Her todo'da `metadata` alanı: JSON nesnesi, varsayılan `{}`. | Kullanıcı isteği; ileride eklenecek alanlar için esneklik. Bu adımda arayüzde kullanılmaz. |
| K6 | Yaklaşım A: şemayı Go yönetir (gömülü SQL migration'lar açılışta otomatik uygulanır), `database/sql` + elle yazılmış sorgular, standart `net/http`, geliştirmede Vite proxy. | Ek araç kurulumu yok, Windows'ta sorunsuz, şema adım adım büyütülebilir. |
| K7 | SQLite sürücüsü: `modernc.org/sqlite` (saf Go). | Makinede gcc yok ve CGO kapalı; `mattn/go-sqlite3` derlenemez. |
| K8 | Frontend TypeScript; arayüz metinleri Türkçe. API hata mesajları İngilizce. | Kullanıcı Türkçe kullanıyor; API mesajları geliştiriciye yöneliktir. |
| K9 | Go sunucusu `127.0.0.1:8080` adresini dinler; Vite proxy hedefi `http://127.0.0.1:8080`. | Windows güvenlik duvarı uyarısı çıkmaz; `localhost` → `::1` çözümleme sorunları yaşanmaz. |
| K10 | Git: yerel repo, adım adım commit, push yok. | Kullanıcı onayı. |

## 3. Kapsam

**Bu adımda var:**
- SQLite şeması (kategoriler + todo'lar), otomatik migration, 3 başlangıç kategorisi
- Go REST API: kategorileri listeleme; todo listeleme, oluşturma, getirme, güncelleme, silme
- Arayüz: 3 sütunlu pano, istenen sütuna todo ekleme, kartı sütunlar arasında taşıma
- Otomatik testler (Go + frontend) ve uçtan uca doğrulama

**Bu adımda yok (sonraki adımlar):**
- Kategori ekleme, yeniden adlandırma, silme (API ve arayüz)
- Arayüzde todo silme, başlık düzenleme, sürükle-bırak, sütun içinde elle sıralama
- Metadata'nın arayüzde gösterilmesi veya düzenlenmesi
- Kimlik doğrulama, dağıtım, tek binary, etiketler

## 4. Mimari

```
Tarayıcı ──> Vite dev server (localhost:5173)
               │  /api/* isteklerini proxy'ler
               v
             Go API (127.0.0.1:8080) ──> SQLite (backend/data/todos.db)
```

**Dizin yapısı ve sahiplik:**

```
claude testing/
├─ README.md                        [orkestratör] çalıştırma komutları
├─ .gitignore                       [orkestratör]
├─ docs/superpowers/{specs,plans}/  [orkestratör]
├─ backend/                         Go modülü "todoapp" (go.mod'u orkestratör oluşturur)
│  ├─ internal/db/                  [SQL agent]
│  │  ├─ migrations/0001_init.sql
│  │  └─ *.go, *_test.go
│  ├─ internal/api/                 [API agent]
│  ├─ cmd/server/main.go            [API agent]
│  └─ data/                         çalışınca oluşur, git'e girmez
└─ frontend/                        [Frontend agent]
```

**Sahiplik kuralları:**
- Her agent yalnızca kendi dizinine yazar. Başka bir dizinde değişiklik gerekiyorsa kendisi
  yapmaz, raporunda orkestratöre bildirir.
- `go.mod` / `go.sum`: SQL agent `modernc.org/sqlite` bağımlılığını ekler. API agent yeni
  bağımlılık eklemez (yalnızca standart kütüphane).
- Agent'lar git komutu çalıştırmaz (paralel çalışırken `.git/index.lock` çakışmasın diye).
  Tüm commit'leri orkestratör atar.

## 5. Veritabanı (SQL agent)

### 5.1 Şema — `backend/internal/db/migrations/0001_init.sql`

```sql
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
```

### 5.2 Bağlantı

- `Open(path)`: `path`'in üst klasörü yoksa oluşturur; dosya yoksa SQLite oluşturur.
- Havuzdaki **her** bağlantıda `foreign_keys = ON`, `journal_mode = WAL`, `busy_timeout = 5000`
  açık olur (modernc DSN'inde `_pragma` parametreleriyle). SQLite'ta foreign key kontrolü
  varsayılan olarak kapalıdır; açılmazsa `category_id` doğrulaması garanti edilmez.
- Eşzamanlı isteklerde "database is locked" hatası olmamalı.
- Windows'ta göreli (`data/todos.db`) ve mutlak yollarla çalışmalı.
- Zaman damgaları yalnızca SQL tarafında `strftime('%Y-%m-%dT%H:%M:%fZ','now')` ile üretilir
  (insert'te kolon varsayılanı, güncellemede `updated_at = strftime(...)`). Go zaman damgası
  yazmaz. Sabit genişlikli, milisaniyeli bu biçim sayesinde metin sıralaması kronolojik sırayla
  aynı olur.

### 5.3 Migration'lar

- `migrations/*.sql` dosyaları `embed` ile programa gömülür. Ad biçimi `NNNN_aciklama.sql`;
  sürüm numarası sayısal önektir.
- Açılışta `PRAGMA user_version` okunur. Bu değerden büyük sürümlü dosyalar sırayla, her biri
  tek bir transaction içinde uygulanır ve `user_version` o sürüme ayarlanır.
- Tekrar açılışta uygulanmış migration yeniden çalışmaz. Hatalı bir migration'da transaction
  geri alınır ve `Open` hata döner.

### 5.4 Go arayüzü (SQL agent ↔ API agent sözleşmesi)

```go
package db // import "todoapp/internal/db"

var (
	ErrNotFound        = errors.New("not found")        // todo bulunamadı
	ErrInvalidCategory = errors.New("invalid category") // category_id mevcut değil
)

type Category struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Position int64  `json:"position"`
}

type Todo struct {
	ID         int64           `json:"id"`
	Title      string          `json:"title"`
	CategoryID int64           `json:"category_id"`
	Metadata   json.RawMessage `json:"metadata"`   // her zaman geçerli bir JSON nesnesi
	CreatedAt  time.Time       `json:"created_at"` // UTC
	UpdatedAt  time.Time       `json:"updated_at"` // UTC
}

type CreateTodoParams struct {
	Title      string          // çağıran doğrular (kırpılmış, 1–500 karakter)
	CategoryID *int64          // nil → en küçük position'lı kategori ("Henüz yapılmadı")
	Metadata   json.RawMessage // nil veya boş → {}
}

// nil alan = değişmez. En az bir alanın dolu olduğunu çağıran doğrular.
type UpdateTodoParams struct {
	Title      *string
	CategoryID *int64
	Metadata   json.RawMessage
}

func Open(path string) (*Store, error)
func (s *Store) Close() error
func (s *Store) ListCategories(ctx context.Context) ([]Category, error)                     // position, id sırasıyla
func (s *Store) ListTodos(ctx context.Context) ([]Todo, error)                              // created_at DESC, id DESC; boşsa nil değil boş slice
func (s *Store) GetTodo(ctx context.Context, id int64) (Todo, error)                        // yoksa ErrNotFound
func (s *Store) CreateTodo(ctx context.Context, p CreateTodoParams) (Todo, error)           // geçersiz kategori: ErrInvalidCategory
func (s *Store) UpdateTodo(ctx context.Context, id int64, p UpdateTodoParams) (Todo, error) // önce ErrNotFound, sonra ErrInvalidCategory kontrolü; updated_at güncellenir
func (s *Store) DeleteTodo(ctx context.Context, id int64) error                             // yoksa ErrNotFound
```

- Dönen hatalar `errors.Is(err, db.ErrNotFound)` ve `errors.Is(err, db.ErrInvalidCategory)`
  ile kontrol edilebilir olmalı.
- `Store` yapısının alanları paket içine kapalıdır (`*sql.DB` tutar).

### 5.5 Testler (gerçek geçici SQLite dosyası, `t.TempDir()`)

- `Open` olmayan klasörü ve dosyayı oluşturuyor; migration sonrası `user_version = 1`;
  `ListCategories` 3 kategoriyi doğru sırayla döndürüyor.
- Aynı dosya ikinci kez açılınca migration tekrar çalışmıyor (hâlâ 3 kategori, `user_version = 1`).
- Mutlak yolla (`t.TempDir()`) ve göreli yolla (`t.Chdir` sonrası `data/todos.db`) açılabiliyor.
- Havuzdaki bağlantılarda `PRAGMA foreign_keys` değeri 1.
- Eşzamanlı 20 goroutine'den `CreateTodo` hatasız tamamlanıyor ("database is locked" yok).
- `CreateTodo`: kategori verilmezse "Henüz yapılmadı"ya düşüyor; verilen kategoriye ekliyor;
  olmayan kategoride `ErrInvalidCategory`; metadata verilmezse `{}`, verilirse aynen geri dönüyor.
- `ListTodos`: en yeni üstte; boş veritabanında nil olmayan boş slice.
- `GetTodo` / `DeleteTodo`: olmayan id'de `ErrNotFound`; silinen todo artık getirilemiyor.
- `UpdateTodo`: yalnızca başlık / yalnızca kategori / yalnızca metadata güncellenebiliyor;
  verilmeyen alanlar değişmiyor; `updated_at` ilerliyor (milisaniye çözünürlüğü nedeniyle testte
  kısa bir bekleme kullanılabilir); olmayan todo'da `ErrNotFound`;
  olmayan kategoride `ErrInvalidCategory`.
- DB kısıtları: doğrudan SQL ile `metadata = '[]'` eklenemiyor; içinde todo olan kategori
  silinemiyor (`RESTRICT`).

## 6. HTTP API (API agent)

### 6.1 Uç noktalar

| Method | Yol | Gövde | Başarılı | Hata |
|---|---|---|---|---|
| GET | `/api/health` | yok | 200 `{"status":"ok"}` | |
| GET | `/api/categories` | yok | 200 `Category[]` (`position` sırasıyla) | |
| GET | `/api/todos` | yok | 200 `Todo[]` (en yeni üstte; boşsa `[]`) | |
| POST | `/api/todos` | `title` (zorunlu), `category_id` ve `metadata` (isteğe bağlı) | 201 `Todo` | 400 |
| GET | `/api/todos/{id}` | yok | 200 `Todo` | 400, 404 |
| PATCH | `/api/todos/{id}` | `title`, `category_id`, `metadata` alanlarından en az biri | 200 `Todo` | 400, 404 |
| DELETE | `/api/todos/{id}` | yok | 204 (gövdesiz) | 400, 404 |

### 6.2 JSON şekilleri

```json
// Category
{ "id": 1, "name": "Henüz yapılmadı", "position": 1 }

// Todo
{ "id": 7, "title": "Süt al", "category_id": 3, "metadata": {},
  "created_at": "2026-10-24T12:00:00.123Z", "updated_at": "2026-10-24T12:00:00.123Z" }

// Hata
{ "error": "title is required" }
```

- Zaman damgaları UTC, RFC3339 biçiminde metin (kesirli saniye olabilir).
- `metadata` yanıtta her zaman bir JSON **nesnesi** olarak döner (metin içine gömülü JSON değil).

### 6.3 Doğrulama ve hatalar

- `title`: `strings.TrimSpace` ile kırpılır ve kırpılmış hali saklanır. Uzunluk 1–500 **karakter**
  (byte değil, rune). POST'ta zorunlu; PATCH'te gönderilirse aynı kurallar geçerli.
- `category_id`: Gönderilirse var olan bir kategori olmalı, aksi halde 400. POST'ta gönderilmezse
  todo en küçük `position`'lı kategoriye ("Henüz yapılmadı") düşer.
- **Taşıma:** `PATCH /api/todos/{id}` + `{"category_id": 3}`. `updated_at` güncellenir.
- `metadata`: JSON nesnesi olmalı (dizi, metin, sayı → 400). POST'ta gönderilmezse `{}`. PATCH'te
  gönderilirse eskisinin yerine komple geçer (birleştirme yok).
- Hiçbir alanda `null` kabul edilmez → 400. Gönderilmeyen alan değişmez.
- PATCH gövdesinde hiç alan yoksa (`{}`) → 400.
- Bilinmeyen alan, bozuk JSON, 1 MB'tan büyük gövde → 400.
- `{id}` pozitif tam sayı değilse → 400. Bulunamazsa → 404.
- Beklenmeyen hata → 500 ve `{"error":"internal server error"}`. Ayrıntı loglanır, istemciye verilmez.
- Kontrat dışı yollar (bilinmeyen yol 404, yanlış method 405) Go'nun varsayılan yanıtıyla dönebilir.

### 6.4 Sunucu ve ayarlar

- `api.New(store *db.Store) http.Handler` — Go 1.22+ method desenleriyle kayıtlı rotalar
  (ör. `GET /api/todos/{id}`). Yalnızca standart kütüphane.
- JSON yanıtlarında `Content-Type: application/json; charset=utf-8`.
- Basit istek logu (method, yol, durum kodu, süre) — `log/slog`.
- `cmd/server/main.go`: ortam değişkenleri `ADDR` (varsayılan `127.0.0.1:8080`) ve `DB_PATH`
  (varsayılan `data/todos.db`, çalışma dizinine göre). Açılışta adres ve DB yolu loglanır.
  Ctrl+C / SIGTERM gelince `http.Server.Shutdown` ve `store.Close()` ile düzgün kapanır.
- Çalıştırma: `cd backend && go run ./cmd/server`

### 6.5 Testler (`httptest` + gerçek geçici SQLite)

- `health`; `categories` sırası; boş DB'de `GET /api/todos` gövdesi `[]` (`null` değil).
- POST: varsayılan kategori; verilen kategori; metadata ile; 201 ve dönen gövde kontrata uygun.
- POST hataları: başlık yok, yalnızca boşluk, 500 karakteri aşan, olmayan kategori, nesne olmayan
  metadata, `null` alan, bilinmeyen alan, bozuk JSON → 400 ve `{"error": ...}`.
- GET/PATCH/DELETE `{id}`: başarılı yol, olmayan id → 404, geçersiz id → 400.
- PATCH: başlık, taşıma (`category_id`), metadata değişimi; boş gövde → 400; olmayan kategori → 400.
- DELETE sonrası GET → 404.

## 7. Frontend (Frontend agent)

### 7.1 Kurulum

- `frontend/`: Vite + React + TypeScript (`react-ts` şablonu), Tailwind CSS v4
  (`@tailwindcss/vite` eklentisi, CSS'te `@import "tailwindcss";`).
- `vite.config.ts`: `server.port = 5173`, `strictPort: true`,
  `server.proxy['/api'] = 'http://127.0.0.1:8080'`.
- Ek runtime kütüphanesi yok (router, state yönetimi, UI kit yok). Test için: Vitest,
  @testing-library/react, @testing-library/user-event, @testing-library/jest-dom, jsdom.
- Script'ler: `dev`, `build` (tip kontrolü + vite build), `test` (vitest run), `lint`.

### 7.2 Dosyalar

- `src/api.ts` — tipler (`Category`, `Todo`) ve `getCategories`, `getTodos`, `createTodo`,
  `updateTodo`. Hatalı yanıtta API'nin `{"error"}` mesajını taşıyan bir `Error` fırlatır; ağ
  hatasında da `Error` fırlatır.
- `src/App.tsx` — veri yükleme ve durum.
- `src/components/` — ekleme formu, pano/sütun ve kart bileşenleri (küçük, tek amaçlı dosyalar).

### 7.3 Davranış

1. **Açılış:** `getCategories()` ve `getTodos()` paralel çağrılır; yüklenirken "Yükleniyor..." görünür.
2. **Pano:** Her kategori `position` sırasıyla bir sütundur. Başlıkta ad ve sayı görünür, ör.
   "Tamamlandı (2)". Sütun içindeki sıra API'den geldiği gibidir (en yeni üstte). Boş sütunda "Boş" yazar.
3. **Ekleme formu** (panonun üstünde): başlık alanı (placeholder "Yeni todo yaz..."), sütun
   seçici (varsayılan: ilk sütun, "Henüz yapılmadı") ve "Ekle" butonu. Enter ile de gönderilir.
   Kırpılmış başlık boşsa ya da istek sürüyorsa buton pasiftir. Başarılı olunca yeni todo listenin
   başına eklenir (seçilen sütunda en üstte görünür), başlık alanı temizlenir, seçili sütun korunur.
4. **Kart:** başlık ve sütun seçici (mevcut kategori seçili). Seçim değişince
   `updateTodo(id, { category_id })` çağrılır; istek sürerken seçici pasiftir. Başarılı olunca
   yerel todo sunucudan dönenle değiştirilir ve kart yeni sütunda görünür.
5. Ekran yalnızca sunucu yanıtından sonra güncellenir (iyimser güncelleme yok). Böylece ekranda
   görülen her zaman DB'deki durumdur.
6. **Hatalar** Türkçe ve görünürdür (`role="alert"`):
   - Açılış hatası: "Sunucuya ulaşılamadı." ve "Tekrar dene" butonu
   - Ekleme hatası: "Todo eklenemedi: <detay>" — form içeriği korunur
   - Taşıma hatası: "Todo taşınamadı: <detay>" — kart eski sütununda kalır
7. **Etiketler:** Başlık alanı ve seçicilerin erişilebilir etiketleri vardır (ör. "Todo başlığı",
   "Sütun", kartta "<başlık> için sütun"). Bu, testlerde de kullanılır.
8. **Yerleşim:** Geniş ekranda 3 sütun yan yana, dar ekranda alt alta. Sade Tailwind stili;
   görsel tasarım bu adımın odağı değil.

### 7.4 Testler (Vitest + React Testing Library, `fetch` taklit edilir)

- Sütunlar API'deki kategorilere göre çiziliyor; todo'lar doğru sütunda.
- Varsayılan sütuna ekleme ve "Tamamlandı" seçilerek doğrudan o sütuna ekleme (POST gövdesi doğru).
- Kırpılmış başlık boşken "Ekle" pasif.
- Kart taşıma: PATCH gövdesi `{"category_id": X}`; kart yeni sütunda görünüyor.
- Açılış hatasında mesaj görünüyor ve "Tekrar dene" yeniden yüklüyor; ekleme ve taşıma hatalarında mesaj görünüyor.

## 8. Orkestrasyon

Toplam **3 subagent** (SQL, Go API, Frontend). Kod incelemesini ve entegrasyonu ana thread
(orkestratör) yapar.

1. **Faz 0 — Orkestratör:** `git init`, `.gitignore`, `README.md`, `backend/go.mod`
   (`module todoapp`), spec ve planın commit'i.
2. **Faz 1 — Paralel:** SQL agent (`backend/internal/db`) ve Frontend agent (`frontend/`).
   Frontend yalnızca bu spec'teki API kontratına ihtiyaç duyar, kimseyi beklemez.
3. **Faz 2:** SQL agent bitince orkestratör çıktıyı inceler (testler, sözleşmeye uygunluk) ve
   commit'ler. Ardından API agent (`backend/internal/api`, `backend/cmd/server`) gerçek `db`
   paketinin üzerine kurar.
4. **Faz 3:** Frontend ve API agent teslimleri incelenip commit'lenir. Düzeltme gerekirse aynı
   agent'a geri gönderilir (yeni agent açılmaz).
5. **Faz 4 — Entegrasyon doğrulaması** (bölüm 9), son commit, kullanıcıya rapor. Sunucular
   çalışır halde bırakılır.

## 9. Doğrulama ve kabul kriterleri

**Otomatik:** `go vet ./...` ve `go test ./...` (backend); `npm run lint`, `npm run build`,
`npm test` (frontend) hatasız geçer.

**API (curl, Vite proxy üzerinden `http://localhost:5173/api/...`):** tüm uç noktalar, başarılı ve
hatalı durumlar (400/404) kontrata uyar.

**Tarayıcı (headless, kurulu Edge + `playwright-core`; proje dışında geçici klasörde):**
1. `cd backend && go run ./cmd/server` → `backend/data/todos.db` oluşur, 3 kategori vardır,
   sunucu `127.0.0.1:8080` adresinde dinler.
2. `cd frontend && npm run dev` → http://localhost:5173 açılınca 3 sütun görünür.
3. Varsayılan sütunla eklenen todo "Henüz yapılmadı"da görünür; "Tamamlandı" seçilerek eklenen
   todo doğrudan "Tamamlandı"da görünür.
4. Karttaki seçiciyle taşınan todo yeni sütunda görünür.
5. Sayfa yenilenince ve backend yeniden başlatılınca aynı durum görünür.

## 10. Ortam notları

- Windows 11; Go 1.26.1, Node 24.18, npm 11.16, git 2.52.
- CGO kapalı, gcc yok → `modernc.org/sqlite`.
- `sqlite3` CLI yok → DB doğrulaması Go testleriyle yapılır.
- Headless doğrulama için kurulu Microsoft Edge kullanılır (`channel: 'msedge'`); ek tarayıcı indirilmez.
