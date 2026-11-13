package web

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestIndexReferencesEmbeddedAssets(t *testing.T) {
	index, err := fs.ReadFile(FS(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	refs := regexp.MustCompile(`(?:src|href)="/static/([^"]+)"`).FindAllSubmatch(index, -1)
	if len(refs) < 4 {
		t.Fatalf("expected css, js and vendor references, found %d", len(refs))
	}
	h := Handler()
	for _, m := range refs {
		p := string(m[1])
		if _, err := fs.Stat(FS(), p); err != nil {
			t.Errorf("index.html references missing asset %s", p)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/static/"+p, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /static/%s = %d", p, rec.Code)
		}
	}
}

func TestRootServesIndex(t *testing.T) {
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("GET / = %d, cache-control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestDevDirOverridesEmbeddedUI(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("dev ui"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANDSCAPE_WEB_DIR", dir)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Body.String(); got != "dev ui" {
		t.Errorf("GET / with LANDSCAPE_WEB_DIR = %q, want the file from disk", got)
	}
}
