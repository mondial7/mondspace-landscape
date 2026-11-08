package enrich

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

type fakeLLM struct {
	mu      sync.Mutex
	prompts []string
	fail    string // fail prompts containing this
}

func (f *fakeLLM) Name() string  { return "fake" }
func (f *fakeLLM) Model() string { return "fake-1" }
func (f *fakeLLM) Complete(_ context.Context, prompt string) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, prompt)
	f.mu.Unlock()
	if f.fail != "" && strings.Contains(prompt, f.fail) {
		return "", errors.New("model crashed")
	}
	switch {
	case strings.Contains(prompt, "COMPONENT"):
		return `"Handles ` + between(prompt, "Name: ", "\n") + `."` + "\n\nextra paragraph", nil
	case strings.Contains(prompt, "CONTAINER"):
		return "Runs " + between(prompt, "Name: ", "\n") + ".", nil
	default:
		return "The whole system.", nil
	}
}

func between(s, start, end string) string {
	i := strings.Index(s, start)
	if i < 0 {
		return ""
	}
	s = s[i+len(start):]
	if j := strings.Index(s, end); j >= 0 {
		return s[:j]
	}
	return s
}

type mapSource map[string]string

func (m mapSource) Read(p string) ([]byte, error) {
	if s, ok := m[p]; ok {
		return []byte(s), nil
	}
	return nil, errors.New("not found")
}

func model(t *testing.T) (*landscape.Model, mapSource) {
	t.Helper()
	files := []scan.File{
		{Path: "main.go", Language: "go", Lines: 10},
		{Path: "store/store.go", Language: "go", Lines: 30},
		{Path: "store/store_test.go", Language: "go", Lines: 30, Test: true},
		{Path: "e2e/flow_test.go", Language: "go", Lines: 5, Test: true},
	}
	m, err := landscape.Build(landscape.Input{
		Name: "demo", Files: files, Readme: "# Demo\n\nA demo.",
		Facts: analyze.Facts{
			Units: []analyze.Unit{
				{ID: "go:.", Name: "demo", Dir: ".", Language: "go", Files: []string{"main.go"}, Entry: true, Tags: []string{"cli"}},
				{ID: "go:store", Name: "store", Dir: "store", Language: "go", Files: []string{"store/store.go", "store/store_test.go"}, Doc: "Package store persists things."},
				{ID: "go:e2e", Name: "e2e", Dir: "e2e", Language: "go", Files: []string{"e2e/flow_test.go"}},
			},
			Deps: []analyze.Dep{{From: "go:.", To: "go:store", Kind: graph.EdgeDepends}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m, mapSource{"main.go": "package main\nfunc main() {}\n", "store/store.go": "package store\n// Save writes.\nfunc Save() {}\n"}
}

func desc(t *testing.T, m *landscape.Model, id string) graph.Node {
	t.Helper()
	n, ok := m.Graph.Node(id)
	if !ok {
		t.Fatalf("node %s missing", id)
	}
	return n
}

func TestEnrichDescribesBottomUp(t *testing.T) {
	m, src := model(t)
	llm := &fakeLLM{}
	var stages []string
	e := New(llm, WithProgress(func(p Progress) {
		if p.Done == p.Total {
			stages = append(stages, p.Stage)
		}
	}))
	if err := e.Enrich(context.Background(), m, src); err != nil {
		t.Fatal(err)
	}
	if got := desc(t, m, "cmp:store"); got.Description != "Handles store." {
		t.Errorf("store description = %q (want cleaned first paragraph)", got.Description)
	} else if got.Details["doc"] != "Package store persists things." || got.Details["ai"] != "fake/fake-1" {
		t.Errorf("store details = %v, want original doc kept and model recorded", got.Details)
	}
	if got := desc(t, m, "ctr:.").Description; got != "Runs demo." {
		t.Errorf("container description = %q", got)
	}
	if got := desc(t, m, landscape.SystemID).Description; got != "The whole system." {
		t.Errorf("system description = %q", got)
	}
	if got := desc(t, m, "cmp:e2e").Description; got != "" {
		t.Errorf("test-only component should not be described, got %q", got)
	}
	var containerPrompt, storePrompt string
	for _, p := range llm.prompts {
		if strings.Contains(p, "CONTAINER") {
			containerPrompt = p
		}
		if strings.Contains(p, "Name: store") {
			storePrompt = p
		}
	}
	if !strings.Contains(containerPrompt, "Handles store.") {
		t.Errorf("container prompt lacks component descriptions:\n%s", containerPrompt)
	}
	for _, want := range []string{"store/store.go", "func Save()", "Used by: demo", "Package store persists things."} {
		if !strings.Contains(storePrompt, want) {
			t.Errorf("component prompt missing %q:\n%s", want, storePrompt)
		}
	}
	if strings.Contains(storePrompt, "store_test.go\n---") {
		t.Error("test files should not be excerpted")
	}
	if strings.Join(stages, ",") != "components,containers,system" {
		t.Errorf("stages = %v", stages)
	}
}

func TestEnrichRespectsLimit(t *testing.T) {
	m, src := model(t)
	llm := &fakeLLM{}
	if err := New(llm, WithMaxComponents(1)).Enrich(context.Background(), m, src); err != nil {
		t.Fatal(err)
	}
	// store (30 lines) is larger than demo (10 lines), so it wins the single slot.
	if desc(t, m, "cmp:store").Description == "" || desc(t, m, "cmp:.").Description != "" {
		t.Errorf("limit not applied by size: store=%q root=%q",
			desc(t, m, "cmp:store").Description, desc(t, m, "cmp:.").Description)
	}
}

func TestEnrichKeepsGoingWhenSomeCallsFail(t *testing.T) {
	m, src := model(t)
	err := New(&fakeLLM{fail: "Name: store"}).Enrich(context.Background(), m, src)
	if err == nil {
		t.Error("want error reporting the failed description")
	}
	if desc(t, m, "cmp:.").Description == "" || desc(t, m, landscape.SystemID).Description == "" {
		t.Error("other descriptions should still be applied")
	}
	if got := desc(t, m, "cmp:store").Description; got != "Package store persists things." {
		t.Errorf("failed node should keep its static description, got %q", got)
	}
}
