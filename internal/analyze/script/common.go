// Package script holds lightweight, pattern-based analyzers for languages
// where a full parser would be a heavy dependency: JavaScript/TypeScript,
// Python, Swift and HTML templates. Units are source directories.
package script

import (
	"path"
	"sort"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
)

// dep records that file depends on unit to, separating test-only code.
func (b *builder) dep(to, file string, test bool) {
	m := b.deps
	if test {
		m = b.tests
	}
	if m[to] == nil {
		m[to] = map[string]bool{}
	}
	m[to][file] = true
}

// units accumulates directory-based units for one language.
type units struct {
	prefix string
	lang   string
	byDir  map[string]*builder
}

type builder struct {
	unit   analyze.Unit
	tags   map[string]bool
	routes map[string]bool
	uses   map[string]analyze.External
	deps   map[string]map[string]bool // target unit → importing files
	tests  map[string]map[string]bool // same, for test files
}

func newUnits(prefix, lang string) *units {
	return &units{prefix: prefix, lang: lang, byDir: map[string]*builder{}}
}

func (u *units) id(dir string) string { return u.prefix + ":" + dir }

// get returns the builder for the directory of file, creating it if needed.
func (u *units) get(file string) *builder {
	dir := path.Dir(file)
	b, ok := u.byDir[dir]
	if !ok {
		name := path.Base(dir)
		if dir == "." {
			name = "root"
		}
		b = &builder{
			unit: analyze.Unit{ID: u.id(dir), Name: name, Dir: dir, Language: u.lang},
			tags: map[string]bool{}, routes: map[string]bool{},
			uses: map[string]analyze.External{}, deps: map[string]map[string]bool{}, tests: map[string]map[string]bool{},
		}
		u.byDir[dir] = b
	}
	b.unit.Files = append(b.unit.Files, file)
	return b
}

func (u *units) unitByID(id string) *builder {
	return u.byDir[strings.TrimPrefix(id, u.prefix+":")]
}

func (u *units) has(dir string) bool {
	_, ok := u.byDir[dir]
	return ok
}

func (b *builder) use(lang, imp string) {
	if e, ok := analyze.Lookup(lang, imp); ok {
		b.uses[e.ID] = e
	}
}

// facts flattens the builders into analyzer output, sorted by directory.
func (u *units) facts(out *analyze.Facts) {
	dirs := make([]string, 0, len(u.byDir))
	for d := range u.byDir {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		b := u.byDir[d]
		b.unit.Tags = keys(b.tags)
		b.unit.Routes = keys(b.routes)
		out.Units = append(out.Units, b.unit)
		emit := func(deps map[string]map[string]bool, label string) {
			targets := make([]string, 0, len(deps))
			for to := range deps {
				targets = append(targets, to)
			}
			sort.Strings(targets)
			for _, to := range targets {
				if to == b.unit.ID {
					continue
				}
				for _, file := range keys(deps[to]) {
					out.Deps = append(out.Deps, analyze.Dep{From: b.unit.ID, To: to, File: file, Kind: graph.EdgeDepends, Label: label})
				}
			}
		}
		emit(b.deps, "")
		emit(b.tests, analyze.TestsLabel)
		ids := make([]string, 0, len(b.uses))
		for id := range b.uses {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			e := b.uses[id]
			out.AddExternal(e)
			out.Uses = append(out.Uses, analyze.Use{Unit: b.unit.ID, External: id, Kind: analyze.EdgeKindFor(e.Kind)})
		}
	}
}

func keys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// topDir returns the first path segment of p ("." for root files).
func topDir(p string) string {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}
