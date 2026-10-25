// Package web serves the built frontend next to the API, so that one server on
// one port carries the whole app (the Docker image runs it that way).
package web

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// Handler serves api for /api and every path under /api/, and the built
// frontend in dir for every other path.
//
// "/" serves index.html. Folders are never listed, and no path reaches a file
// outside dir. index.html is not cached, so that a new build shows at once;
// the files under assets/ carry a hash of their content in their names, so
// they are cached for good.
func Handler(api http.Handler, dir string) (http.Handler, error) {
	fsys := os.DirFS(dir)
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("static dir %q: index.html not found", dir)
		}
		return nil, fmt.Errorf("static dir %q: %w", dir, err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			api.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		serveFile(w, r, fsys)
	}), nil
}

// serveFile answers with the file in fsys that the path of r names, or with
// 404 if there is no such file.
func serveFile(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	// Cleaning a rooted path removes every "..", so the name stays inside fsys.
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	f, err := fsys.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("X-Content-Type-Options", "nosniff")
	switch {
	case name == "index.html":
		header.Set("Cache-Control", "no-cache")
	case strings.HasPrefix(name, "assets/"):
		header.Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, name, info.ModTime(), content)
}
