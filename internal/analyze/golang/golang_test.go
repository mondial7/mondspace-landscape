package golang

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"testing/fstest"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

const mainGo = `// Command shop serves the shop.
package main

import (
	"embed"
	"net/http"

	"example.com/shop/internal/server"
	"example.com/shop/internal/store"
)

//go:embed web/templates/*.html
var templates embed.FS

//go:embed web/static
var static embed.FS

func main() {
	st := store.Open("data.db")
	http.ListenAndServe(":8080", server.New(st))
}
`

const serverGo = `// Package server exposes the HTTP API.
package server

import (
	"html/template"
	"net/http"
	"os/exec"

	"example.com/shop/internal/store"
)

type Server struct{ mux *http.ServeMux }

func New(st *store.Store) *Server {
	s := &Server{mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /items", s.list)
	s.mux.HandleFunc("POST /items/{id}/buy", s.buy)
	s.mux.Handle("/static/", nil)
	_ = template.Must(template.ParseFS(nil, "x.html"))
	_ = exec.Command("git", "status")
	return s
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {}
func (s *Server) buy(w http.ResponseWriter, r *http.Request)  {}
`

const storeGo = `package store

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

func Open(path string) *Store { return &Store{} }
`

const journalGo = `package journal

import "os"

func Save(b []byte) error {
	_, _ = os.ReadFile("config.json")
	return os.WriteFile("journal.json", b, 0o600)
}
`

const storeTestGo = `package store

import "testing"

func TestOpen(t *testing.T) {}
`

func fixture(t *testing.T) *scan.Repo {
	t.Helper()
	fsys := fstest.MapFS{
		"go.mod":                       {Data: []byte("module example.com/shop\n\ngo 1.22\n")},
		"main.go":                      {Data: []byte(mainGo)},
		"internal/server/server.go":    {Data: []byte(serverGo)},
		"internal/store/store.go":      {Data: []byte(storeGo)},
		"internal/store/store_test.go": {Data: []byte(storeTestGo)},
		"internal/journal/journal.go":  {Data: []byte(journalGo)},
		"internal/broken/broken.go":    {Data: []byte("package broken\nfunc {")},
		"web/templates/index.html":     {Data: []byte("<html></html>")},
	}
	repo, err := scan.Scan(fsys, "shop")
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func analyzeFixture(t *testing.T) analyze.Facts {
	t.Helper()
	facts, err := New().Analyze(context.Background(), fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func unit(t *testing.T, f analyze.Facts, id string) analyze.Unit {
	t.Helper()
	for _, u := range f.Units {
		if u.ID == id {
			return u
		}
	}
	t.Fatalf("unit %q not found in %v", id, f.Units)
	return analyze.Unit{}
}

func TestUnitsPerPackage(t *testing.T) {
	f := analyzeFixture(t)
	var ids []string
	for _, u := range f.Units {
		ids = append(ids, u.ID)
	}
	sort.Strings(ids)
	want := []string{"go:.", "go:internal/broken", "go:internal/journal", "go:internal/server", "go:internal/store"}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("units = %v, want %v", ids, want)
	}

	root := unit(t, f, "go:.")
	if !root.Entry || root.Name != "shop" || root.Doc != "Command shop serves the shop." {
		t.Errorf("root unit = %+v, want entry named after module with doc", root)
	}
	if !hasTag(root, "http-server") {
		t.Errorf("root tags = %v, want http-server (ListenAndServe)", root.Tags)
	}
	store := unit(t, f, "go:internal/store")
	if !reflect.DeepEqual(store.Files, []string{"internal/store/store.go", "internal/store/store_test.go"}) {
		t.Errorf("store files = %v", store.Files)
	}
	if store.Entry {
		t.Error("store should not be an entry point")
	}
	if !reflect.DeepEqual(store.Symbols, []string{"Open", "Store"}) {
		t.Errorf("store symbols = %v", store.Symbols)
	}
}

func TestInternalDependencies(t *testing.T) {
	f := analyzeFixture(t)
	var got []string
	for _, d := range f.Deps {
		if d.Kind != graph.EdgeDepends {
			t.Errorf("dep kind = %s, want depends", d.Kind)
		}
		got = append(got, d.From+"->"+d.To)
	}
	sort.Strings(got)
	want := []string{"go:.->go:internal/server", "go:.->go:internal/store", "go:internal/server->go:internal/store"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("deps = %v, want %v", got, want)
	}
}

func TestRoutesAndTags(t *testing.T) {
	f := analyzeFixture(t)
	srv := unit(t, f, "go:internal/server")
	want := []string{"/static/", "GET /items", "POST /items/{id}/buy"}
	if !reflect.DeepEqual(srv.Routes, want) {
		t.Errorf("routes = %v, want %v", srv.Routes, want)
	}
	for _, tag := range []string{"http-server", "templates"} {
		if !hasTag(srv, tag) {
			t.Errorf("server tags = %v, missing %s", srv.Tags, tag)
		}
	}
}

func TestExternalsCollapseGenericSQLIntoDriver(t *testing.T) {
	f := analyzeFixture(t)
	var exts []string
	for _, e := range f.Externals {
		exts = append(exts, e.ID)
	}
	sort.Strings(exts)
	if want := []string{"filesystem", "os-processes", "sqlite"}; !reflect.DeepEqual(exts, want) {
		t.Errorf("externals = %v, want %v", exts, want)
	}
	var uses []string
	for _, u := range f.Uses {
		uses = append(uses, u.Unit+"->"+u.External+":"+string(u.Kind))
	}
	sort.Strings(uses)
	want := []string{"go:internal/journal->filesystem:data", "go:internal/server->os-processes:uses", "go:internal/store->sqlite:data"}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses = %v, want %v", uses, want)
	}
}

func TestEmbedsBecomeRefs(t *testing.T) {
	f := analyzeFixture(t)
	var got []string
	for _, r := range f.Refs {
		got = append(got, r.From+"->"+r.Path)
	}
	sort.Strings(got)
	want := []string{"go:.->web/static", "go:.->web/templates/*.html"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refs = %v, want %v", got, want)
	}
}

func TestBrokenFilesProduceWarningNotFailure(t *testing.T) {
	f := analyzeFixture(t)
	if len(f.Warnings) != 1 {
		t.Errorf("warnings = %v, want 1 for the broken file", f.Warnings)
	}
}

func TestNoGoFilesNoFacts(t *testing.T) {
	repo, err := scan.Scan(fstest.MapFS{"a.py": {Data: []byte("x=1")}}, "py")
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Analyze(context.Background(), repo)
	if err != nil || len(f.Units) != 0 {
		t.Errorf("Analyze(no go) = %+v, %v; want empty", f, err)
	}
}

func hasTag(u analyze.Unit, tag string) bool {
	for _, t := range u.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func TestDisplayNameSkipsMajorVersionSuffix(t *testing.T) {
	tests := []struct{ pkg, dir, module, want string }{
		{"main", ".", "github.com/charmbracelet/glow/v3", "glow"},
		{"main", ".", "example.com/shop", "shop"},
		{"main", "cmd/tool", "example.com/x/v2", "tool"},
		{"store", "internal/store", "example.com/x", "store"},
		{"", ".", "", "main"},
	}
	for _, tt := range tests {
		if got := displayName(tt.pkg, tt.dir, tt.module); got != tt.want {
			t.Errorf("displayName(%q,%q,%q) = %q, want %q", tt.pkg, tt.dir, tt.module, got, tt.want)
		}
	}
}

func TestDepsRecordTheImportingFile(t *testing.T) {
	f := analyzeFixture(t)
	for _, d := range f.Deps {
		if d.From == "go:internal/server" && d.To == "go:internal/store" && d.File != "internal/server/server.go" {
			t.Errorf("dep file = %q, want internal/server/server.go", d.File)
		}
	}
}
