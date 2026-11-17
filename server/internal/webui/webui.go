// Package webui serves the built web app (web/dist) from the binary: the
// local-mode CLI embeds it with the embedweb build tag; server mode keeps
// nginx. Cache headers match web/nginx: hashed assets immutable, everything
// else revalidated.
package webui

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Handler serves files from dist and falls back to index.html for client
// routes (paths without a file extension that do not exist).
func Handler(dist fs.FS) http.Handler {
	files := http.FileServerFS(dist)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, dist)
			return
		}
		if _, err := fs.Stat(dist, name); err != nil {
			if path.Ext(name) == "" {
				serveIndex(w, r, dist) // SPA route such as /tasks/12
				return
			}
			http.NotFound(w, r)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(name, "assets/") {
			h.Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			h.Set("Cache-Control", "no-cache")
		}
		if name == "sw.js" {
			h.Set("Service-Worker-Allowed", "/")
		}
		if name == "manifest.webmanifest" {
			h.Set("Content-Type", "application/manifest+json")
		}
		files.ServeHTTP(w, r)
	})
}

func serveIndex(w http.ResponseWriter, r *http.Request, dist fs.FS) {
	b, err := fs.ReadFile(dist, "index.html")
	if err != nil {
		http.Error(w, "web app not built into this binary", http.StatusNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(b)
}
