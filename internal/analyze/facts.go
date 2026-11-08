// Package analyze defines the language-neutral facts that analyzers extract
// from a repository and the plumbing to run analyzers and merge their output.
//
// Analyzers know about languages; the landscape assembler knows about
// architecture. Facts are the contract between the two.
package analyze

import (
	"context"
	"errors"
	"fmt"

	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Unit is a cohesive group of source files: a Go package, a source
// directory, a module. Units become components in the landscape.
type Unit struct {
	ID       string   `json:"id"`   // stable, language-prefixed ("go:internal/store")
	Name     string   `json:"name"` // short display name
	Dir      string   `json:"dir"`  // repo-relative directory ("." for root)
	Language string   `json:"language"`
	Files    []string `json:"files"`
	Entry    bool     `json:"entry"`             // executable entry point (main)
	Root     string   `json:"root,omitempty"`    // project root this unit belongs to (package.json dir, module…)
	Routes   []string `json:"routes,omitempty"`  // HTTP routes served ("GET /api/x")
	Symbols  []string `json:"symbols,omitempty"` // notable exported identifiers
	Tags     []string `json:"tags,omitempty"`    // "http-server", "cli", "ui", "templates", "assets"…
	Doc      string   `json:"doc,omitempty"`     // package doc comment, if any
}

// TestsLabel marks dependencies that only exist in test code.
const TestsLabel = "tests"

// Dep is a dependency between two units.
type Dep struct {
	From  string         `json:"from"`
	To    string         `json:"to"`
	File  string         `json:"file,omitempty"` // importing file, when known
	Kind  graph.EdgeKind `json:"kind"`
	Label string         `json:"label,omitempty"`
}

// ExternalKind classifies systems outside the repository.
type ExternalKind string

const (
	ExtDatabase ExternalKind = "database"
	ExtHTTP     ExternalKind = "http-api"
	ExtCloud    ExternalKind = "cloud"
	ExtSaaS     ExternalKind = "saas"
	ExtQueue    ExternalKind = "queue"
	ExtAI       ExternalKind = "ai"
	ExtPlatform ExternalKind = "platform" // OS / device services, process exec
	ExtHosting  ExternalKind = "hosting"
	ExtMail     ExternalKind = "mail"
	ExtStorage  ExternalKind = "storage" // local filesystem / object storage
)

// External is a system the code talks to but does not own.
type External struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Kind       ExternalKind `json:"kind"`
	Technology string       `json:"technology,omitempty"`
}

// Use links code to an external system. Either Unit or File must be set;
// File is resolved to its owning unit by the assembler.
type Use struct {
	Unit     string         `json:"unit,omitempty"`
	File     string         `json:"file,omitempty"`
	External string         `json:"external"`
	Kind     graph.EdgeKind `json:"kind"`
	Label    string         `json:"label,omitempty"`
}

// Call is an outgoing HTTP request to a relative path, to be matched
// against routes served elsewhere in the repository.
type Call struct {
	File   string `json:"file"`
	Method string `json:"method,omitempty"`
	Path   string `json:"path"`
}

// DataAccess records SQL tables touched by a file.
type DataAccess struct {
	File   string   `json:"file"`
	Reads  []string `json:"reads,omitempty"`
	Writes []string `json:"writes,omitempty"`
}

// Ref is a reference from a unit to files by path (e.g. //go:embed
// templates/*.html). The assembler resolves it to the units owning them.
type Ref struct {
	From  string         `json:"from"`
	Path  string         `json:"path"` // repo-relative path or glob
	Kind  graph.EdgeKind `json:"kind"`
	Label string         `json:"label,omitempty"`
}

// Service is a runtime/deployment unit declared by infrastructure files.
type Service struct {
	Name      string   `json:"name"`
	Source    string   `json:"source"`          // file that declares it
	Platform  string   `json:"platform"`        // "docker", "docker-compose", "fly.io", …
	Image     string   `json:"image,omitempty"` // base or service image
	BuildDir  string   `json:"buildDir,omitempty"`
	Ports     []string `json:"ports,omitempty"`
	DependsOn []string `json:"dependsOn,omitempty"`
	Details   []string `json:"details,omitempty"`
}

// Facts is everything analyzers learned about a repository.
type Facts struct {
	Units     []Unit       `json:"units"`
	Deps      []Dep        `json:"deps"`
	Externals []External   `json:"externals"`
	Uses      []Use        `json:"uses"`
	Calls     []Call       `json:"calls"`
	Data      []DataAccess `json:"data"`
	Refs      []Ref        `json:"refs"`
	Services  []Service    `json:"services"`
	Warnings  []string     `json:"warnings,omitempty"`
}

// Merge appends o into f, de-duplicating externals by ID.
func (f *Facts) Merge(o Facts) {
	f.Units = append(f.Units, o.Units...)
	f.Deps = append(f.Deps, o.Deps...)
	f.Uses = append(f.Uses, o.Uses...)
	f.Calls = append(f.Calls, o.Calls...)
	f.Data = append(f.Data, o.Data...)
	f.Refs = append(f.Refs, o.Refs...)
	f.Services = append(f.Services, o.Services...)
	f.Warnings = append(f.Warnings, o.Warnings...)
	for _, e := range o.Externals {
		f.AddExternal(e)
	}
}

// AddExternal registers an external system once.
func (f *Facts) AddExternal(e External) {
	for _, x := range f.Externals {
		if x.ID == e.ID {
			return
		}
	}
	f.Externals = append(f.Externals, e)
}

// Analyzer extracts facts from a scanned repository.
type Analyzer interface {
	Name() string
	Analyze(ctx context.Context, repo *scan.Repo) (Facts, error)
}

// Run executes the analyzers in order and merges their facts. A failing
// analyzer does not stop the others: its error is recorded as a warning and
// returned joined with any other failures.
func Run(ctx context.Context, repo *scan.Repo, analyzers ...Analyzer) (Facts, error) {
	var all Facts
	var errs []error
	for _, a := range analyzers {
		if err := ctx.Err(); err != nil {
			return all, err
		}
		f, err := a.Analyze(ctx, repo)
		if err != nil {
			err = fmt.Errorf("analyzer %s: %w", a.Name(), err)
			errs = append(errs, err)
			all.Warnings = append(all.Warnings, err.Error())
			continue
		}
		all.Merge(f)
	}
	return all, errors.Join(errs...)
}
