# Todo App — Adım 3: Yapay Zekâ ile Kategoriler, Kart Durumu, Açıklama, Log Paneli (Tasarım Spec'i)

- **Tarih:** 2026-10-25
- **Durum:** Tasarım sohbette bölüm bölüm onaylandı; bu yazılı spec kullanıcı incelemesinde
- **Önceki adımlar:** `2026-10-24-todo-app-foundation-design.md`, `2026-10-24-todo-app-designs-dnd-design.md`
  (bu spec'in değiştirmediği her şey orada geçerli: sıra kuralı, sürükle-bırak, iyimser taşıma, Pano teması)
- **Branch:** `step-3-ai-categories`

## 1. Amaç

Panonun sütunları artık süreç durumu değil **kategoridir**. Kategoriyi, yazılan todo'nun niyetinden
**ücretsiz bir yapay zekâ modeli** belirler. Her kartın durumu (Yapılacak / Devam ediyor / Tamamlandı)
kartın üzerinden seçilir, kartların bir açıklaması olur. Varsayılan olarak gizli, açılıp kapanabilen
bir **log paneli** uygulamada olup biteni (yapay zekâ çağrılarının ayrıntısı dahil) canlı gösterir.

## 2. Kararlar

| # | Karar | Gerekçe |
|---|---|---|
| E1 | Sütunlar = kategoriler: **Gelen kutusu** (sistem) + 7 kategori + **Diğer**. | Kullanıcı seçimi. |
| E2 | Her kartta aynı 3 durum: `todo` / `in_progress` / `done` (Yapılacak / Devam ediyor / Tamamlandı), kart üzerindeki seçiciyle. | Kullanıcı seçimi. |
| E3 | Sınıflandırıcı: **Gemini API ücretsiz katmanı**, varsayılan model `gemini-3.5-flash-lite`, faturalandırması açılmamış bir projenin anahtarıyla. | Kullanıcı: "kesinlikle para çıkmasın"; büyük model indirmek şu an mümkün değil. GitHub Models 2026-07-30'da kapatıldı (doğrulandı). Faturasız projede ücretli katmana geçilemez; kota aşımı reddedilir, ücretlendirilmez. |
| E4 | Yapılandırılmış çıktı: JSON şeması; `category` yalnızca listedeki anahtarlardan biri (enum), `reason` tek cümle; `temperature: 0`. Cevap ayrıca kodda doğrulanır. | Model listede olmayan kategori uyduramaz; aynı girdi aynı sonucu verir. |
| E5 | Sınıflandırma **asenkron**: tek arka plan işçisi, sıralı, çağrılar arası en az `AI_MIN_INTERVAL` (4 sn). Sonuç, kart **hâlâ Gelen kutusunda ve `pending` ise** uygulanır. | Ekleme hiç beklemez; dakikalık kotaya takılmaz; kullanıcının elle kararı ezilmez. |
| E6 | Haberleşme: **REST + kısa aralıklı polling (1 sn)**, yalnızca bekleyen kart varken ve log paneli açıkken. | Kullanıcı seçimi ("small polling"). |
| E7 | Loglar sunucu belleğinde halka tampon (son 500), sıra numaralı; tek satır mesaj + isteğe bağlı ayrıntı. Polling istekleri loglanmaz. | Canlı takip için; sunucu yeniden başlayınca sıfırlanır (kullanıcı onayladı). |
| E8 | Mevcut veri: durum eski sütundan türetilir; kartlar Gelen kutusuna `pending` taşınır ve açılışta sınıflandırılır. Canlı DB'ye uygulanmadan önce yedek alınır (orkestratör). | Veri kaybı yok. |
| E9 | Sınıflandırıcı küçük bir Go arayüzünün arkasında. | İleride Ollama (yerel model) tek dosya + bir ayarla eklenebilir; şimdi yazılmaz. |
| E10 | Ekleme formunda kategori seçici yok; **durum seçici var**. | Kategoriyi yapay zekâ seçer; "bugün süt aldım" gibi bir kart baştan Tamamlandı eklenebilmeli (adım 1 isteği). |
| E11 | Gizlilik: ücretsiz katmanda içerik Google tarafından ürün geliştirmede kullanılabilir. | Kullanıcı bilgilendirildi ve kabul etti. |

## 3. Kapsam

**Var:** migration 0003; durum, açıklama, sınıflandırma durumu; Gemini sınıflandırıcı + işçi + yeniden
deneme; log tamponu ve API'si; yeni/ değişen uç noktalar; 9 sütunlu yatay pano; kartta açıklama, durum,
kategori seçici, rozetler; ekleme formunda açıklama ve durum; log paneli; Pano temasının yeni öğeler için
genişletilmesi; sahte sunucularla otomatik testler; `cmd/aibench` ile model ölçümü; Edge ile uçtan uca
doğrulama.

**Yok (sonraki adımlar):** kategori ekleme/düzenleme arayüzü; arayüzde todo silme; Ollama sağlayıcısı;
kalıcı loglar; yapay zekânın yeni kategori önermesi; kimlik doğrulama.

## 4. Veritabanı — `backend/internal/db/migrations/0003_ai_categories.sql`

```sql
-- 1) Kategorilere sabit anahtar ve açıklama (yapay zekâ talimatı açıklamalardan üretilir)
ALTER TABLE categories ADD COLUMN key TEXT;
ALTER TABLE categories ADD COLUMN description TEXT NOT NULL DEFAULT '';

INSERT INTO categories (key, name, description, position) VALUES
  ('inbox',         'Gelen kutusu',       'Sistem sütunu: sınıflandırılmayı bekleyen ya da sınıflandırılamayan kartlar.', 0),
  ('purchase',      'Satın alım',         'Satın alınacak ya da sipariş edilecek şeyler. Örnek: süt al, kulaklık sipariş et.', 1),
  ('research',      'Araştırma',          'Bilgi toplama, öğrenme, karşılaştırma ya da ne yapılacağı için gerekli bilgisi verilmemiş belirsiz işler. Örnek: tatil için otel bak, X nedir öğren, bir hediye bul.', 2),
  ('communication', 'İletişim',           'Birini arama, mesaj ya da e-posta gönderme. Örnek: annemi ara, Ali''ye mail at.', 3),
  ('payment',       'Ödeme',              'Fatura, kira, vergi ve diğer para işleri. Örnek: elektrik faturasını öde.', 4),
  ('appointment',   'Randevu & Etkinlik', 'Tarihi ya da saati olan işler. Örnek: dişçi randevusu al, cuma toplantısı.', 5),
  ('work',          'İş',                 'Mesleki görevler. Örnek: sunumu bitir, kod incelemesi.', 6),
  ('home',          'Ev & Kişisel',       'Ev işleri, sağlık, spor, kişisel bakım. Örnek: çamaşır yıka, spora git.', 7),
  ('other',         'Diğer',              'Hiçbir kategoriye uymayanlar.', 8);

-- 2) Todo'lara durum, açıklama, sınıflandırma durumu
ALTER TABLE todos ADD COLUMN status TEXT NOT NULL DEFAULT 'todo'
  CHECK (status IN ('todo', 'in_progress', 'done'));
ALTER TABLE todos ADD COLUMN description TEXT NOT NULL DEFAULT '';
ALTER TABLE todos ADD COLUMN classification TEXT NOT NULL DEFAULT 'pending'
  CHECK (classification IN ('pending', 'done', 'failed', 'manual'));

-- 3) Eski durum sütunları (anahtarı olmayan kategoriler; position 1/2/3) → durum
UPDATE todos SET status = CASE
  (SELECT c.position FROM categories c WHERE c.id = todos.category_id AND c.key IS NULL)
  WHEN 2 THEN 'in_progress' WHEN 3 THEN 'done' ELSE 'todo' END;

-- 4) Tüm kartlar Gelen kutusuna; sıra: eski sütun sırası, sonra eski sıra
UPDATE todos SET
  category_id = (SELECT id FROM categories WHERE key = 'inbox'),
  position    = ranked.rn
FROM (SELECT t.id, ROW_NUMBER() OVER (ORDER BY c.position, t.position, t.id) - 1 AS rn
      FROM todos t JOIN categories c ON c.id = t.category_id) AS ranked
WHERE todos.id = ranked.id;

-- 5) Eski 3 kategori silinir; anahtar benzersiz
DELETE FROM categories WHERE key IS NULL;
CREATE UNIQUE INDEX idx_categories_key ON categories(key);
CREATE INDEX idx_todos_classification ON todos(classification);
```

- `key` için `NOT NULL` ALTER ile eklenemez (SQLite); kategori satırları yalnızca migration'larla
  oluşur ve hepsinin anahtarı vardır. Tablo yeniden kurulmaz (FK'ler açıkken migration transaction'ı
  içinde parent tablo düşürülemez).
- Sıra kuralı (her sütunda boşluksuz `0..n-1`) Gelen kutusu dahil geçerlidir.
- **`metadata.ai`**: işçinin yazdığı ayrıntılar (mevcut `metadata` JSON nesnesine `json_set` ile):
  başarıda `{"model","category","reason","duration_ms","at"}`, hatada `{"model","error","at"}`.
  Kullanıcı PATCH ile `metadata` gönderirse nesne adım 1 kuralıyla tamamen değişir (arayüz göndermez).

## 5. Sınıflandırma durumu (classification)

| Değer | Anlamı | Nereden gelir |
|---|---|---|
| `pending` | Yapay zekâ sırası bekliyor | Yeni kart; "tekrar dene"; migration |
| `done` | Yapay zekâ yerleştirdi | İşçi, kart hâlâ Gelen kutusunda ve `pending` iken |
| `failed` | Sınıflandırılamadı (anahtar yok, kota, hata, geçersiz cevap) | İşçi |
| `manual` | Kullanıcı kategoriyi değiştirdi | PATCH ile farklı bir `category_id` (sürükle-bırak ya da seçici) |

- Yapay zekâ `manual` bir karta asla dokunmaz. Sonuç geldiğinde kart artık Gelen kutusunda değilse ya da
  `pending` değilse **hiçbir şey değişmez**; öneri yalnızca loga yazılır.
- Başarılı sonuçta kart hedef kategorinin **en üstüne** (position 0) taşınır, `classification = 'done'`.
- Hatada kart Gelen kutusunda kalır, `classification = 'failed'`, ayrıntı `metadata.ai.error`'da.

## 6. Yapay zekâ (`backend/internal/ai`)

### 6.1 Arayüz

```go
type Classifier interface {
	Classify(ctx context.Context, in Input) (Result, error)
}
type Input struct {
	Title, Description string
	Categories []Category // Gelen kutusu hariç, position sırasıyla: Key, Name, Description
}
type Result struct {
	Key, Reason, Model string
	Duration time.Duration
	Raw string // modelin ham JSON cevabı (log ayrıntısı için)
}
```

Hatalar ayırt edilebilir olmalı: anahtar yok, **kota (HTTP 429; varsa `Retry-After`)**, zaman aşımı,
geçersiz cevap (JSON değil / listede olmayan anahtar), diğer HTTP hataları.

### 6.2 Gemini uygulaması

- Yalnızca standart kütüphane (`net/http`); yeni Go bağımlılığı yok.
- `POST {GEMINI_BASE_URL}/v1beta/models/{AI_MODEL}:generateContent`; anahtar **başlıkta**
  (`x-goog-api-key`), asla URL'de ya da logda değil.
- Gövde: sistem talimatı (6.3), kullanıcı içeriği `Başlık: …\nAçıklama: …` (açıklama boşsa satır yok),
  `generationConfig`: `temperature: 0`, JSON MIME tipi ve `category` (enum) + `reason` (string) zorunlu
  alanlı şema.
- **Alan adları ve şema biçimi güncel dokümandan doğrulanır**:
  https://ai.google.dev/gemini-api/docs/structured-output ve generateContent REST referansı.
  Model düşünme ayarı destekliyorsa en düşük düzeye çekilir (dokümandan doğrulanır).
- Cevap metni JSON olarak çözülür; `category` listedeki anahtarlardan biri değilse geçersiz cevap hatası.

### 6.3 Sistem talimatı (Türkçe; kategoriler DB'den üretilir)

```
Sen bir yapılacaklar listesi asistanısın. Kullanıcının yazdığı görevi aşağıdaki kategorilerden
TAM OLARAK BİRİNE yerleştir.
Kurallar:
1. Görev bir şeyi öğrenmeyi, araştırmayı ya da karşılaştırmayı gerektiriyorsa veya ne yapılacağına
   karar vermek için gerekli bilgi verilmemişse "research" seç.
2. Hiçbir kategori uymuyorsa "other" seç.
3. "reason" alanına seçimini açıklayan tek kısa Türkçe cümle yaz.
Kategoriler:
- <key> (<name>): <description>
...
```

Kategori açıklamaları ve örnekleri `cmd/aibench` sonuçlarına göre iyileştirilebilir (canlı DB'ye
uygulanmadan önce 0003 içinde; sonrasında yeni bir migration ile).

### 6.4 İşçi (kuyruk)

- Tek goroutine; tekrarları ayıklanan bir kuyruk. Açılışta tüm `pending` kartlar (en eski önce)
  kuyruğa alınır; yeni kart ve "tekrar dene" kuyruğa ekler. Sunucu kapanırken düzgün durur.
- Her kart için: kart yoksa ya da `pending` değilse atla. Anahtar yoksa → `failed` ("API anahtarı yok")
  + log. Aksi halde son çağrıdan bu yana `AI_MIN_INTERVAL` geçmesini bekle → `AI_TIMEOUT` ile çağır →
  başarıda 5'teki kuralla uygula, hatada `failed`. Her adım loglanır (8).
- Kota hatasında kendi kendine sık tekrar deneme yapılmaz; kart `failed` olur, `Retry-After` log
  ayrıntısına yazılır.

### 6.5 Ayarlar (ortam değişkenleri)

| Değişken | Varsayılan | Açıklama |
|---|---|---|
| `GEMINI_API_KEY` | (yok) | Yoksa uygulama çalışır; yeni kartlar `failed` ("API anahtarı yok") olur. |
| `AI_MODEL` | `gemini-3.5-flash-lite` | |
| `AI_TIMEOUT` | `20s` | Tek çağrı zaman aşımı. |
| `AI_MIN_INTERVAL` | `4s` | Ardışık çağrılar arası en kısa süre. |
| `GEMINI_BASE_URL` | `https://generativelanguage.googleapis.com` | Testlerde sahte sunucu için. |

### 6.6 Model ölçümü — `go run ./cmd/aibench`

- `backend/internal/ai/testdata/benchmark.json`: ~40 etiketli Türkçe örnek (`title`, isteğe bağlı
  `description`, `expected`); her kategoriden en az 4, belirsiz/eksik bilgi → `research` örnekleri dahil.
- Kategorileri geçici bir DB'den (migration'lar uygulanmış) okur; `AI_MIN_INTERVAL` ile gönderir;
  her örneğin sonucunu, toplam isabeti, kategori karışıklık tablosunu ve medyan süreyi yazar.
- Hedef: ≥ %85 isabet. Altındaysa kategori açıklamaları/örnekleri iyileştirilip yeniden ölçülür.
- `go test`'e dahil değildir (ağ ve anahtar ister).

## 7. HTTP API değişiklikleri

| Uç nokta | Sözleşme |
|---|---|
| `GET /api/categories` | `[{"id","key","name","position"}]`, `position` sırasıyla |
| `GET /api/todos` | Todo: `id, title, description, category_id, position, status, classification, metadata, created_at, updated_at` |
| `POST /api/todos` | Gövde: `title` (zorunlu), `description`?, `status`?, `metadata`?. Kart Gelen kutusunun en üstüne, `classification = 'pending'`; **201 hemen** döner; kuyruğa eklenir. `category_id`/`position` → 400 `unknown field "…"`. |
| `PATCH /api/todos/{id}` | Ek alanlar: `description`, `status`. Farklı bir `category_id` → `classification = 'manual'`. |
| `POST /api/todos/{id}/classify` *(yeni)* | Kartı Gelen kutusunun en üstüne alır, `classification = 'pending'`, `metadata.ai` silinir, kuyruğa ekler → **202** + todo; yoksa 404. |
| `GET /api/logs?after=<n>&limit=<m>` *(yeni)* | `{"entries":[…],"last_seq":<n>,"truncated":<bool>}`. `after` ≥ 0 (varsayılan 0), `limit` 1–500 (varsayılan 200). `seq > after` olanlar eskiden yeniye; `limit`'ten fazlaysa **en yeni** `limit` tanesi ve `truncated: true`. |

**Doğrulama ve yeni hata mesajları (birebir):**
- `description`: string, `strings.TrimSpace` ile kırpılıp saklanır, en fazla 2000 karakter (rune).
  `description must be a string`, `description must not be null`, `description must be at most 2000 characters`.
- `status`: `status must be one of todo, in_progress, done`; `status must not be null`.
- Loglar: `invalid after`, `invalid limit`.
- Adım 1–2 mesajları ve kuralları aynen geçerli.

## 8. Loglar (`backend/internal/applog`)

```go
type Entry struct {
	Seq     int64     `json:"seq"`
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`             // info | warn | error
	Source  string    `json:"source"`            // api | ai | system
	Message string    `json:"message"`           // tek satır
	Details string    `json:"details,omitempty"` // çok satırlı, isteğe bağlı
}
```

- Eşzamanlı kullanıma güvenli halka tampon, kapasite 500; her kayıt ayrıca `slog` ile stdout'a yazılır.
- API anahtarı hiçbir log kaydında yer almaz.

**Mesajlar (birebir biçim; `“…”` Türkçe tırnak):**

| Kaynak | Seviye | Mesaj | Ayrıntı |
|---|---|---|---|
| system | info | `Sunucu başladı (model: <model>)` | — |
| system | warn | `Yapay zekâ kapalı: GEMINI_API_KEY tanımlı değil` | — |
| system | info | `<n> kart sınıflandırma kuyruğuna alındı` | — |
| api | info | `Todo eklendi: “<başlık>”` | — |
| api | info | `Durum değişti: “<başlık>” → <Yapılacak/Devam ediyor/Tamamlandı>` | — |
| api | info | `Taşındı: “<başlık>” → <kategori adı>` | — (sütun içi sıra değişimi: `Sıra değişti: “<başlık>”`) |
| api | info | `Açıklama güncellendi: “<başlık>”` | — |
| api | info | `Başlık değişti: “<eski>” → “<yeni>”` | — |
| api | info | `Yeniden sınıflandırmaya gönderildi: “<başlık>”` | — |
| ai | info | `Sınıflandırılıyor: “<başlık>”` | — |
| ai | info | `Sınıflandırıldı: “<başlık>” → <kategori adı> (<süre> sn)` | Model, giden metin, modelin JSON cevabı, gerekçe, süre |
| ai | warn | `Öneri uygulanmadı: “<başlık>” (kart elle taşınmıştı)` | Önerilen kategori, gerekçe |
| ai | error | `Sınıflandırılamadı: “<başlık>”` | Hata türü ve mesajı; varsa HTTP durumu ve `Retry-After` |

Süre bir ondalıkla, Türkçe virgülle (`0,4`). Bir PATCH birden fazla alanı değiştirirse her değişiklik
için ayrı satır yazılır.

## 9. Frontend

### 9.1 Pano ve kart
- 9 sütun `position` sırasıyla; pano **yatay kaydırılır** (sabit sütun genişliği); sürüklerken kenarda
  kendiliğinden kayar. Gelen kutusu ilk sütun.
- Kart: başlık; açıklama (varsa, en fazla 2 satır görünür); **Durum** seçici (Yapılacak / Devam ediyor /
  Tamamlandı); **kategori** seçici (klavye/erişilebilirlik için taşıma; seçimde kart hedefin en üstüne);
  açıklama düzenleme (kalem → metin alanı → Kaydet / Vazgeç); rozetler: `pending` → `sınıflandırılıyor…`,
  `failed` → `sınıflandırılamadı` + **Tekrar dene**. Tamamlandı kartlar soluk ve üstü çizili (tema).
- Durum ve açıklama değişikliği sunucu yanıtından sonra uygulanır; kategori/sıra taşıması adım 2'deki
  iyimser akışla.

### 9.2 Ekleme formu
Başlık (`Todo başlığı`, placeholder `Yeni todo yaz...`), **Açıklama ekle** düğmesiyle açılan metin alanı
(`Açıklama`, placeholder `Açıklama (isteğe bağlı)`), **Durum** seçici (varsayılan Yapılacak), `Ekle`.
Kategori seçici yok. Başarılı eklemede kart Gelen kutusunun en üstünde `sınıflandırılıyor…` rozetiyle
görünür.

### 9.3 Polling
- **Bekleyen kartlar:** en az bir kart `pending` iken her 1 sn `GET /api/todos`; sunucuda artık
  `pending` olmayan (yerelde `pending` olan) her kart sunucudaki haliyle değiştirilir ve kendi
  kategorisinin en üstüne yerleştirilir (`failed` ise Gelen kutusunda yerinde kalır). Diğer kartlara
  dokunulmaz. Bekleyen kart kalmayınca durur.
- **Loglar:** panel açılınca `GET /api/logs?limit=200`, sonra açık kaldıkça her 1 sn
  `GET /api/logs?after=<son seq>`; panel kapalıyken istek yok.
- Bir istek bitmeden aynı türden yenisi başlamaz.

### 9.4 Log paneli
- Sağ altta **Loglar** düğmesi (`aria-expanded`); panel her açılışta **gizli**; başlık `Loglar`,
  **Gizle** düğmesi. Boşken `Henüz log yok.`
- Kayıtlar kronolojik (en yeni altta); kullanıcı en alttayken yeni kayıt gelince kendiliğinden aşağı kayar.
- Her kayıt **tek satır**: `HH:MM:SS`, kaynak (`ai` / `api` / `sistem`), mesaj (taşarsa üç nokta);
  seviyeye göre renk.
- Ayrıntısı olan kayıt bir düğmedir (`aria-expanded`, ▸/▾); tıklanınca ayrıntı **aşağı doğru** açılır
  (biçimli, kaydırılabilir), tekrar tıklanınca kapanır. Ayrıntısız kayıt tıklanamaz.
- Panel `role="log"`.

### 9.5 Arayüz metinleri (birebir)
- Kart: `<başlık> için durum`, `<başlık> için kategori` (seçici etiketleri), `Açıklamayı düzenle`,
  `<başlık> açıklaması` (metin alanı etiketi), `Kaydet`, `Vazgeç`, `sınıflandırılıyor…`,
  `sınıflandırılamadı`, `Tekrar dene` (görünen metin; erişilebilir adı `<başlık> için tekrar dene`,
  yükleme hatasındaki `Tekrar dene` düğmesinden ayırt edilsin diye).
- Hatalar: `Todo eklenemedi: <detay>`, `Todo taşınamadı: <detay>`, `Durum değiştirilemedi: <detay>`,
  `Açıklama kaydedilemedi: <detay>`, `Yeniden sınıflandırma başlatılamadı: <detay>`.
- Adım 1–2 metinleri (`Todo'lar`, `Yükleniyor...`, `Sunucuya ulaşılamadı.`, `Tekrar dene`, `Boş`,
  sütun başlığı `<ad> (<sayı>)`) aynen geçerli.

### 9.6 İşaretleme sözleşmesi (yeni sınıflar; adım 2 §5.3'e eklenir)
`.board` (yatay kaydırma kabı), `.column[data-key]`, `.card[data-status][data-classification]`,
`.card-description`, `.card-select.card-status`, `.card-select.card-category`, `.description-editor`,
`.badge.badge-pending`, `.badge.badge-failed`, `.retry-button`, `.add-description-toggle`,
`.add-description`, `.add-status`, `.log-toggle`, `.log-panel`, `.log-list`,
`.log-entry[data-level][data-source]` (+ `.is-expanded`), `.log-time`, `.log-source`, `.log-message`,
`.log-details`. Pano teması (`skeuo.css`) bunları adım 2'deki tema kurallarıyla stillendirir.

## 10. Testler

**Backend (Go, `go test`):**
- Migration 0003: eski sütunlardan durum eşlemesi; tüm kartlar Gelen kutusunda `pending`, sıra kuralı;
  9 kategori anahtarlarıyla; eski kategoriler silinmiş; `user_version = 3`.
- Store: yeni alanlar; `manual` geçişi; sınıflandırma sonucunun yalnızca Gelen kutusu + `pending`
  koşulunda uygulanması (aksi halde değişiklik yok); `failed`; yeniden kuyruğa alma.
- Gemini istemcisi (sahte HTTP sunucusu): yol + model, `x-goog-api-key` başlığı, şema (enum anahtarlar),
  `temperature 0`; başarı; 429 (+ `Retry-After`), 500, zaman aşımı, JSON olmayan cevap, listede olmayan
  anahtar.
- İşçi: açılışta bekleyenleri sıraya alma, sıralı işleme, `AI_MIN_INTERVAL`, anahtar yokken `failed`,
  elle taşınmış karta dokunmama, kapanış.
- Log tamponu: artan `seq`, `after`/`limit`/`truncated`, 500 kapasite; eşzamanlı ekleme.
- API: yeni POST gövdesi, PATCH `status`/`description`/`manual`, `classify`, `logs` ve doğrulama mesajları.

**Frontend (Vitest):** ekleme formu (açıklama + durum); kartta durum ve açıklama düzenleme; rozetler ve
Tekrar dene; polling (sahte saat: `pending` → sınıflanınca kategorisinin en üstüne, bekleyen yokken durma,
üst üste binmeme); log paneli (varsayılan gizli, göster/gizle, tek satır, yalnızca ayrıntılı kayıtta
açılma/kapanma, panel kapalıyken istek yok, `after` ile devam).

**Model ölçümü:** `go run ./cmd/aibench` gerçek Gemini ile (anahtar gerekir).

**Uçtan uca (Edge, gerçek Gemini, orkestratör):** "süt al" → Satın alım, "tatil için otel bak" →
Araştırma; log panelinde yapay zekâ kaydı ayrıntısıyla açılır; elle taşıma `manual` olur ve yapay zekâ
dokunmaz; durum ve açıklama değişikliği yenilemeden sonra korunur; konsol temiz.

## 11. Kabul kriterleri

1. Canlı DB'nin yedeği alınıp 0003 uygulanınca "bakkala gitmek" durumu Tamamlandı olarak Gelen kutusuna
   geçer ve (anahtar varken) açılışta sınıflandırılır.
2. Yeni todo anında Gelen kutusunda `sınıflandırılıyor…` ile görünür; birkaç saniye içinde kategorisine
   geçer; log panelinde ilgili kayıtlar görünür, yapay zekâ kaydının ayrıntısı açılır.
3. Baştan Tamamlandı eklenebilir; durum ve açıklama düzenlenip yenilemeden sonra korunur.
4. Elle taşınan kart `manual` olur; yapay zekâ ona dokunmaz.
5. Anahtar yokken uygulama çalışır, kartlar `sınıflandırılamadı` olur; anahtar tanımlanıp Tekrar dene ile
   sınıflandırılır.
6. Log paneli varsayılan gizli; göster/gizle; tek satır; ayrıntılı kayıt tıklanınca aşağı açılır.
7. `go vet`, `go test`, `npm run lint`, `npm run build`, `npm test` hatasız; aibench isabeti ≥ %85.

## 12. Ortam notları

- Windows 11; Go 1.26.1; Node 24; Edge (uçtan uca).
- Kullanıcı ücretsiz anahtarı Google AI Studio'dan, faturalandırma açmadan alır ve kendi ortamına
  `setx GEMINI_API_KEY "..."` ile kaydeder. Anahtar sohbete yazılmaz; orkestratör onu kullanıcı ortam
  değişkeninden ekrana basmadan sunucu sürecine aktarır.
- Anahtar gelene kadar geliştirme ve otomatik testler sahte sunucuyla ilerler; yalnızca aibench ve gerçek
  uçtan uca doğrulama anahtar bekler.
