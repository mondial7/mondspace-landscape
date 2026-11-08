// Package pipeline wires scanning, analysis, assembly and optional
// enrichment into a single "repository in, landscape out" operation.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/analyze/golang"
	"github.com/mondial7/mondspace-landscape/internal/analyze/infra"
	"github.com/mondial7/mondspace-landscape/internal/analyze/literal"
	"github.com/mondial7/mondspace-landscape/internal/analyze/script"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Source gives read access to repository files. It is an alias of an
// unnamed interface so that equivalent declarations in other packages are
// the same type (and implementations satisfy all of them).
type Source = interface {
	Read(path string) ([]byte, error)
}

// Enricher adds information to an assembled model (e.g. LLM descriptions).
type Enricher interface {
	Enrich(ctx context.Context, m *landscape.Model, src Source) error
}

// Pipeline analyzes repositories. Construct with New.
type Pipeline struct {
	analyzers []analyze.Analyzer
	enricher  Enricher
}

// Option configures a Pipeline.
type Option func(*Pipeline)

// WithEnricher sets an enrichment step run after assembly.
func WithEnricher(e Enricher) Option { return func(p *Pipeline) { p.enricher = e } }

// WithAnalyzers replaces the default analyzers.
func WithAnalyzers(as ...analyze.Analyzer) Option { return func(p *Pipeline) { p.analyzers = as } }

// DefaultAnalyzers returns every built-in analyzer.
func DefaultAnalyzers() []analyze.Analyzer {
	return []analyze.Analyzer{
		golang.New(), script.NewJS(), script.NewPython(), script.NewSwift(), script.NewHTML(),
		infra.New(), literal.New(),
	}
}

// New returns a pipeline with the default analyzers.
func New(opts ...Option) *Pipeline {
	p := &Pipeline{analyzers: DefaultAnalyzers()}
	for _, o := range opts {
		o(p)
	}
	return p
}

// Run analyzes the repository rooted at fsys.
func (p *Pipeline) Run(ctx context.Context, fsys fs.FS, name string) (*landscape.Model, error) {
	repo, err := scan.Scan(fsys, name)
	if err != nil {
		return nil, err
	}
	facts, err := analyze.Run(ctx, repo, p.analyzers...)
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return nil, err
	}
	// Other analyzer errors are already recorded as warnings in facts.
	m, err := landscape.Build(landscape.Input{Name: name, Files: repo.Files, Facts: facts, Readme: readme(fsys)})
	if err != nil {
		return nil, fmt.Errorf("assemble %s: %w", name, err)
	}
	if p.enricher != nil {
		if err := p.enricher.Enrich(ctx, m, repo); err != nil {
			m.Summary.Warnings = append(m.Summary.Warnings, "enrichment: "+err.Error())
		}
	}
	return m, nil
}

func readme(fsys fs.FS) string {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		n := strings.ToLower(e.Name())
		if !e.IsDir() && (n == "readme.md" || n == "readme" || n == "readme.txt") {
			b, err := fs.ReadFile(fsys, e.Name())
			if err == nil {
				return string(b)
			}
		}
	}
	return ""
}
