# Todo App

Todo'ları toplamak, kategorilere (panodaki sütunlara) ayırmak ve arayüzden yönetmek için
yerelde çalışan, tek kullanıcılı bir uygulama. Giriş yok: açınca pano doğrudan gelir.

- **Backend:** Go (standart `net/http`) + SQLite (`modernc.org/sqlite`, CGO gerekmez)
- **Frontend:** React + TypeScript + Vite + Tailwind CSS v4

## Gereksinimler

- Go 1.26+
- Node.js 24+ ve npm 11+

## Çalıştırma

İki ayrı terminalde:

```bash
# 1) API — http://127.0.0.1:8080
#    İlk açılışta backend/data/todos.db ve 3 başlangıç kategorisi oluşturulur.
cd backend
go run ./cmd/server

# 2) Arayüz — http://localhost:5173
cd frontend
npm install   # yalnızca ilk sefer
npm run dev
```

Ardından tarayıcıda **http://localhost:5173** adresini aç. Vite, `/api` isteklerini Go
sunucusuna iletir; sunucuyu `Ctrl+C` ile kapatabilirsin.

### Backend ayarları (ortam değişkenleri)

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `ADDR` | `127.0.0.1:8080` | API'nin dinlediği adres. Değiştirirsen `frontend/vite.config.ts` içindeki proxy hedefini de güncelle. |
| `DB_PATH` | `data/todos.db` | SQLite dosyasının yolu (çalışma dizinine göre). Klasör yoksa oluşturulur. |

## Testler

```bash
cd backend && go vet ./... && go test ./...
cd frontend && npm test && npm run lint && npm run build
```

## API

| Method | Yol | Açıklama |
|---|---|---|
| GET | `/api/health` | `{"status":"ok"}` |
| GET | `/api/categories` | Sütunlar, `position` sırasıyla |
| GET | `/api/todos` | Todo'lar, sütun sütun ve her sütunda yukarıdan aşağı (`position`) |
| POST | `/api/todos` | `{"title", "category_id"?, "metadata"?}` → 201 |
| GET | `/api/todos/{id}` | Tek todo |
| PATCH | `/api/todos/{id}` | `title`, `category_id` (sütun taşıma), `position` (sütundaki sıra, 0 = en üst), `metadata` alanlarından en az biri |
| DELETE | `/api/todos/{id}` | 204 |

Hatalar `{"error": "..."}` biçiminde döner. Ayrıntılar ve doğrulama kuralları:
[`docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md`](docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md) §6.

## Tasarımlar

Sağ üstteki minik menüden 5 tasarım arasında anında geçilir; seçim tarayıcıda hatırlanır.
Hepsi aynı sayfa ve aynı işlevdir — yalnızca görünüm değişir. Kartlar sürüklenerek sütunlar
arasında taşınır ve sütun içinde sıralanır; sıra veritabanında saklanır.

| Menü | Tema dosyası |
|---|---|
| Neubrutalizm | `frontend/src/themes/neubrutalism.css` |
| Glass | `frontend/src/themes/glass.css` |
| Swiss | `frontend/src/themes/swiss.css` |
| Pano | `frontend/src/themes/skeuo.css` |
| Zen | `frontend/src/themes/zen.css` |

**Bir tasarımı kaldırmak:** tema dosyasını sil, `frontend/src/index.css` içindeki `@import`
satırını ve `frontend/src/themes/index.ts` listesindeki girdiyi kaldır. Tek tasarım kalınca menü
kendiliğinden gizlenir. Temalar, bileşenlerdeki anlamlı sınıfları (`.board`, `.column`, `.card`,
`.card-body`, ...) `[data-theme="<id>"]` altında stillendirir; liste tasarım spec'inde
(`docs/superpowers/specs/2026-10-24-todo-app-designs-dnd-design.md` §5.3).

## Proje yapısı

```
backend/
  cmd/server/          sunucu giriş noktası (ayarlar, düzgün kapanış)
  internal/api/        HTTP katmanı: rotalar, doğrulama, JSON
  internal/db/         SQLite bağlantısı, gömülü migration'lar, veri erişimi
    migrations/        NNNN_aciklama.sql — açılışta sırayla, bir kez uygulanır
frontend/
  src/api.ts           tipli API istemcisi
  src/board.ts         sürükle-bırak sonrası yerel sıralama (API ile aynı kural)
  src/components/      pano (sürükle-bırak), ekleme formu, kart, tema menüsü
  src/themes/          tema listesi (index.ts) ve tema başına bir CSS dosyası
docs/superpowers/      tasarım spec'i ve implementation planı
```

Şema değişikliği için `backend/internal/db/migrations/` altına bir sonraki numarayla yeni
bir `.sql` dosyası ekle; sunucu bir sonraki açılışta uygular.
