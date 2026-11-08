package script

import (
	"context"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Python analyzes Python sources.
type Python struct{}

// NewPython returns a Python analyzer.
func NewPython() Python { return Python{} }

// Name implements analyze.Analyzer.
func (Python) Name() string { return "python" }

var (
	pyImport     = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+(?:\s*,\s*[\w.]+)*)`)
	pyFromImport = regexp.MustCompile(`(?m)^\s*from\s+(\.*[\w.]*)\s+import\b`)
	pyRoute      = regexp.MustCompile(`(?m)^\s*@\w+\.(get|post|put|patch|delete|route)\(\s*['"](/[^'"]*)['"](?:[^)]*methods\s*=\s*\[\s*['"](\w+)['"])?`)
	pyMain       = regexp.MustCompile(`(?m)^if\s+__name__\s*==\s*['"]__main__['"]`)
)

var pyWebFrameworks = map[string]bool{"fastapi": true, "flask": true, "django": true, "starlette": true, "aiohttp": true, "sanic": true}

// Analyze implements analyze.Analyzer.
func (Python) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	files := repo.ByLanguage("python")
	if len(files) == 0 {
		return facts, nil
	}
	us := newUnits("py", "python")
	for _, f := range files {
		us.get(f.Path).unit.Root = topDir(f.Path)
	}
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		b := us.byDir[path.Dir(f.Path)]
		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		var mods []string
		for _, m := range pyImport.FindAllSubmatch(src, -1) {
			for _, part := range strings.Split(string(m[1]), ",") {
				mods = append(mods, strings.TrimSpace(part))
			}
		}
		for _, m := range pyFromImport.FindAllSubmatch(src, -1) {
			mods = append(mods, string(m[1]))
		}
		for _, mod := range mods {
			if dir, ok := resolvePython(us, f.Path, mod); ok {
				b.dep(us.id(dir), f.Path, f.Test)
				continue
			}
			if f.Test {
				continue
			}
			if pyWebFrameworks[strings.Split(mod, ".")[0]] {
				b.tags["http-server"] = true
			}
			b.use("python", mod)
		}
		if f.Test {
			continue
		}
		for _, m := range pyRoute.FindAllSubmatch(src, -1) {
			method := strings.ToUpper(string(m[1]))
			if method == "ROUTE" {
				method = strings.ToUpper(string(m[3]))
			}
			route := string(m[2])
			if method != "" {
				route = method + " " + route
			}
			b.routes[route] = true
			b.tags["http-server"] = true
		}
		if pyMain.Match(src) {
			b.unit.Entry = true
		}
	}
	us.facts(&facts)
	return facts, nil
}

// resolvePython maps an import to an in-repo directory unit, if it is one.
func resolvePython(us *units, from, mod string) (string, bool) {
	if strings.HasPrefix(mod, ".") {
		dots := len(mod) - len(strings.TrimLeft(mod, "."))
		dir := path.Dir(from)
		for i := 1; i < dots; i++ {
			dir = path.Dir(dir)
		}
		rest := strings.ReplaceAll(strings.TrimLeft(mod, "."), ".", "/")
		if rest != "" && us.has(path.Join(dir, rest)) {
			return path.Join(dir, rest), true
		}
		return dir, us.has(dir)
	}
	p := strings.ReplaceAll(mod, ".", "/")
	for _, base := range []string{"", "src/"} {
		cand := base + p
		for cand != "." && cand != "" {
			if us.has(cand) {
				return cand, true
			}
			cand = path.Dir(cand)
		}
	}
	return "", false
}
