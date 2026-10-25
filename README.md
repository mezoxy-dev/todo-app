# Todo App

Todo'ları toplamak ve arayüzden yönetmek için yerelde çalışan, tek kullanıcılı bir uygulama.
Giriş yok: açınca pano doğrudan gelir. Yeni bir todo'nun hangi sütuna (kategoriye) gideceğine
yapay zekâ (Google Gemini, ücretsiz katman) karar verir; kartı elle taşımak her zaman mümkündür.

- **Backend:** Go (standart `net/http`) + SQLite (`modernc.org/sqlite`, CGO gerekmez)
- **Frontend:** React + TypeScript + Vite + Tailwind CSS v4
- **Yapay zekâ:** Gemini API (REST, yalnızca `net/http` ile; ek bağımlılık yok)

## Gereksinimler

- Go 1.26+
- Node.js 24+ ve npm 11+
- Yapay zekâ için ücretsiz bir Gemini API anahtarı (isteğe bağlı; bkz. [Anahtar kurulumu](#anahtar-kurulumu-ücretsiz))

## Çalıştırma

İki ayrı terminalde:

```bash
# 1) API — http://127.0.0.1:8080
#    İlk açılışta backend/data/todos.db ve 9 sütun (Gelen kutusu, 7 kategori, Diğer) oluşturulur.
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
| `GEMINI_API_KEY` | (yok) | Gemini anahtarı. Yoksa uygulama çalışır, yalnızca kartlar sınıflandırılamaz. |
| `AI_MODEL` | `gemini-3.5-flash-lite` | Kullanılan model. |
| `AI_TIMEOUT` | `20s` | Tek çağrının zaman aşımı. |
| `AI_MIN_INTERVAL` | `4s` | Ardışık iki çağrı arasındaki en kısa süre (ücretsiz katmanın dakika sınırı için). |
| `GEMINI_BASE_URL` | `https://generativelanguage.googleapis.com` | API adresi; testlerde sahte sunucu için. |

## Yapay zekâ ile kategoriler

Pano 9 sütundan oluşur ve yatay kaydırılır: **Gelen kutusu**, 7 kategori (Satın alım, Araştırma,
İletişim, Ödeme, Randevu & Etkinlik, İş, Ev & Kişisel) ve **Diğer**. Yeni todo hemen Gelen
kutusunun en üstünde `sınıflandırılıyor…` rozetiyle görünür. Sunucu kartları sırayla Gemini'ye
sorar; cevap gelince kart kategorisinin en üstüne geçer (genelde birkaç saniye). Kategori adları ve
açıklamaları veritabanındadır (`backend/internal/db/migrations/0003_ai_categories.sql`); modele
giden talimat bunlardan üretilir.

- **Elle taşıma her zaman kazanır:** kartı sürükleyerek ya da karttaki kategori seçiciyle taşırsan
  kart elle yerleştirilmiş sayılır ve yapay zekâ ona bir daha dokunmaz.
- **Sınıflandırılamazsa** (anahtar yok, kota doldu, ağ hatası) kart Gelen kutusunda
  `sınıflandırılamadı` rozetiyle kalır; **Tekrar dene** onu yeniden sıraya alır.
- Kartlarda ayrıca **durum** (Yapılacak / Devam ediyor / Tamamlandı) ve **açıklama** vardır; ikisi
  de ekleme formunda seçilebilir, kartta sonradan değiştirilebilir. Tamamlandı kartlar soluk ve
  üstü çizili görünür.
- Sunucu açılışta sınıflandırma bekleyen kartları sıraya alır; kapanınca yarım kalanlar bir sonraki
  açılışta işlenir.

### Anahtar kurulumu (ücretsiz)

1. [Google AI Studio](https://aistudio.google.com/)'dan, faturalandırma açmadan bir API anahtarı al.
2. Anahtarı kullanıcı ortam değişkeni olarak kaydet (Windows):

   ```powershell
   setx GEMINI_API_KEY "<anahtar>"
   ```

3. **Yeni** bir terminal aç (`setx` açık terminalleri etkilemez) ve backend'i oradan başlat.

Anahtar yalnızca istek başlığında (`x-goog-api-key`) gönderilir; log kayıtlarına, hata mesajlarına,
API yanıtlarına ve arayüze yazılmaz. Anahtarı sonradan tanımlarsan backend'i yeniden başlatıp
sınıflandırılamayan kartlarda **Tekrar dene**'ye bas.

### Model ölçümü

```bash
cd backend
go run ./cmd/aibench   # GEMINI_API_KEY gerekir; go test'e dahil değildir
```

`internal/ai/testdata/benchmark.json` içindeki etiketli örnekleri modele gönderir; her örneğin
sonucunu, toplam isabeti, kategori başına isabeti, karışıklık tablosunu ve medyan süreyi yazar.
Hedef ≥ %85 (`gemini-3.5-flash-lite` ile son ölçüm: 44/46, %95,7). Kategori açıklamalarını
değiştirdikten sonra yeniden ölç.

## Log paneli

Sağ alttaki **Loglar** düğmesi sunucunun son olaylarını (todo eklendi, taşındı, durum değişti,
yapay zekâ sınıflandırdı, hata...) gösteren paneli açar. Panel her açılışta gizlidir ve kapalıyken
sunucuya istek atmaz. Her kayıt tek satırdır; ▸ işaretli kayıtlara tıklayınca ayrıntı (ör. modele
giden metin, modelin cevabı ve gerekçesi, hata ayrıntısı) aşağı doğru açılır. Kayıtlar bellekte
tutulur (son 500) ve sunucu yeniden başlayınca sıfırlanır; aynı kayıtlar backend'in terminal
çıktısında da görünür.

## Testler

```bash
cd backend && go vet ./... && go test ./...
cd frontend && npm test && npm run lint && npm run build
```

Testler gerçek Gemini'ye istek atmaz (sahte HTTP sunucusu kullanır); anahtar gerekmez.

## API

| Method | Yol | Açıklama |
|---|---|---|
| GET | `/api/health` | `{"status":"ok"}` |
| GET | `/api/categories` | Sütunlar (`id`, `key`, `name`), `position` sırasıyla |
| GET | `/api/todos` | Todo'lar, sütun sütun ve her sütunda yukarıdan aşağı (`position`) |
| POST | `/api/todos` | `{"title", "description"?, "status"?, "metadata"?}` → 201; kart Gelen kutusunun en üstüne, sınıflandırma sırasına girer |
| GET | `/api/todos/{id}` | Tek todo |
| PATCH | `/api/todos/{id}` | `title`, `description`, `status`, `category_id` (sütun taşıma; kart elle yerleştirilmiş olur), `position` (sütundaki sıra, 0 = en üst), `metadata` alanlarından en az biri |
| POST | `/api/todos/{id}/classify` | Kartı Gelen kutusunun en üstüne alıp yeniden sınıflandırma sırasına koyar → 202 |
| DELETE | `/api/todos/{id}` | 204 |
| GET | `/api/logs?after=<seq>&limit=<n>` | `{"entries", "last_seq", "truncated"}`: `seq`'i `after`'dan büyük kayıtlar, eskiden yeniye (`limit` 1–500, varsayılan 200) |

Todo'da `status` `todo` / `in_progress` / `done`, `classification` `pending` / `done` / `failed` /
`manual` değerlerinden birini alır. Hatalar `{"error": "..."}` biçiminde döner. Ayrıntılar ve
doğrulama kuralları:
[`docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md`](docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md) §6 ve
[`docs/superpowers/specs/2026-10-25-todo-app-ai-categories-design.md`](docs/superpowers/specs/2026-10-25-todo-app-ai-categories-design.md) §7.

## Tasarım

Arayüz **Pano** (skeuomorfizm) temasını kullanır: mantar pano, ahşap çerçeveli sütunlar,
bantlı yapışkan notlar. Kartlar sürüklenerek sütunlar arasında taşınır ve sütun içinde
sıralanır; sıra veritabanında saklanır. (Adım 2'de denenen diğer 4 tasarım kaldırıldı.)

Tema `frontend/src/themes/skeuo.css` dosyasındadır ve bileşenlerdeki anlamlı sınıfları
(`.board`, `.column`, `.card`, `.card-body`, ...) `[data-theme="skeuo"]` altında stillendirir;
sınıf listesi tasarım spec'lerinde (`docs/superpowers/specs/2026-10-24-todo-app-designs-dnd-design.md`
§5.3 ve `docs/superpowers/specs/2026-10-25-todo-app-ai-categories-design.md` §9.6). **Yeni tema
eklemek:** `frontend/src/themes/<id>.css` dosyası, `frontend/src/index.css`'e bir `@import` satırı
ve `frontend/src/themes/index.ts` listesine bir girdi. İki ya da daha fazla tema olunca sağ üstteki
tema menüsü kendiliğinden görünür.

## Proje yapısı

```
backend/
  cmd/server/          sunucu giriş noktası (ayarlar, sınıflandırma işçisi, düzgün kapanış)
  cmd/aibench/         modelin kategori isabetini ölçen araç
  internal/api/        HTTP katmanı: rotalar, doğrulama, JSON
  internal/ai/         Gemini sınıflandırıcı: talimat, JSON şeması, hata türleri
  internal/applog/     bellek içi log tamponu (log paneli)
  internal/classify/   sınıflandırma işçisi: tek sıra, çağrılar arası bekleme
  internal/db/         SQLite bağlantısı, gömülü migration'lar, veri erişimi
    migrations/        NNNN_aciklama.sql — açılışta sırayla, bir kez uygulanır
frontend/
  src/api.ts           tipli API istemcisi
  src/board.ts         sürükle-bırak ve sınıflandırma sonrası yerel sıralama (API ile aynı kural)
  src/statuses.ts      durum değerleri ve etiketleri
  src/usePendingPolling.ts  bekleyen kartlar için 1 saniyelik yoklama
  src/components/      pano (sürükle-bırak), ekleme formu, kart, log paneli, tema menüsü
  src/themes/          tema listesi (index.ts) ve tema başına bir CSS dosyası
docs/superpowers/      tasarım spec'leri ve implementation planları
```

Şema değişikliği için `backend/internal/db/migrations/` altına bir sonraki numarayla yeni
bir `.sql` dosyası ekle; sunucu bir sonraki açılışta uygular. Kategori açıklamaları da böyle
değiştirilir: uygulanmış bir migration dosyası değiştirilmez, yeni bir migration eklenir.
