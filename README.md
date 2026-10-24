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
| GET | `/api/todos` | Todo'lar, en yeni üstte |
| POST | `/api/todos` | `{"title", "category_id"?, "metadata"?}` → 201 |
| GET | `/api/todos/{id}` | Tek todo |
| PATCH | `/api/todos/{id}` | `title`, `category_id` (sütun taşıma), `metadata` alanlarından en az biri |
| DELETE | `/api/todos/{id}` | 204 |

Hatalar `{"error": "..."}` biçiminde döner. Ayrıntılar ve doğrulama kuralları:
[`docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md`](docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md) §6.

## Proje yapısı

```
backend/
  cmd/server/          sunucu giriş noktası (ayarlar, düzgün kapanış)
  internal/api/        HTTP katmanı: rotalar, doğrulama, JSON
  internal/db/         SQLite bağlantısı, gömülü migration'lar, veri erişimi
    migrations/        NNNN_aciklama.sql — açılışta sırayla, bir kez uygulanır
frontend/
  src/api.ts           tipli API istemcisi
  src/components/      pano, ekleme formu, kart
docs/superpowers/      tasarım spec'i ve implementation planı
```

Şema değişikliği için `backend/internal/db/migrations/` altına bir sonraki numarayla yeni
bir `.sql` dosyası ekle; sunucu bir sonraki açılışta uygular.
