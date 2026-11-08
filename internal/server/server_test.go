package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mondial7/mondspace-landscape/internal/enrich"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
	"github.com/mondial7/mondspace-landscape/internal/source"
)

type fakeResolver struct{ err error }

func (f fakeResolver) Resolve(_ context.Context, in string) (source.Repo, error) {
	if f.err != nil {
		return source.Repo{}, f.err
	}
	return source.Repo{Dir: "/tmp/" + in, Name: in}, nil
}

type fakeAnalyzer struct{}

func (fakeAnalyzer) Run(_ context.Context, _ fs.FS, name string) (*landscape.Model, error) {
	g := graph.New()
	for _, n := range []graph.Node{
		{ID: landscape.SystemID, Name: name, Kind: graph.KindSystem},
		{ID: "ctr:a", Name: "a", Kind: graph.KindContainer, Parent: landscape.SystemID},
		{ID: "ext:db", Name: "DB", Kind: graph.KindExternal},
	} {
		if err := g.AddNode(n); err != nil {
			return nil, err
		}
	}
	if err := g.AddEdge(graph.Edge{From: "ctr:a", To: "ext:db", Kind: graph.EdgeData, Label: "writes"}); err != nil {
		return nil, err
	}
	return &landscape.Model{Graph: g, Summary: landscape.Summary{Name: name, Files: 3}}, nil
}

type fakeEnricher struct{}

func (fakeEnricher) Label() string { return "fake/model" }
func (fakeEnricher) Run(_ context.Context, _ *landscape.Model, _ enrich.Source, h enrich.Hooks) error {
	h.Progress(enrich.Progress{Stage: "components", Done: 1, Total: 1})
	h.Apply(enrich.Results{"ctr:a": "Does A."})
	return nil
}

func newTestServer(t *testing.T, opts ...func(*Config)) http.Handler {
	t.Helper()
	cfg := Config{
		Resolver: fakeResolver{},
		Analyzer: fakeAnalyzer{},
		OpenFS:   func(string) fs.FS { return fstest.MapFS{} },
	}
	for _, o := range opts {
		o(&cfg)
	}
	return New(cfg).Handler()
}

func do(t *testing.T, h http.Handler, method, url, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Host = "localhost:7777"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return v
}

func waitFor(t *testing.T, h http.Handler, id string, status Status) Job {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rec := do(t, h, "GET", "/api/analyses/"+id, "")
		j := decode[Job](t, rec)
		if j.Status == status {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s never reached %s", id, status)
	return Job{}
}

func TestAnalyzeLifecycle(t *testing.T) {
	h := newTestServer(t, func(c *Config) { c.Enricher = fakeEnricher{} })
	rec := do(t, h, "POST", "/api/analyses", `{"source":"demo"}`)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST status = %d, body %s", rec.Code, rec.Body)
	}
	j := decode[Job](t, rec)
	if j.ID == "" || j.Source != "demo" {
		t.Fatalf("job = %+v", j)
	}
	done := waitFor(t, h, j.ID, StatusReady)
	if done.Summary == nil || done.Summary.Name != "demo" || done.LLM != "fake/model" || done.Dir != "/tmp/demo" {
		t.Errorf("ready job = %+v", done)
	}

	rec = do(t, h, "GET", "/api/analyses/"+j.ID+"/view?focus=sys", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("view status = %d %s", rec.Code, rec.Body)
	}
	v := decode[graph.View](t, rec)
	if len(v.Nodes) != 2 || len(v.Edges) != 1 {
		t.Errorf("view = %+v", v)
	}
	for _, n := range v.Nodes {
		if n.ID == "ctr:a" && n.Description != "Does A." {
			t.Errorf("enriched description not visible: %q", n.Description)
		}
	}

	rec = do(t, h, "GET", "/api/analyses/"+j.ID+"/graph", "")
	g := decode[struct {
		Nodes []graph.Node `json:"nodes"`
		Edges []graph.Edge `json:"edges"`
	}](t, rec)
	if len(g.Nodes) != 3 || len(g.Edges) != 1 {
		t.Errorf("graph export = %+v", g)
	}

	list := decode[[]Job](t, do(t, h, "GET", "/api/analyses", ""))
	if len(list) != 1 {
		t.Errorf("list = %+v", list)
	}
}

func TestAnalyzeFailureIsReported(t *testing.T) {
	h := newTestServer(t, func(c *Config) { c.Resolver = fakeResolver{err: errors.New("repository not found")} })
	j := decode[Job](t, do(t, h, "POST", "/api/analyses", `{"source":"x/y"}`))
	failed := waitFor(t, h, j.ID, StatusFailed)
	if !strings.Contains(failed.Error, "repository not found") {
		t.Errorf("error = %q", failed.Error)
	}
}

func TestBadRequests(t *testing.T) {
	h := newTestServer(t)
	tests := []struct {
		name, method, url, body string
		ctype                   string
		want                    int
	}{
		{"invalid json", "POST", "/api/analyses", `{`, "application/json", http.StatusBadRequest},
		{"empty source", "POST", "/api/analyses", `{"source":"  "}`, "application/json", http.StatusBadRequest},
		{"form post (CSRF)", "POST", "/api/analyses", `source=x`, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{"unknown job", "GET", "/api/analyses/nope", "", "", http.StatusNotFound},
		{"unknown job view", "GET", "/api/analyses/nope/view", "", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.url, strings.NewReader(tt.body))
			req.Host = "127.0.0.1:7777"
			if tt.ctype != "" {
				req.Header.Set("Content-Type", tt.ctype)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.want {
				t.Errorf("status = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}
		})
	}
}

func TestUnknownFocusIs404(t *testing.T) {
	h := newTestServer(t)
	j := decode[Job](t, do(t, h, "POST", "/api/analyses", `{"source":"demo"}`))
	waitFor(t, h, j.ID, StatusReady)
	if rec := do(t, h, "GET", "/api/analyses/"+j.ID+"/view?focus=ghost", ""); rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestViewBeforeReadyIsConflict(t *testing.T) {
	block := make(chan struct{})
	h := newTestServer(t, func(c *Config) { c.Resolver = blockingResolver{block} })
	j := decode[Job](t, do(t, h, "POST", "/api/analyses", `{"source":"slow"}`))
	if rec := do(t, h, "GET", "/api/analyses/"+j.ID+"/view", ""); rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while analyzing", rec.Code)
	}
	close(block)
	waitFor(t, h, j.ID, StatusReady)
}

type blockingResolver struct{ ch chan struct{} }

func (b blockingResolver) Resolve(_ context.Context, in string) (source.Repo, error) {
	<-b.ch
	return source.Repo{Dir: "/tmp/" + in, Name: in}, nil
}

func TestRejectsForeignHosts(t *testing.T) {
	h := newTestServer(t)
	req := httptest.NewRequest("GET", "/api/analyses", nil)
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for DNS-rebinding host", rec.Code)
	}
}

func TestServesUI(t *testing.T) {
	h := newTestServer(t)
	rec := do(t, h, "GET", "/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("GET / = %d %q", rec.Code, rec.Body.String()[:min(80, rec.Body.Len())])
	}
	if rec := do(t, h, "GET", "/static/app.js", ""); rec.Code != http.StatusOK {
		t.Errorf("GET /static/app.js = %d", rec.Code)
	}
}

func TestConfigEndpoint(t *testing.T) {
	h := newTestServer(t, func(c *Config) { c.Enricher = fakeEnricher{}; c.Initial = "demo" })
	cfg := decode[map[string]string](t, do(t, h, "GET", "/api/config", ""))
	if cfg["llm"] != "fake/model" || cfg["initial"] == "" {
		t.Errorf("config = %v, want llm and initial job id", cfg)
	}
}
