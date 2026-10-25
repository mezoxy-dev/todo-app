# Todo App — Adım 4: Docker ile Paketleme ve GitHub'a Gönderme (Tasarım Spec'i)

- **Tarih:** 2026-10-25
- **Durum:** Tasarım sohbette bölüm bölüm onaylandı; bu yazılı spec kullanıcı incelemesinde
- **Önceki adımlar:** `2026-10-24-todo-app-foundation-design.md`, `2026-10-24-todo-app-designs-dnd-design.md`,
  `2026-10-25-todo-app-ai-categories-design.md` (bu spec'in değiştirmediği her şey orada geçerli)
- **Branch:** `step-4-docker`

## 1. Amaç

Depoyu indiren herkes, bilgisayarında yalnızca Docker varken uygulamayı tek komutla
(`docker compose up -d`) çalıştırabilsin; arayüz ve API tek adreste açılsın (http://localhost:8080).
Kod, bütün git geçmişiyle kullanıcının GitHub hesabındaki **gizli** bir depoya güvenli şekilde gönderilir.

## 2. Kararlar

| # | Karar | Gerekçe |
|---|---|---|
| D1 | Uygulamaya yalnızca bu bilgisayardan erişilir: port `127.0.0.1`'e bağlanır. | Kullanıcı seçimi; uygulamada giriş yok, ağa açmak todo'ları herkese açar. |
| D2 | Tek imaj, tek konteyner; Go sunucusu derlenmiş arayüzü de sunar (`STATIC_DIR`). | Kullanıcı seçimi: kuran kişi için en az parça, tek port; nginx yok. |
| D3 | Çok aşamalı Dockerfile; son imaj `gcr.io/distroless/static-debian13:nonroot` (shell yok, kullanıcı 65532). Sürümler sabit: `node:24.18.0-alpine`, `golang:1.26.1-alpine`. | Küçük saldırı yüzeyi; tekrarlanabilir derleme; geliştirme ortamıyla aynı Node (24.18.0) ve Go (1.26.1). Etiketlerin varlığı doğrulandı. |
| D4 | Gemini anahtarı yalnızca çalışma anında verilir: kabuk ortamından ya da git'e girmeyen `.env` dosyasından. İmaja, compose dosyasına ve depoya yazılmaz. Anahtar yoksa uygulama çalışır (adım 3 davranışı). | Güvenlik; kuran kişi kendi ücretsiz anahtarını kullanır. |
| D5 | Veri adlandırılmış Docker volume'unda: `todo-data` → `/data`. `backend/data` bağlanmaz. | Windows'ta klasör bağlama (bind mount) üzerinden SQLite WAL güvenilir çalışmaz; volume her makinede aynı davranır. |
| D6 | Mevcut todo'lar Docker'a taşınmaz; Docker boş panoyla başlar. Geliştirme kurulumu kendi DB'siyle sürer. | Kullanıcı seçimi. |
| D7 | Konteyner sıkılaştırma: salt okunur kök dosya sistemi, `/tmp` tmpfs, bütün Linux yetkileri düşürülmüş, `no-new-privileges`. | Güvenlik; sunucu yalnızca `/data`'ya yazar. |
| D8 | GitHub: gizli depo; boş depoyu kullanıcı açar; push HTTPS + Git Credential Manager ile (giriş tarayıcıda); URL'de kimlik bilgisi yok; yalnızca `main` gönderilir. Commit e-postası `mezoxy@outlook.com` olarak kalır (27 commit'in hepsinde zaten bu; geçmiş yeniden yazılmaz). | Kullanıcı seçimleri; token sohbete ya da dosyaya yazılmaz. |
| D9 | Push'tan önce bütün geçmiş sır ve kişisel veri için taranır (§9). Bulgu varsa push yapılmaz. | Kullanıcı: "güvenli şekilde". |

## 3. Kapsam

**Var:** `backend/internal/web` paketi ve `STATIC_DIR` ayarı; `Dockerfile`; `.dockerignore`;
`compose.yaml`; `.env.example`; `.gitignore`'a `.env`; README'ye Docker bölümü; Go testleri; temiz
klondan Docker doğrulaması (Edge ile uçtan uca dahil); push öncesi tarama; GitHub'a push.

**Yok:** hazır imajın bir registry'de (GHCR, Docker Hub) yayımlanması; CI; HTTPS ve giriş; ağdan ya da
internetten erişim; mevcut verinin Docker'a taşınması; Docker healthcheck (distroless imajda shell ve
curl yok).

## 4. Arayüzün sunulması — `backend/internal/web`

```go
// Handler serves api for /api and every path under /api/, and the built
// frontend in dir for every other path.
func Handler(api http.Handler, dir string) (http.Handler, error)
```

- `dir` içinde `index.html` yoksa `Handler` hata döner: `static dir "<dir>": index.html not found`.
- Yolu `/api` olan ya da `/api/` ile başlayan istekler olduğu gibi `api`'ye gider (bilinmeyen API yolu
  bugünkü gibi API'nin 404'ünü alır).
- Diğer yollar yalnızca `GET` ve `HEAD` kabul eder; başka yöntem → `405 Method Not Allowed`,
  `Allow: GET, HEAD`.
- `/` → `index.html`. Diğer yollar → `dir` içindeki aynı adlı dosya. Dosyalar `os.DirFS(dir)` üzerinden
  okunur; `..` ile `dir` dışına çıkılamaz.
- Klasör yolu (ör. `/assets/`) → 404; klasör içeriği listelenmez. Olmayan dosya → 404.
- Başlıklar:
  - her statik yanıtta `X-Content-Type-Options: nosniff`;
  - `index.html` → `Cache-Control: no-cache` (imaj güncellenince eski arayüz kalmaz);
  - `/assets/` altındaki dosyalar → `Cache-Control: public, max-age=31536000, immutable` (Vite bu
    dosyaların adına içerik hash'i koyar);
  - diğer dosyalar → `Cache-Control` yok.
- `Content-Type` dosya uzantısından belirlenir (`http.ServeContent`).

**`cmd/server`:** yeni ortam değişkeni `STATIC_DIR` (varsayılan boş).
- Boşsa sunucu bugünkü gibi yalnızca API sunar (geliştirme düzeni değişmez).
- Doluysa handler `web.Handler(api, STATIC_DIR)` olur; hata dönerse sunucu açılmaz, `run` hatayla
  döner (çıkış kodu 1).
- `listening` başlangıç kaydına `static` alanı eklenir (boşsa boş).

## 5. Docker imajı — `Dockerfile` (depo kökünde)

| Aşama | Taban | İş |
|---|---|---|
| `web` | `node:24.18.0-alpine` | `frontend/package.json` + `package-lock.json` → `npm ci`; ardından `frontend/` → `npm run build` (→ `dist/`) |
| `server` | `golang:1.26.1-alpine` | `backend/go.mod` + `go.sum` → `go mod download`; ardından `backend/` → `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server`; boş `/out/data` klasörü |
| son | `gcr.io/distroless/static-debian13:nonroot` | `/app/server`, `/app/web` (= `dist`), `/data` (sahibi `65532:65532`); `ENV ADDR=0.0.0.0:8080 DB_PATH=/data/todos.db STATIC_DIR=/app/web`; `EXPOSE 8080`; `USER 65532:65532`; `ENTRYPOINT ["/app/server"]` |

- Bağımlılık dosyaları kaynaklardan önce kopyalanır; kaynak değişince `npm ci` ve `go mod download`
  önbellekten gelir.
- `ADDR=0.0.0.0:8080` yalnızca konteynerin içindeki ağ içindir; dışarıya açılış §6'daki port
  tanımıyla `127.0.0.1`'e sınırlıdır.

**`.dockerignore`** izin listesidir: önce her şey dışlanır, sonra yalnızca `backend/` ve `frontend/`
alınır; bunların içinden de `backend/data/`, `node_modules/`, `frontend/dist/`, `*.db`, `*.db-shm`,
`*.db-wal`, `*.log` ve `.env*` dışlanır.

## 6. Çalıştırma — `compose.yaml` (depo kökünde)

```yaml
name: todo-app

services:
  app:
    build: .
    image: todo-app:local
    ports:
      - "127.0.0.1:${APP_PORT:-8080}:8080"
    environment:
      GEMINI_API_KEY: ${GEMINI_API_KEY:-}
    volumes:
      - todo-data:/data
    read_only: true
    tmpfs:
      - /tmp
    cap_drop:
      - ALL
    security_opt:
      - no-new-privileges:true
    restart: unless-stopped

volumes:
  todo-data:
```

- `GEMINI_API_KEY` önce kabuk ortamından, yoksa `.env` dosyasından okunur; ikisi de yoksa boş kalır ve
  yapay zekâ kapalı çalışır.
- `docker compose down` sunucuya SIGTERM gönderir; sunucu adım 1–3'teki gibi düzgün kapanır.
  `down` volume'u korur; `down -v` siler.

**`.env.example`** (depoda; kullanıcı `.env` olarak kopyalar):

```
# Bu dosyayı .env adıyla kopyala ve doldur. .env git'e girmez.
# Gemini API anahtarı (ücretsiz, Google AI Studio). Boş bırakılırsa yapay zekâ kapalı çalışır.
GEMINI_API_KEY=
# Uygulamanın açılacağı port: http://localhost:<APP_PORT>
APP_PORT=8080
```

**`.gitignore`**'a eklenir: `.env`, `.env.*`, `!.env.example`.

## 7. README

- Yeni bölüm **Docker ile çalıştırma** (Çalıştırma bölümünden önce): gereksinim (Docker Desktop ya da
  Docker Engine + Compose); `git clone`; isteğe bağlı `.env`; `docker compose up -d`;
  http://localhost:8080; loglar (`docker compose logs -f`); durdurma (`docker compose down`, veri
  kalır) ve veriyi silme (`docker compose down -v`); güncelleme (`git pull` + `docker compose up -d --build`);
  port değiştirme (`APP_PORT`); "giriş yok, yalnızca bu bilgisayardan erişilir" notu.
- Ayar tablosuna `STATIC_DIR` satırı; proje yapısına `internal/web/`, `Dockerfile`, `compose.yaml`.

## 8. Testler ve doğrulama

**Go (`internal/web`, TDD):** §4'teki her kural bir test:
- `GET /` → 200, `index.html` içeriği, `Cache-Control: no-cache`, `nosniff`;
- `GET /assets/<dosya>.js` → 200, JavaScript içerik tipi, `immutable`;
- `GET /favicon.svg` gibi kökteki başka dosya → 200, `Cache-Control` yok;
- `GET /api/health` ve `GET /api` → `api` handler'ı çağrılır;
- olmayan dosya → 404; `GET /assets/` → 404 (listeleme yok); `..` içeren yol `dir` dışındaki bir
  dosyayı döndürmez;
- `POST /` → 405, `Allow: GET, HEAD`; `HEAD /` → 200, gövde yok;
- `index.html` olmayan klasörle `Handler` hata döner.

**Mevcut kontroller:** `go vet`, `go test`, `gofmt`, `npm run lint`, `npm run build`, `npm test` hatasız.

**Docker (orkestratör, bu bilgisayar):** Docker Desktop doğrulama için açılır. Depo geçici bir klasöre
**temiz klonlanır** ve testler oradan çalışır (yerelde git'e girmemiş hiçbir dosya kullanılmaz). Ayrı
proje adı `todo-app-test` ve `APP_PORT=8094`; çalışan geliştirme sunucularına (5173, 8080) dokunulmaz.
1. `docker compose build` başarılı.
2. Anahtarsız `up -d`: `/` → 200 HTML; bir asset → 200; `/api/health` → 200. Edge'de adım 3 uçtan uca
   betiğinin anahtarsız aşaması (adres konteyner): kart `sınıflandırılamadı` + **Tekrar dene**; konsol temiz.
3. Anahtarla `up -d` (aynı volume): betiğin anahtarlı aşaması — Tekrar dene ile kart kategorisine geçer,
   yeni kartlar sınıflandırılır, elle taşınan karta dokunulmaz, log paneli çalışır, konsol temiz.
4. `down` + `up -d`: veriler duruyor.
5. Güvenlik: `docker inspect` → kullanıcı `65532:65532`, `ReadonlyRootfs: true`, `CapDrop: [ALL]`,
   `no-new-privileges`, port yalnızca `127.0.0.1:8094`; imajın dosya listesinde `.db`, `.env`, `.git`,
   `node_modules` yok; anahtarın değeri ne `docker history --no-trunc` çıktısında ne de imaj arşivinde
   (`docker save`) var.
6. Temizlik: `docker compose -p todo-app-test down -v`; geçici klon silinir.

## 9. GitHub'a gönderme

1. `step-4-docker` testleri yeşilse `main`'e `--no-ff` ile merge edilir (adım 1–3 gibi).
2. Push öncesi tarama (bütün commit'ler ve takip edilen dosyalar):
   - Gemini anahtarının değeri (ekrana basılmadan karşılaştırılır);
   - `AIza[0-9A-Za-z_-]{35}`, `ghp_`, `github_pat_`, `sk-` gibi token kalıpları;
   - `password`/`secret`/`token` adlı değişkenlere değer atamaları;
   - takip edilen dosyalarda `*.db*`, `.env`, `*.log`;
   - `gmail.com` adresleri.
   Bulgu varsa push yapılmaz; kullanıcıya gösterilir ve ne yapılacağı sorulur.
3. Kullanıcı github.com'da boş, gizli bir depo açar (README, lisans ya da `.gitignore` eklemeden) ve
   HTTPS URL'sini verir.
4. `git remote add origin <URL>` (kimlik bilgisi içermeyen HTTPS URL); `git push -u origin main`.
   Giriş Git Credential Manager'ın tarayıcı penceresinde yapılır. Pencere orkestratörün kabuğundan
   açılamazsa kullanıcı `! git push -u origin main` çalıştırır.
5. `git ls-remote origin refs/heads/main` → yerel `main` ile aynı hash.

## 10. Kabul kriterleri

1. Temiz bir klonda yalnızca Docker ile `docker compose up -d` → http://localhost:8080 panoyu açar;
   Edge'de konsol temiz.
2. Anahtarla yeni todo kategorisine geçer; anahtarsız `sınıflandırılamadı` + Tekrar dene görünür.
3. `docker compose down` + `up -d` sonrası veriler duruyor; `down -v` siler.
4. Port yalnızca `127.0.0.1`'de; konteyner root olarak çalışmıyor; kök dosya sistemi salt okunur.
5. Anahtar, `.env`, DB ve log dosyaları ne git geçmişinde ne de imajda var.
6. Geliştirme düzeni (`go run` + `npm run dev`) aynı çalışıyor; `go vet`, `go test`, `npm run lint`,
   `npm run build`, `npm test` hatasız.
7. GitHub'da gizli depo var ve uzaktaki `main` yerel `main` ile aynı.

## 11. Ortam notları

- Windows 11; Docker 29.2.1, Compose v5.1.0 (Docker Desktop); git 2.52 + Git Credential Manager.
- Geliştirme sunucuları (Vite 5173, Go 8080, canlı DB `backend/data/`) doğrulama boyunca çalışır kalır.
