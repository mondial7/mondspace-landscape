// Package enrich uses a (local) language model to describe what each part
// of the landscape does, bottom-up: components from their code, containers
// from their components, the system from its containers.
package enrich

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
)

// Completer is the subset of an LLM client the enricher needs.
type Completer interface {
	Complete(ctx context.Context, prompt string) (string, error)
	Name() string
	Model() string
}

// Source gives read access to repository files. It is an alias of an
// unnamed interface so that equivalent declarations in other packages are
// the same type (and implementations satisfy all of them).
type Source = interface {
	Read(path string) ([]byte, error)
}

// Progress reports how far a stage is.
type Progress struct {
	Stage string `json:"stage"` // "components", "containers", "system"
	Done  int    `json:"done"`
	Total int    `json:"total"`
}

// Results maps node IDs to generated descriptions.
type Results map[string]string

// Enricher generates descriptions. Construct with New.
type Enricher struct {
	llm           Completer
	workers       int
	maxComponents int
	progress      func(Progress)
}

// Option configures an Enricher.
type Option func(*Enricher)

// WithWorkers sets how many prompts run concurrently.
func WithWorkers(n int) Option { return func(e *Enricher) { e.workers = max(1, n) } }

// WithMaxComponents caps the components described (largest first).
func WithMaxComponents(n int) Option { return func(e *Enricher) { e.maxComponents = n } }

// WithProgress registers a progress callback.
func WithProgress(fn func(Progress)) Option { return func(e *Enricher) { e.progress = fn } }

// New returns an enricher using the given model.
func New(c Completer, opts ...Option) *Enricher {
	e := &Enricher{llm: c, workers: 2, maxComponents: 60, progress: func(Progress) {}}
	for _, o := range opts {
		o(e)
	}
	return e
}

// Excerpt limits keep prompts small enough for laptop-sized models.
const (
	excerptFiles = 3
	excerptLines = 40
	excerptChars = 3000
	maxDescChars = 300
)

// Label identifies the model producing descriptions ("ollama/qwen3:8b").
func (e *Enricher) Label() string { return e.llm.Name() + "/" + e.llm.Model() }

// Enrich describes the model in place (implements pipeline.Enricher).
func (e *Enricher) Enrich(ctx context.Context, m *landscape.Model, src Source) error {
	return e.Run(ctx, m, src, Hooks{})
}

// Hooks let callers observe and control how results are applied.
type Hooks struct {
	// Apply writes a stage's results; defaults to Apply on the model graph.
	// It is the only writer, so callers may wrap it in a lock.
	Apply func(Results)
	// Progress overrides the enricher's progress callback.
	Progress func(Progress)
}

// Run generates descriptions stage by stage, applying each stage's results
// before the next stage (which builds on them) reads the graph.
func (e *Enricher) Run(ctx context.Context, m *landscape.Model, src Source, h Hooks) error {
	g := m.Graph
	if h.Apply == nil {
		h.Apply = func(r Results) { Apply(g, r, e.Label()) }
	}
	if h.Progress == nil {
		h.Progress = e.progress
	}
	var errs []error
	stages := []struct {
		name   string
		nodes  func() []graph.Node
		prompt func(graph.Node) string
	}{
		{"components", func() []graph.Node { return e.components(g) }, func(n graph.Node) string { return componentPrompt(g, n, src) }},
		{"containers", func() []graph.Node { return g.Children(landscape.SystemID) }, func(n graph.Node) string { return containerPrompt(g, n) }},
		{"system", func() []graph.Node { sys, _ := g.Node(landscape.SystemID); return []graph.Node{sys} }, func(n graph.Node) string { return systemPrompt(g, n, src) }},
	}
	for _, st := range stages {
		if ctx.Err() != nil {
			break
		}
		res, err := e.stage(ctx, st.name, st.nodes(), st.prompt, h.Progress)
		errs = append(errs, err)
		h.Apply(res)
	}
	return errors.Join(errs...)
}

// components returns the non-test components to describe, largest first.
func (e *Enricher) components(g *graph.Graph) []graph.Node {
	var out []graph.Node
	for _, n := range g.Nodes() {
		if n.Kind == graph.KindComponent && !hasTag(n, "test") {
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Metrics["lines"] > out[j].Metrics["lines"] })
	if e.maxComponents >= 0 && len(out) > e.maxComponents {
		out = out[:e.maxComponents]
	}
	return out
}

// stage runs one prompt per node with bounded concurrency.
func (e *Enricher) stage(ctx context.Context, name string, nodes []graph.Node, prompt func(graph.Node) string, progress func(Progress)) (Results, error) {
	res := Results{}
	total := len(nodes)
	progress(Progress{Stage: name, Total: total})
	if total == 0 {
		return res, nil
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		errs []error
		done int
	)
	jobs := make(chan graph.Node)
	for w := 0; w < e.workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				out, err := e.llm.Complete(ctx, prompt(n))
				mu.Lock()
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", n.ID, err))
				} else if d := tidy(out); d != "" {
					res[n.ID] = d
				}
				done++
				progress(Progress{Stage: name, Done: done, Total: total})
				mu.Unlock()
			}
		}()
	}
	for _, n := range nodes {
		if ctx.Err() != nil {
			break
		}
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		errs = append(errs, err)
	}
	return res, errors.Join(errs...)
}

// Apply writes descriptions into the graph, keeping any static description
// (e.g. a package doc comment) under Details["doc"].
func Apply(g *graph.Graph, r Results, source string) {
	for id, d := range r {
		_ = g.Update(id, func(n *graph.Node) {
			if n.Details == nil {
				n.Details = map[string]string{}
			}
			if n.Description != "" && n.Description != d && n.Details["doc"] == "" {
				n.Details["doc"] = n.Description
			}
			n.Description = d
			n.Details["ai"] = source
		})
	}
}

// tidy keeps the first paragraph of a model answer, unquoted and bounded.
func tidy(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "\n\n"); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, "\"'`")
	if len(s) > maxDescChars {
		s = s[:maxDescChars-1] + "…"
	}
	return s
}

func hasTag(n graph.Node, tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
