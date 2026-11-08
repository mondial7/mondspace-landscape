package pipeline

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
)

func repo() fstest.MapFS {
	return fstest.MapFS{
		"README.md": {Data: []byte("# Demo\n\nDemo app.\n")},
		"go.mod":    {Data: []byte("module example.com/demo\n")},
		"main.go": {Data: []byte(`package main

import (
	"net/http"

	"example.com/demo/internal/store"
)

func main() {
	http.HandleFunc("GET /items", nil)
	store.Open()
	http.ListenAndServe(":8080", nil)
}
`)},
		"internal/store/store.go": {Data: []byte("package store\n\nimport _ \"modernc.org/sqlite\"\n\nfunc Open() { q := `SELECT * FROM items`; _ = q }\n")},
		"web/index.html":          {Data: []byte(`<a href="/items">items</a>`)},
	}
}

type recordingEnricher struct{ called bool }

func (r *recordingEnricher) Enrich(ctx context.Context, m *landscape.Model, src Source) error {
	r.called = true
	return m.Graph.Update(landscape.SystemID, func(n *graph.Node) { n.Description = "enriched" })
}

func TestRunEndToEnd(t *testing.T) {
	enr := &recordingEnricher{}
	m, err := New(WithEnricher(enr)).Run(context.Background(), repo(), "demo")
	if err != nil {
		t.Fatal(err)
	}
	if !enr.called {
		t.Error("enricher not called")
	}
	sys, _ := m.Graph.Node(landscape.SystemID)
	if sys.Description != "enriched" {
		t.Errorf("system description = %q", sys.Description)
	}
	for _, id := range []string{"ctr:.", "cmp:.", "cmp:internal/store", "cmp:web", "ext:sqlite", "person:user"} {
		if !m.Graph.Has(id) {
			t.Errorf("missing node %s", id)
		}
	}
	found := false
	for _, e := range m.Graph.Edges() {
		if e.From == "cmp:internal/store" && e.To == "ext:sqlite" && e.Label == "reads items" {
			found = true
		}
	}
	if !found {
		t.Errorf("missing data flow store→sqlite: %v", m.Graph.Edges())
	}
}

type failingEnricher struct{}

func (failingEnricher) Enrich(context.Context, *landscape.Model, Source) error {
	return errors.New("llm down")
}

func TestEnricherFailureIsAWarning(t *testing.T) {
	m, err := New(WithEnricher(failingEnricher{})).Run(context.Background(), repo(), "demo")
	if err != nil {
		t.Fatalf("Run = %v, want success despite enrichment failure", err)
	}
	if len(m.Summary.Warnings) == 0 {
		t.Error("expected an enrichment warning")
	}
}

func TestReadmeLookupIsCaseInsensitive(t *testing.T) {
	fsys := fstest.MapFS{"readme.MD": {Data: []byte("# X\n\nHello.\n")}, "a.go": {Data: []byte("package a")}}
	m, err := New().Run(context.Background(), fsys, "x")
	if err != nil {
		t.Fatal(err)
	}
	if sys, _ := m.Graph.Node(landscape.SystemID); sys.Description != "Hello." {
		t.Errorf("description = %q", sys.Description)
	}
}
