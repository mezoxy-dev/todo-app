# Todo App Adım 4 — Docker ve GitHub Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Depoyu indiren herkes uygulamayı yalnızca Docker ile tek komutla çalıştırabilsin; kod gizli bir GitHub deposuna güvenle gönderilsin.

**Architecture:** Go sunucusu yeni `STATIC_DIR` ayarıyla derlenmiş arayüzü de sunar (`internal/web`). Çok aşamalı Dockerfile arayüzü ve sunucuyu derleyip shell'siz, root olmayan bir imaj üretir; `compose.yaml` portu yalnızca `127.0.0.1`'e açar, veriyi volume'da tutar, anahtarı çalışma anında alır. Son görev `main`'e merge eder, geçmişi tarar ve push eder.

**Tech Stack:** Go 1.26.1 (`net/http`, `io/fs`), Docker 29.2.1 + Compose v5.1.0, `node:24.18.0-alpine`, `golang:1.26.1-alpine`, `gcr.io/distroless/static-debian13:nonroot`; doğrulamada `playwright-core` + Edge.

**Spec:** `docs/superpowers/specs/2026-10-25-todo-app-docker-design.md` — plan bu spec'e dayanır; uygulayan ikisini de okur.

## Global Constraints

- Branch `step-4-docker`. Windows 11; Go 1.26.1; Node 24.18.0; Docker 29.2.1, Compose v5.1.0.
- Yeni Go ya da npm bağımlılığı yok (adım 3 kuralı geçerli); `internal/web` yalnızca standart kütüphane.
- Taban imajlar birebir: `node:24.18.0-alpine`, `golang:1.26.1-alpine`, `gcr.io/distroless/static-debian13:nonroot`.
- Konteyner içi ayarlar birebir: `ADDR=0.0.0.0:8080`, `DB_PATH=/data/todos.db`, `STATIC_DIR=/app/web`; kullanıcı `65532:65532`.
- Port tanımı birebir: `127.0.0.1:${APP_PORT:-8080}:8080`. Volume `todo-data` → `/data`. Proje adı `todo-app`.
- Başlıklar birebir: `X-Content-Type-Options: nosniff`; `index.html` → `Cache-Control: no-cache`; `/assets/` → `Cache-Control: public, max-age=31536000, immutable`; 405'te `Allow: GET, HEAD`.
- Hata mesajı birebir: `static dir "<dir>": index.html not found` (`%q` ile).
- Gemini anahtarı yalnızca çalışma anında; hiçbir dosyaya, imaja, commit'e ya da komut çıktısına yazılmaz. Kullanıcı ortamından ekrana basılmadan okunur: `[Environment]::GetEnvironmentVariable('GEMINI_API_KEY','User')`.
- Geliştirme sunucuları (Vite 5173, Go 8080, canlı DB `backend/data/`) durdurulmaz ve kullanılmaz. Docker doğrulaması proje adı `todo-app-test` ve `APP_PORT=8094` ile yapılır.
- `git add` yalnızca adı verilen dosyalarla (izlenmeyen `.claude/` commit'lenmez).

## Review Focus

1. **Host portu dolu** (8080'de geliştirme sunucusu var) — `docker compose up` port hatası verir; kişi `APP_PORT` ile başka port seçebilmeli. → Görev 2 doğrulaması 8080 doluyken `APP_PORT=8094` ile çalışır; README bunu anlatır.
2. **Yeni volume root olmayan kullanıcıya yazılabilir olmalı** — ilk açılışta DB oluşur, yazma çalışır. → Görev 2 doğrulamasında yeni volume'la ilk `up` sonrası todo eklenir.
3. **`docker compose down` sunucuyu düzgün kapatmalı** — WAL'deki veri kaybolmaz. → Görev 2 doğrulamasında `down` + `up` sonrası 4 todo durur.
4. **İmaj güncellenince eski arayüz** — `index.html` önbelleğe alınmamalı. → Görev 1 `TestRootServesIndex` (`no-cache`).
5. **Klasör dışına çıkan yollar** (`..`, kodlanmış `%2e%2e`) — `dir` dışındaki dosya dönmemeli. → Görev 1 `TestNotFound`.

---

### Görev 1: Sunucunun derlenmiş arayüzü sunması (`internal/web` + `STATIC_DIR`)

**Files:**
- Create: `backend/internal/web/web.go`, `backend/internal/web/web_test.go`
- Modify: `backend/cmd/server/main.go` (paket yorumu, `run`, `listening` kaydı)

**Interfaces:**
- Produces: `func Handler(api http.Handler, dir string) (http.Handler, error)` (paket `web`); ortam değişkeni `STATIC_DIR`.

- [ ] **Step 1: Başarısız testleri yaz** — `backend/internal/web/web_test.go` (paket `web`):

```go
// newSite: t.TempDir()/site içinde index.html ("<!doctype html><title>t</title>"),
// robots.txt ("User-agent: *"), assets/app-abc123.js ("console.log(1)");
// klasörün DIŞINDA t.TempDir()/secret.txt ("secret"). site yolunu döndürür.
// newHandler: Handler(fakeAPI, site); fakeAPI 418 + "api "+r.URL.Path yazar.
// do(h, method, target) *httptest.ResponseRecorder

func TestRootServesIndex(t *testing.T) {
	// GET / → 200; gövde index.html içeriği; Content-Type "text/html; charset=utf-8";
	// Cache-Control "no-cache"; X-Content-Type-Options "nosniff"
}
func TestIndexByName(t *testing.T)               { /* GET /index.html → 200, Cache-Control "no-cache" */ }
func TestAssetsAreImmutable(t *testing.T) {
	// GET /assets/app-abc123.js → 200; Content-Type "text/javascript; charset=utf-8";
	// Cache-Control "public, max-age=31536000, immutable"; nosniff
}
func TestOtherFilesHaveNoCacheControl(t *testing.T) { /* GET /robots.txt → 200, Cache-Control "" */ }
func TestAPIPathsGoToAPI(t *testing.T) {
	// /api, /api/health, /api/todos/1 → 418, gövde "api <yol>";
	// /apix → 404 ve gövde "api " ile başlamaz (API'ye gitmez)
}
func TestNotFound(t *testing.T) {
	// /missing.js, /assets/, /assets, /../secret.txt, /%2e%2e/secret.txt → 404;
	// hiçbir gövde "secret" içermez
}
func TestOnlyGetAndHead(t *testing.T) {
	// POST / ve DELETE /assets/app-abc123.js → 405, Allow "GET, HEAD";
	// HEAD / → 200, boş gövde
}
func TestHandlerNeedsIndex(t *testing.T) {
	// index.html olmayan klasör: err != nil ve
	// err.Error() == fmt.Sprintf("static dir %q: index.html not found", dir)
}
```

- [ ] **Step 2: Başarısız olduğunu gör** — Run: `go -C backend test ./internal/web/` → FAIL (`undefined: Handler`).

- [ ] **Step 3: `Handler`'ı yaz** — `backend/internal/web/web.go`, paket yorumu ile. Kurulumda `fsys := os.DirFS(dir)` ve `fs.Stat(fsys, "index.html")` (yoksa spec'teki hata). İstekte sıra: yol `/api` ya da `/api/` önekli → `api.ServeHTTP`; yöntem `GET`/`HEAD` değil → `Allow` + 405; `name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")`, boşsa `index.html`; `fsys.Open(name)` hata ya da klasör → `http.NotFound`; başlıklar; `http.ServeContent(w, r, name, info.ModTime(), f.(io.ReadSeeker))`. (`http.ServeFile`/`FileServer` kullanma: `/index.html`'i yönlendirir ve klasör listeler.)

- [ ] **Step 4: Testler geçsin** — Run: `go -C backend test ./internal/web/` → PASS.

- [ ] **Step 5: `cmd/server`'a bağla** — `main.go` paket yorumundaki listeye: `STATIC_DIR: a folder with the built frontend to serve next to the API (default empty: API only).` `run` içinde `net.Listen`'dan **önce**: `handler := api.New(store, logs, worker)`; `STATIC_DIR` doluysa `handler, err = web.Handler(handler, dir)`, hata → `return err`. `http.Server{Handler: handler}`. `listening` kaydına `"static", dir`.

- [ ] **Step 6: Elle doğrula** — `npm --prefix frontend run build`; exe'yi scratchpad'e derle; geçici `DB_PATH`, `ADDR=127.0.0.1:8095`:
  - `STATIC_DIR=<repo>/frontend/dist` → `curl /` 200 ve gövdede `<div id="root">`; `curl /api/health` → `{"status":"ok"}`; `curl -I /assets/<index.html'deki js>` → `immutable`.
  - `STATIC_DIR=<boş klasör>` → çıkış kodu 1, stderr'de `index.html not found`.
  - `STATIC_DIR` yok → `curl /` 404 (bugünkü gibi yalnızca API).

- [ ] **Step 7: Tüm kontroller** — `go -C backend vet ./...`, `go -C backend test ./...` → PASS; `gofmt -l backend` → çıktı yok.

- [ ] **Step 8: Commit**

```bash
git add backend/internal/web/web.go backend/internal/web/web_test.go backend/cmd/server/main.go
git commit -m "feat(server): serve the built frontend from STATIC_DIR"
```

---

### Görev 2: Docker paketleme, README ve doğrulama

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `compose.yaml`, `.env.example`
- Modify: `.gitignore`, `README.md`

**Interfaces:**
- Consumes: Görev 1'in `STATIC_DIR` davranışı.

- [ ] **Step 1: `Dockerfile`** (depo kökü; spec §5):

```dockerfile
FROM node:24.18.0-alpine AS web
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

FROM golang:1.26.1-alpine AS server
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server && mkdir /out/data

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=server /out/server /app/server
COPY --from=web /src/frontend/dist /app/web
COPY --from=server --chown=65532:65532 /out/data /data
ENV ADDR=0.0.0.0:8080 DB_PATH=/data/todos.db STATIC_DIR=/app/web
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/app/server"]
```

- [ ] **Step 2: `.dockerignore`** (izin listesi):

```
*
!backend/
!frontend/
backend/data/
**/node_modules/
frontend/dist/
**/*.db
**/*.db-shm
**/*.db-wal
**/*.log
**/.env*
```

- [ ] **Step 3: `compose.yaml` ve `.env.example`** — ikisi de spec §6'daki metinle birebir. `.gitignore` sonuna `.env`, `.env.*`, `!.env.example` (bir yorum satırıyla).

- [ ] **Step 4: README** — spec §7: "Docker ile çalıştırma" bölümü "Çalıştırma"dan önce (`git clone https://github.com/<kullanıcı>/<depo>.git`, `.env`, `docker compose up -d`, http://localhost:8080, `docker compose logs -f`, `down` / `down -v`, `git pull` + `docker compose up -d --build`, `APP_PORT`, "giriş yok, yalnızca bu bilgisayar" notu); ayar tablosuna `STATIC_DIR`; proje yapısına `internal/web/`, `Dockerfile`, `compose.yaml`.

- [ ] **Step 5: Commit** (doğrulama temiz klondan yapılacağı için önce commit):

```bash
git add Dockerfile .dockerignore compose.yaml .env.example .gitignore README.md
git commit -m "feat: Docker image and compose setup for running the app anywhere"
```

- [ ] **Step 6: Docker'ı hazırla** — `docker info` başarısızsa `Start-Process "C:\Program Files\Docker\Docker\Docker Desktop.exe"`; `docker info` başarılı olana kadar bekle (en çok 3 dk). Adım 3 E2E betiğine (`<önceki scratchpad>/e2e/e2e3.mjs`) `APP_URL` ortam değişkeni ekle (varsayılan `http://localhost:5173`).

- [ ] **Step 7: Temiz klondan doğrula** — scratchpad'de bir betikle (`git clone -b step-4-docker <repo> <scratch>/clone`, tüm `docker compose` komutları `-p todo-app-test`, `APP_PORT=8094`):
  1. `docker compose build` → başarılı.
  2. `GEMINI_API_KEY` boşken `up -d`; `/api/health` hazır → `curl /` 200 HTML, `index.html`'deki js 200, `/api/health` 200.
  3. `APP_URL=http://127.0.0.1:8094 API_URL=http://127.0.0.1:8094 node e2e3.mjs nokey <shots>` → `E2E nokey: PASS`.
  4. Anahtarla `up -d` (konteyner yeniden oluşur, volume aynı) → `node e2e3.mjs key <shots>` → `E2E key: PASS`.
  5. `down` + `up -d` → `/api/todos`'ta 4 başlık: `süt al`, `tatil için otel bak`, `elektrik faturasını öde`, `annemi ara`.
  6. `docker inspect -f '{{.Config.User}} {{.HostConfig.ReadonlyRootfs}} {{.HostConfig.CapDrop}} {{.HostConfig.SecurityOpt}}' todo-app-test-app-1` → `65532:65532 true [ALL] [no-new-privileges:true]`; `docker port todo-app-test-app-1` → `8080/tcp -> 127.0.0.1:8094`. (`Config.Env` basılmaz: anahtar içerir.)
  7. `docker export todo-app-test-app-1 | tar -t | grep -E '\.db|\.env|\.git/|node_modules/'` → çıktı yok; anahtar `docker export ... | grep -ac` ve `docker history --no-trunc todo-app:local | grep -c` → `0`.
  8. `down -v` → `docker volume ls -q | grep todo-app-test` çıktı yok (veri silindi); klon silinir.
  Bir madde başarısızsa düzelt, yeni commit, Step 7'yi baştan çalıştır.

---

### Görev 3: `main`'e merge, push öncesi tarama, GitHub'a push

- [ ] **Step 1: Merge** — `git checkout main`; `git merge --no-ff step-4-docker -m "Merge branch 'step-4-docker': Docker packaging (step 4)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"`; `go -C backend vet ./...`, `go -C backend test ./...`, `npm --prefix frontend run lint`, `npm --prefix frontend run build`, `npm --prefix frontend test` → PASS; `git branch -d step-4-docker`.

- [ ] **Step 2: Tarama** (bütün commit'ler; anahtar değişkende, ekrana basılmaz) — hepsinde beklenen: `0` ya da çıktı yok:
  - `git log -p --all | grep -c "$KEY"`;
  - `git log -p --all | grep -nE 'AIza[0-9A-Za-z_-]{35}|ghp_[0-9A-Za-z]{20,}|github_pat_|sk-[0-9A-Za-z]{20,}'`;
  - `git log -p --all | grep -inE "(password|secret|token)[\"']?\s*[:=]\s*[\"'][^\"']{8,}"` (çıkanlar tek tek bakılır; testlerdeki sahte değerler kabul);
  - `git ls-files | grep -E '\.db|(^|/)\.env$|\.log$'`;
  - `git log -p --all | grep -c 'gmail.com'`.
  Bulgu varsa dur, kullanıcıya göster.

- [ ] **Step 3: Depo adresi** — kullanıcı boş, gizli depoyu açar (README/lisans/`.gitignore` yok) ve HTTPS URL'sini verir.

- [ ] **Step 4: Remote** — `git remote add origin <URL>`; `git remote get-url origin` → `https://github.com/...` ve içinde `@` yok.

- [ ] **Step 5: Push** — kullanıcıya tarayıcıda giriş penceresinin açılacağını söyle; `git push -u origin main` (zaman aşımı 5 dk). Pencere açılmaz ya da komut kimlik isterse kullanıcı `! git push -u origin main` çalıştırır.

- [ ] **Step 6: Doğrula** — `git ls-remote origin refs/heads/main` hash'i = `git rev-parse main`.
