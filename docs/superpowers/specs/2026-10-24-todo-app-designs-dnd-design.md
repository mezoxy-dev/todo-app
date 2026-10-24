# Todo App — Adım 2: 5 Tasarım + Sürükle-Bırak (Tasarım Spec'i)

- **Tarih:** 2026-10-24
- **Durum:** Tasarım sohbette bölüm bölüm onaylandı; bu yazılı spec kullanıcı incelemesinde
- **Önceki adım:** `docs/superpowers/specs/2026-10-24-todo-app-foundation-design.md` (bu spec'in
  değiştirmediği her şey orada geçerli)
- **Branch:** `step-2-designs`

## 1. Amaç

Aynı işlevselliği 5 farklı tasarım felsefesiyle göstermek: tek sayfa, tek HTML yapısı; üstteki
minik tuşlarla tasarımlar arasında anında geçiş. Kullanıcı sonra birini seçip diğerlerini
silecek. Ek olarak kartlar sürükle-bırak ile hem sütunlar arasında hem sütun içinde taşınabilir
ve sıra kalıcıdır.

## 2. Kararlar

| # | Karar | Gerekçe |
|---|---|---|
| D1 | Tek bileşen ağacı; 5 tasarım yalnızca CSS (her biri tek dosya) | Kullanıcı: "gereksiz kod yazma, görünümler farklı olsa yeter, işlev hepsinde aynı". |
| D2 | Tasarımlar: Neubrutalizm, Glass(morphism), Swiss/International, Pano (skeuomorfizm), Zen (minimalizm) | Kullanıcı seçimi. |
| D3 | Sürükle-bırak: sütunlar arası **ve** sütun içi sıralama; sıra DB'de kalıcı | Kullanıcı seçimi. |
| D4 | Sıra modeli: her sütunda boşluksuz tam sayı `position` (0 = en üst), sunucu tek transaction'da yeniden numaralar | Tek kullanıcı için en basit ve test edilebilir model; kesirli/sözlüksel sıralamaya gerek yok. |
| D5 | Kütüphane: `@hello-pangea/dnd` | Sütun listeleri arası/içi sürükleme için hazır; bırakınca (sütun, sıra) verir → en az kod. Fare, dokunmatik, klavye desteği hazır. Kurulumda React 19 ile uyumsuz çıkarsa (peer dependency hatası veya StrictMode'da çalışmama) `@dnd-kit/core` + `@dnd-kit/sortable` kullanılır ve raporlanır; davranış sözleşmesi (bölüm 5.4) değişmez. |
| D6 | Taşıma iyimser (optimistic): kart bırakıldığı yerde hemen görünür, PATCH başarısızsa geri alınır | İyimser olmasa kart önce geri zıplar, sonra yerine geçer. Adım 1'deki "iyimser güncelleme yok" kuralı **yalnızca taşıma için** değişir; ekleme sunucu yanıtını beklemeye devam eder. |
| D7 | Karttaki sütun seçici kalır; seçiciyle taşınan kart hedef sütunun en üstüne gider | Klavye/erişilebilirlik yedeği; mevcut testler korunur. |
| D8 | Temanın kalıcılığı `localStorage` (`todo-theme`) | Yenilemede tasarım değişmez; kişisel tercih, sunucuya gitmez. |

## 3. Kapsam

**Var:** migration 0002 + sıralama kuralları (store + API), sürükle-bırak, tema menüsü, 5 tema
CSS'i, ilgili testler, Edge ile uçtan uca doğrulama ve 5 temanın ekran görüntüsü.

**Yok (sonraki adımlar):** kategori yönetimi, arayüzde silme/başlık düzenleme, metadata arayüzü,
tema seçiminin sunucuda saklanması.

## 4. Backend

### 4.1 Migration — `backend/internal/db/migrations/0002_todo_position.sql`

```sql
ALTER TABLE todos ADD COLUMN position INTEGER NOT NULL DEFAULT 0;

-- Mevcut todo'lar: her sütundaki şimdiki sıra (en yeni üstte) korunur
UPDATE todos SET position = ranked.rn
FROM (SELECT id, ROW_NUMBER() OVER (PARTITION BY category_id
                                    ORDER BY created_at DESC, id DESC) - 1 AS rn
      FROM todos) AS ranked
WHERE todos.id = ranked.id;

CREATE INDEX idx_todos_category_position ON todos(category_id, position);
```

### 4.2 Sıra kuralı (invariant)

Her sütunda (kategoride) todo'ların `position` değerleri boşluksuz `0..n-1`'dir. Oluşturma,
taşıma ve silme bu kuralı tek bir transaction içinde korur. (DB'de `UNIQUE` kısıtı yoktur:
yeniden numaralama sırasında SQLite satır satır kontrol ettiği için geçici çakışmalar olur.)

### 4.3 Davranış

| İşlem | Davranış |
|---|---|
| CreateTodo | Hedef sütunun en üstüne (`position = 0`) eklenir; o sütundaki diğerleri +1 kayar. |
| UpdateTodo `Position` verilir | Todo hedef sütunda (verilmişse `CategoryID`, yoksa mevcut sütun) `Position` sırasına yerleşir. `Position`, todo kendi yerinden **çıkarıldıktan sonraki** listede hedef sıradır. Hedef sütundaki todo sayısından büyükse sona yerleşir. |
| UpdateTodo yalnızca `CategoryID` (farklı sütun) | Hedef sütunun en üstüne (`0`) gider. Aynı sütun verilirse sıra değişmez. |
| UpdateTodo yalnızca `Title`/`Metadata` | Sıra değişmez. |
| DeleteTodo | Silinen todo'nun sütununda arkasındakiler −1 kayar. |
| ListTodos | `ORDER BY category_id, position, id`. |

Her taşımada `updated_at` güncellenir (adım 1 kuralı).

### 4.4 Go arayüzü değişiklikleri (`todoapp/internal/db`)

```go
type Todo struct {
	// ... adım 1 alanları aynen
	Position int64 `json:"position"` // sütun içindeki sıra, 0 = en üst
}

type UpdateTodoParams struct {
	Title      *string
	CategoryID *int64
	Position   *int64 // >= 0 olduğunu çağıran doğrular
	Metadata   json.RawMessage
}
```

`CreateTodoParams` değişmez (yeni todo hep en üste). Hata davranışı adım 1 ile aynı
(`ErrNotFound` önce, sonra `ErrInvalidCategory`).

### 4.5 API değişiklikleri

- Todo JSON'una `"position"` (tam sayı) eklenir.
- `PATCH /api/todos/{id}` gövdesi `position` alanını da kabul eder (tek başına veya
  `category_id` ile). "En az bir alan" kuralına `position` da sayılır.
- `position` doğrulaması: `null` → 400 `position must not be null`; tam sayı değil veya
  negatif → 400 `position must be a non-negative integer`.
- `POST /api/todos` `position` kabul etmez (bilinmeyen alan → 400, adım 1 kuralı).

## 5. Frontend

### 5.1 Yapı

```
frontend/src/
  api.ts              Todo'ya position, UpdateTodoInput'a position eklenir
  board.ts            saf sıralama yardımcısı (moveTodo)
  App.tsx             durum, yükleme, ekleme, taşıma (iyimser + geri alma)
  components/         AddTodoForm, Board (DnD), TodoCard, ThemeSwitcher
  themes/
    index.ts          tema listesi: [{ id, label }]
    neubrutalism.css  glass.css  swiss.css  skeuo.css  zen.css
  index.css           @import "tailwindcss"; + 5 tema dosyasının @import'u
```

**Bir tasarımı öldürmek:** CSS dosyasını sil, `index.css`'teki `@import` satırını ve
`themes/index.ts`'teki girdiyi kaldır. Listede tek tema kalınca menü gizlenir.

### 5.2 Tema mekanizması

- Aktif tema `<html data-theme="<id>">` ile belirtilir. Uygulama ilk render'dan önce
  `localStorage['todo-theme']`'i okur; değer listede yoksa veya okunamazsa varsayılan
  `neubrutalism` kullanılır. Tema değişimi anında olur, veri yeniden yüklenmez.

| id | Menü etiketi | Görünüm (tasarım özeti) |
|---|---|---|
| `neubrutalism` | Neubrutalizm | Krem zemin, 3px siyah kenarlık, sert ofset gölge (`6px 6px 0 #000`), sütun başlıkları farklı doygun renklerde (sarı/pembe/camgöbeği), Space Grotesk. Butona basınca gölge kaybolur (basılma hissi); sürüklenen kart biraz büyür. |
| `glass` | Glass | Mor-mavi-pembe gradyan + bulanık renk lekeleri arka plan; buzlu cam paneller (`backdrop-filter: blur`, yarı saydam beyaz dolgu, ince yarı saydam beyaz kenar, büyük yumuşak köşeler); beyaz metin, okunur kontrast. Sürüklenen kart daha opak. |
| `swiss` | Swiss | Beyaz zemin, siyah metin, tek kırmızı vurgu; büyük, sıkı, sola hizalı tipografi (Inter, ağır başlık); sütunlar ince siyah çizgilerle ayrılır; kartlar kutu değil ince çizgili liste satırları; köşe yuvarlama yok. |
| `skeuo` | Pano | Mantar pano dokusu (CSS gradyanlarıyla); ahşap çerçeveli sütunlar; kartlar hafif eğik pastel yapışkan notlar, el yazısı (Caveat), üstte bant detayı; sürüklenen not "kalkar" (eğim düzelir, gölge büyür). |
| `zen` | Zen | Kum / kırık beyaz tonlar, bol boşluk; ince serif başlıklar (Noto Serif); kartlar neredeyse çerçevesiz (yalnızca ince alt çizgi); tek soluk yeşil (matcha) vurgu; yumuşak geçişler. |

- Fontlar `index.html`'de tek Google Fonts bağlantısıyla yüklenir (Space Grotesk, Inter, Caveat,
  Noto Serif; Türkçe karakterleri içeren alt kümeler). İnternet yoksa sistem fontlarına düşülür.

### 5.3 İşaretleme sözleşmesi (tema CSS'lerinin hedeflediği sınıflar)

Bileşenler aşağıdaki sınıfları ve durum işaretlerini taşır; ortak yerleşim (ızgara, boşluklar,
esneklik) bileşenlerde Tailwind ile yapılır, görünüm (renk, kenarlık, gölge, font, doku, köşe,
efekt) tema CSS'lerindedir.

| Sınıf / işaret | Element |
|---|---|
| `.app` | sayfa kabı (`<main>`) |
| `.app-header`, `.app-title` | başlık satırı ve "Todo'lar" başlığı |
| `.theme-switcher`, `.theme-button` (`aria-pressed`) | tema menüsü ve tuşları |
| `.add-form`, `.field`, `.add-input`, `.add-select`, `.button`, `.add-button` | ekleme formu |
| `.board` | 3 sütunun kabı |
| `.column` (`data-column="<kategori position>"`), `.column-title` | sütun ve başlığı |
| `.card-list` (sürüklerken üzerindeyse `.is-drag-over`) | bırakma alanı (boş sütunda da var) |
| `.card` (sürüklenirken `.is-dragging`), `.card-body`, `.card-title`, `.card-select` | kart |
| `.empty` | boş sütundaki "Boş" yazısı |
| `.alert` | hata mesajları (`role="alert"`) |
| `.status` | "Yükleniyor..." |

**Tema kuralları:**
1. Her kural `[data-theme="<id>"]` altında yazılır; tema dosyası başka temayı etkilemez.
2. CSS `content` ile **metin** eklenmez (tarayıcı bunu başlık/buton erişilebilir adına katar ve
   "Tamamlandı (1)" gibi adlar bozulur). Süs amaçlı boş içerikli (`content: ""`) öğeler serbesttir.
3. `.card` üzerine `transform` uygulanmaz (sürükleme kütüphanesi konumu onunla verir); eğim gibi
   efektler `.card-body`'ye uygulanır.
4. Davranış/görünürlük değişmez: tüm elemanlar her temada görünür ve kullanılabilirdir.

### 5.4 Davranış

- **Menü:** `role="group"`, `aria-label="Tasarım"`; her tuş `aria-pressed`. Tıklayınca
  `data-theme` ve `localStorage['todo-theme']` güncellenir.
- **Sürükle-bırak:** her sütunun `.card-list`'i bırakma alanı, her `.card` sürüklenebilir. Bırakınca
  hedef yoksa ya da aynı sütunda aynı sıraya bırakıldıysa hiçbir şey olmaz; aksi halde
  `moveTodo(id, hedefKategori, hedefSıra)`.
- **Seçici:** değişince `moveTodo(id, yeniKategori, 0)` (hedef sütunun en üstü).
- **moveTodo (iyimser):** önceki durum saklanır → kart yerel olarak yeni yerine konur →
  `PATCH /api/todos/{id}` gövdesi `{"category_id": X, "position": N}` → başarıda sunucunun
  döndürdüğü todo yerel kopyanın yerine konur (sıra korunur); hatada önceki durum geri yüklenir
  ve panonun üstünde `Todo taşınamadı: <detay>` (`role="alert"`) gösterilir.
- Bir taşıma kaydedilirken yeni sürükleme ve tüm kart seçicileri devre dışıdır (yarış olmaz).
- **Ekleme:** adım 1 ile aynı (sunucu yanıtı beklenir); yeni kart sütununun en üstünde görünür.
- **Sıra kaynağı:** arayüzde sütun içi sıra, yerel dizideki sıradır; ilk yüklemede API sırası
  (`category_id, position`) kullanılır.

### 5.5 `board.ts`

```ts
// Todo'yu hedef sütunda hedef sıraya taşır (sıra, todo çıkarıldıktan sonraki listeye göre;
// liste uzunluğundan büyükse sona). Yeni bir dizi döndürür: her sütundaki todo'lar yeni
// sırasında, her todo'nun position'ı sütunundaki sırasına eşit, taşınanın category_id'si güncel.
export function moveTodo(todos: Todo[], todoId: number, toCategoryId: number, toIndex: number): Todo[]
```

## 6. Testler

**Backend (Go):**
- Store: her işlemden sonra sıra kuralı (`0..n-1`) her sütunda geçerli; oluşturma en üste ekler;
  sütun içinde aşağı/yukarı taşıma; sütunlar arası belirli sıraya taşıma; liste sonundan büyük
  sıra sona yerleşir; yalnızca kategori değişimi en üste koyar; aynı kategori + sırasız
  güncelleme sırayı değiştirmez; silme boşluğu kapatır; `ListTodos` sırası.
- Migration: 0001 uygulanmış ve todo içeren bir DB'de 0002 sonrası her sütunda eski sıra
  (en yeni üstte) korunur; `user_version = 2`.
- API: `position` JSON'da; PATCH ile sıralama ve sütun+sıra taşıma; `position` doğrulama
  mesajları (`null`, negatif, ondalık, metin); POST'ta `position` → 400 (bilinmeyen alan).

**Frontend (Vitest):**
- `board.ts`: sütun içi aşağı/yukarı, sütunlar arası sıraya, sona sabitleme, position'ların
  yeniden numaralanması.
- Menü: 5 tuş birebir etiketlerle; varsayılan `neubrutalism` (`aria-pressed`, `data-theme`);
  tıklayınca tema ve `localStorage` güncellenir; açılışta kayıtlı tema uygulanır; geçersiz kayıt →
  varsayılan.
- Taşıma: seçiciyle taşımada PATCH gövdesi `{"category_id": X, "position": 0}` ve kart hedefin
  en üstünde; bekleme sırasında seçiciler pasif; hata → kart ve seçici eski yerinde + mesaj.
- Adım 1'in diğer davranış testleri (ekleme, hata, yükleme) geçmeye devam eder.

**Uçtan uca (Edge, `playwright-core`, orkestratör):** her tema için menüden geçiş + ekran
görüntüsü; gerçek fare ile sütunlar arası ve sütun içi sürükleme; sayfa yenileme ve backend
yeniden başlatma sonrası sıra korunur; konsol temiz.

## 7. Kabul kriterleri

1. Üstteki menüde 5 tuş var; tıklayınca görünüm anında değişir, sayfa yenilenince seçim kalır.
2. 5 temada da: 3 sütun, istenen sütuna ekleme, sürükleyerek sütunlar arası ve sütun içi taşıma,
   seçiciyle taşıma çalışır; görünümler birbirinden belirgin biçimde farklıdır.
3. Sürüklenen kartın yeri ve sırası yenileme ve backend yeniden başlatma sonrası korunur.
4. `go vet ./...`, `go test ./...`, `npm run lint`, `npm run build`, `npm test` hatasız.
5. Bir temayı silmek yalnızca 1 CSS dosyası + 2 satır değişikliği gerektirir.
