package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const indexHTML = "<!doctype html><title>t</title>"

// newSite writes a small built frontend to a temporary folder and returns that
// folder. Next to it, outside the site, lies secret.txt, which no request may
// reach.
func newSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	site := filepath.Join(root, "site")
	files := map[string]string{
		"index.html":           indexHTML,
		"robots.txt":           "User-agent: *",
		"assets/app-abc123.js": "console.log(1)",
	}
	for name, body := range files {
		p := filepath.Join(site, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	return site
}

// newHandler serves newSite next to a fake API, which answers 418 and its path.
func newHandler(t *testing.T) http.Handler {
	t.Helper()
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		io.WriteString(w, "api "+r.URL.Path)
	})
	h, err := Handler(api, newSite(t))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func do(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func wantHeaders(t *testing.T, rec *httptest.ResponseRecorder, want map[string]string) {
	t.Helper()
	for name, value := range want {
		if got := rec.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
}

func TestRootServesIndex(t *testing.T) {
	rec := do(newHandler(t), http.MethodGet, "/")
	if rec.Code != http.StatusOK || rec.Body.String() != indexHTML {
		t.Fatalf("GET / = %d %q, want 200 and index.html", rec.Code, rec.Body.String())
	}
	wantHeaders(t, rec, map[string]string{
		"Content-Type":           "text/html; charset=utf-8",
		"Cache-Control":          "no-cache",
		"X-Content-Type-Options": "nosniff",
	})
}

func TestIndexByName(t *testing.T) {
	rec := do(newHandler(t), http.MethodGet, "/index.html")
	if rec.Code != http.StatusOK || rec.Body.String() != indexHTML {
		t.Fatalf("GET /index.html = %d %q, want 200 and index.html", rec.Code, rec.Body.String())
	}
	wantHeaders(t, rec, map[string]string{"Cache-Control": "no-cache"})
}

func TestAssetsAreImmutable(t *testing.T) {
	rec := do(newHandler(t), http.MethodGet, "/assets/app-abc123.js")
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log(1)" {
		t.Fatalf("GET asset = %d %q, want 200 and the file", rec.Code, rec.Body.String())
	}
	wantHeaders(t, rec, map[string]string{
		"Cache-Control":          "public, max-age=31536000, immutable",
		"X-Content-Type-Options": "nosniff",
	})
	// The type comes from the extension: Go's own table on Linux (the Docker
	// image) says text/javascript, the registry on Windows application/javascript.
	// Browsers run both as scripts.
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") && !strings.HasPrefix(ct, "application/javascript") {
		t.Errorf("Content-Type = %q, want a JavaScript type", ct)
	}
}

func TestOtherFilesHaveNoCacheControl(t *testing.T) {
	rec := do(newHandler(t), http.MethodGet, "/robots.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != "User-agent: *" {
		t.Fatalf("GET /robots.txt = %d %q, want 200 and the file", rec.Code, rec.Body.String())
	}
	wantHeaders(t, rec, map[string]string{
		"Cache-Control":          "",
		"X-Content-Type-Options": "nosniff",
	})
}

func TestAPIPathsGoToAPI(t *testing.T) {
	h := newHandler(t)
	for _, target := range []string{"/api", "/api/health", "/api/todos/1"} {
		rec := do(h, http.MethodGet, target)
		if rec.Code != http.StatusTeapot || rec.Body.String() != "api "+target {
			t.Errorf("GET %s = %d %q, want the API's answer", target, rec.Code, rec.Body.String())
		}
	}
	// Only /api and paths under /api/ belong to the API.
	rec := do(h, http.MethodGet, "/apix")
	if rec.Code != http.StatusNotFound || strings.HasPrefix(rec.Body.String(), "api ") {
		t.Errorf("GET /apix = %d %q, want a 404 of the static files", rec.Code, rec.Body.String())
	}
}

func TestNotFound(t *testing.T) {
	h := newHandler(t)
	for _, target := range []string{"/missing.js", "/assets/", "/assets", "/../secret.txt", "/%2e%2e/secret.txt"} {
		rec := do(h, http.MethodGet, target)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("GET %s = %d %q, want 404 without the file", target, rec.Code, rec.Body.String())
		}
	}
}

func TestOnlyGetAndHead(t *testing.T) {
	h := newHandler(t)
	for _, req := range [][2]string{{http.MethodPost, "/"}, {http.MethodDelete, "/assets/app-abc123.js"}} {
		rec := do(h, req[0], req[1])
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s %s = %d, want 405", req[0], req[1], rec.Code)
		}
		wantHeaders(t, rec, map[string]string{"Allow": "GET, HEAD"})
	}
	rec := do(h, http.MethodHead, "/")
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD / = %d with %d body bytes, want 200 and no body", rec.Code, rec.Body.Len())
	}
}

func TestHandlerNeedsIndex(t *testing.T) {
	dir := t.TempDir()
	_, err := Handler(http.NotFoundHandler(), dir)
	want := fmt.Sprintf("static dir %q: index.html not found", dir)
	if err == nil || err.Error() != want {
		t.Fatalf("Handler on a folder without index.html: err = %v, want %q", err, want)
	}
}
