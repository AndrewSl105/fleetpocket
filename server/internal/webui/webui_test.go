package webui

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestHandler(t *testing.T) {
	dist := fstest.MapFS{
		"index.html":           {Data: []byte("<html>app</html>")},
		"assets/app-abc123.js": {Data: []byte("js")},
		"sw.js":                {Data: []byte("sw")},
		"manifest.webmanifest": {Data: []byte("{}")},
	}
	h := Handler(dist)
	tests := []struct {
		path, cache, body string
		status            int
	}{
		{"/", "no-cache", "<html>app</html>", 200},
		{"/tasks/12", "no-cache", "<html>app</html>", 200},
		{"/assets/app-abc123.js", "public, max-age=31536000, immutable", "js", 200},
		{"/sw.js", "no-cache", "sw", 200},
		{"/manifest.webmanifest", "no-cache", "{}", 200},
		{"/missing.js", "", "", 404},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tt.path, nil))
		if rec.Code != tt.status {
			t.Errorf("%s: status %d, want %d", tt.path, rec.Code, tt.status)
			continue
		}
		if tt.status != 200 {
			continue
		}
		if got := rec.Header().Get("Cache-Control"); got != tt.cache {
			t.Errorf("%s: Cache-Control %q, want %q", tt.path, got, tt.cache)
		}
		if rec.Body.String() != tt.body {
			t.Errorf("%s: body %q", tt.path, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", rec.Code)
	}
}
