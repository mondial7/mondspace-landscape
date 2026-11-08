// Package golang extracts architecture facts from Go source using the
// standard library parser (no build or type-checking required, so it works
// on any checkout, even one that does not compile).
package golang

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Analyzer implements analyze.Analyzer for Go.
type Analyzer struct{}

// New returns a Go analyzer.
func New() Analyzer { return Analyzer{} }

// Name implements analyze.Analyzer.
func (Analyzer) Name() string { return "go" }

// maxSymbols caps the exported identifiers reported per package.
const maxSymbols = 15

// routerMethods are router registration calls whose first argument is a route.
var routerMethods = map[string]string{
	"HandleFunc": "", "Handle": "",
	"Get": "GET", "Post": "POST", "Put": "PUT", "Patch": "PATCH", "Delete": "DELETE",
	"GET": "GET", "POST": "POST", "PUT": "PUT", "PATCH": "PATCH", "DELETE": "DELETE",
}

// serverCalls mark a package as running an HTTP server.
var serverCalls = map[string]bool{"ListenAndServe": true, "ListenAndServeTLS": true, "Serve": true, "ServeTLS": true}

// fsWriteCalls on package os mark a package as persisting to local files.
var fsWriteCalls = map[string]bool{"WriteFile": true, "Create": true, "OpenFile": true, "Rename": true, "CreateTemp": true}

// templateCalls mark a package as rendering server-side templates.
var templateCalls = map[string]bool{"ParseFS": true, "ParseGlob": true, "ParseFiles": true}

type pkg struct {
	unit    analyze.Unit
	imports map[string][]string // import path → importing files
	uses    map[string]analyze.External
	symbols map[string]bool
	routes  map[string]bool
	tags    map[string]bool
	hasMain bool
}

// Analyze implements analyze.Analyzer.
func (Analyzer) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	files := repo.ByLanguage("go")
	if len(files) == 0 {
		return facts, nil
	}
	module := modulePath(repo)
	pkgs := map[string]*pkg{}
	fset := token.NewFileSet()

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		dir := path.Dir(f.Path)
		p := pkgs[dir]
		if p == nil {
			p = &pkg{
				unit:    analyze.Unit{ID: "go:" + dir, Dir: dir, Language: "go"},
				imports: map[string][]string{}, uses: map[string]analyze.External{},
				symbols: map[string]bool{}, routes: map[string]bool{}, tags: map[string]bool{},
			}
			pkgs[dir] = p
		}
		p.unit.Files = append(p.unit.Files, f.Path)

		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		file, err := parser.ParseFile(fset, f.Path, src, parser.ParseComments)
		if err != nil {
			facts.Warnings = append(facts.Warnings, fmt.Sprintf("go: skipping %s: %v", f.Path, firstLine(err.Error())))
			continue
		}
		if f.Test {
			continue // test code does not shape the architecture
		}
		inspectFile(p, file, f.Path, src, dir, &facts)
	}

	driverIDs := map[string]bool{}
	for _, p := range pkgs {
		for id, e := range p.uses {
			if e.Kind == analyze.ExtDatabase && id != "sql" {
				driverIDs[id] = true
			}
		}
	}

	dirs := make([]string, 0, len(pkgs))
	for d := range pkgs {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		p := pkgs[dir]
		u := p.unit
		u.Name = displayName(u.Name, dir, module)
		if p.hasMain {
			u.Entry = true
			if !p.tags["http-server"] {
				p.tags["cli"] = true
			}
		}
		u.Routes = sortedKeys(p.routes)
		u.Symbols = sortedKeys(p.symbols)
		if len(u.Symbols) > maxSymbols {
			u.Symbols = u.Symbols[:maxSymbols]
		}
		u.Tags = sortedKeys(p.tags)
		facts.Units = append(facts.Units, u)

		imports := make([]string, 0, len(p.imports))
		for imp := range p.imports {
			imports = append(imports, imp)
		}
		sort.Strings(imports)
		for _, imp := range imports {
			if module == "" || (imp != module && !strings.HasPrefix(imp, module+"/")) {
				continue
			}
			target := strings.TrimPrefix(strings.TrimPrefix(imp, module), "/")
			if target == "" {
				target = "."
			}
			if _, ok := pkgs[target]; ok && target != dir {
				for _, file := range p.imports[imp] {
					facts.Deps = append(facts.Deps, analyze.Dep{From: u.ID, To: "go:" + target, File: file, Kind: graph.EdgeDepends})
				}
			}
		}

		seen := map[string]bool{}
		for _, id := range sortedKeysExt(p.uses) {
			e := p.uses[id]
			if id == "sql" && len(driverIDs) == 1 {
				for d := range driverIDs {
					e = findExternal(pkgs, d)
				}
			}
			if seen[e.ID] {
				continue
			}
			seen[e.ID] = true
			facts.AddExternal(e)
			facts.Uses = append(facts.Uses, analyze.Use{Unit: u.ID, External: e.ID, Kind: analyze.EdgeKindFor(e.Kind)})
		}
	}
	return facts, nil
}

func inspectFile(p *pkg, file *ast.File, filePath string, src []byte, dir string, facts *analyze.Facts) {
	if p.unit.Name == "" {
		p.unit.Name = file.Name.Name
	}
	if file.Doc != nil && p.unit.Doc == "" {
		p.unit.Doc = strings.TrimSpace(firstSentence(file.Doc.Text()))
	}
	for _, imp := range file.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		p.imports[ip] = append(p.imports[ip], filePath)
		if e, ok := analyze.Lookup("go", ip); ok {
			p.uses[e.ID] = e
		}
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == "main" && file.Name.Name == "main" {
				p.hasMain = true
			}
			if d.Recv == nil && d.Name.IsExported() {
				p.symbols[d.Name.Name] = true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
					p.symbols[ts.Name.Name] = true
				}
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		switch {
		case serverCalls[name]:
			p.tags["http-server"] = true
		case templateCalls[name]:
			p.tags["templates"] = true
		case fsWriteCalls[name] && isIdent(sel.X, "os"):
			p.uses[analyze.Filesystem.ID] = analyze.Filesystem
		}
		if method, ok := routerMethods[name]; ok && len(call.Args) >= 2 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if route, err := strconv.Unquote(lit.Value); err == nil && isRoute(route) {
					if method != "" && !strings.Contains(route, " ") {
						route = method + " " + route
					}
					p.routes[route] = true
					p.tags["http-server"] = true
				}
			}
		}
		return true
	})
	for _, pattern := range embedPatterns(src) {
		facts.Refs = append(facts.Refs, analyze.Ref{
			From: p.unit.ID, Path: path.Join(dir, pattern), Kind: graph.EdgeDepends, Label: "embeds",
		})
	}
}

// displayName names a package: its package clause, except for main
// packages (and unparsable ones), which are named after their directory or,
// at the repository root, after the module.
func displayName(pkgName, dir, module string) string {
	if pkgName != "" && pkgName != "main" {
		return pkgName
	}
	if dir == "." {
		if module != "" {
			return moduleName(module)
		}
		return "main"
	}
	return path.Base(dir)
}

func isIdent(e ast.Expr, name string) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == name
}

var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// moduleName is the last meaningful element of a module path, skipping a
// major-version suffix ("github.com/o/glow/v3" → "glow").
func moduleName(module string) string {
	base := path.Base(module)
	if majorVersion.MatchString(base) && path.Dir(module) != "." {
		return path.Base(path.Dir(module))
	}
	return base
}

// isRoute accepts "/x" and "METHOD /x" patterns.
func isRoute(s string) bool {
	if strings.HasPrefix(s, "/") {
		return true
	}
	if i := strings.IndexByte(s, ' '); i > 0 {
		return strings.HasPrefix(strings.TrimSpace(s[i+1:]), "/")
	}
	return false
}

// embedPatterns returns the patterns of //go:embed directives.
func embedPatterns(src []byte) []string {
	var out []string
	sc := bufio.NewScanner(bytes.NewReader(src))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "//go:embed ") {
			continue
		}
		for _, f := range strings.Fields(strings.TrimPrefix(line, "//go:embed ")) {
			if uq, err := strconv.Unquote(f); err == nil {
				f = uq
			}
			out = append(out, strings.TrimPrefix(f, "all:"))
		}
	}
	return out
}

func modulePath(repo *scan.Repo) string {
	data, err := repo.Read("go.mod")
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) >= 2 && f[0] == "module" {
			return strings.Trim(f[1], `"`)
		}
	}
	return ""
}

func findExternal(pkgs map[string]*pkg, id string) analyze.External {
	for _, p := range pkgs {
		if e, ok := p.uses[id]; ok {
			return e
		}
	}
	return analyze.External{ID: id, Name: id}
}

func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if i := strings.Index(s, ". "); i >= 0 {
		return s[:i+1]
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeysExt(m map[string]analyze.External) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
