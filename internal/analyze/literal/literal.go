// Package literal is a language-agnostic analyzer that reads string literals
// in source files: absolute URLs reveal external HTTP dependencies and SQL
// statements reveal which tables a file reads and writes.
package literal

import (
	"context"
	"fmt"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// codeLanguages are the languages whose literals are runtime behaviour
// (as opposed to docs or config, where URLs are mostly references).
var codeLanguages = map[string]bool{
	"go": true, "javascript": true, "typescript": true, "python": true, "swift": true,
	"kotlin": true, "java": true, "ruby": true, "rust": true, "csharp": true, "php": true,
	"c": true, "cpp": true, "html": true, "sql": true, "shell": true,
}

// Analyzer implements analyze.Analyzer.
type Analyzer struct{}

// New returns a literal analyzer.
func New() Analyzer { return Analyzer{} }

// Name implements analyze.Analyzer.
func (Analyzer) Name() string { return "literals" }

// Analyze implements analyze.Analyzer.
func (Analyzer) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	for _, f := range repo.Files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		if f.Test || !codeLanguages[f.Language] {
			continue
		}
		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		for _, host := range analyze.ExtractHosts(src) {
			e := analyze.External{ID: "host:" + host, Name: host, Kind: analyze.ExtHTTP}
			facts.AddExternal(e)
			facts.Uses = append(facts.Uses, analyze.Use{File: f.Path, External: e.ID, Kind: analyze.EdgeKindFor(e.Kind)})
		}
		if reads, writes := analyze.ExtractSQLTables(src); len(reads)+len(writes) > 0 {
			facts.Data = append(facts.Data, analyze.DataAccess{File: f.Path, Reads: reads, Writes: writes})
		}
	}
	return facts, nil
}
