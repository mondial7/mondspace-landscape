package script

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// JS analyzes JavaScript and TypeScript.
type JS struct{}

// NewJS returns a JavaScript/TypeScript analyzer.
func NewJS() JS { return JS{} }

// Name implements analyze.Analyzer.
func (JS) Name() string { return "javascript" }

var (
	jsImport = regexp.MustCompile(`(?m)(?:\bimport\s+(?:[\w*{}\s,$]+\s+from\s+)?|\bexport\s+[\w*{}\s,$]*\s+from\s+|\brequire\(\s*|\bimport\(\s*)['"]([^'"]+)['"]`)
	jsFetch  = regexp.MustCompile("\\bfetch\\(\\s*([\"'`])(/[^\"'`]*)([\"'`])")
	jsAxios  = regexp.MustCompile("\\baxios\\.(get|post|put|patch|delete)\\(\\s*[\"'`](/[^\"'`]*)[\"'`]")
	jsMethod = regexp.MustCompile(`method:\s*['"](\w+)['"]`)
	jsRoute  = regexp.MustCompile("\\b(?:app|router|server|api|fastify)\\.(get|post|put|patch|delete|all)\\(\\s*[\"'`](/[^\"'`]*)[\"'`]")
	jsListen = regexp.MustCompile(`\.listen\(`)
	jsServer = regexp.MustCompile(`createServer\(|\bexpress\(\)|\bfastify\(|\bnew Koa\(|\bHono\(`)
	jsUI     = regexp.MustCompile(`\bdocument\.|\bwindow\.|\bnavigator\.|self\.addEventListener\(`)
)

var uiLibs = map[string]bool{"react": true, "react-dom": true, "vue": true, "svelte": true, "preact": true, "solid-js": true, "@angular/core": true, "lit": true}

// Analyze implements analyze.Analyzer.
func (JS) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	files := append(repo.ByLanguage("javascript"), repo.ByLanguage("typescript")...)
	if len(files) == 0 {
		return facts, nil
	}
	roots, mains := packageRoots(repo)
	us := newUnits("js", "javascript")
	for _, f := range files {
		if f.Language == "typescript" {
			us.lang = "typescript" // dominant language label is good enough for display
		}
		us.get(f.Path)
	}

	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		b := us.byDir[path.Dir(f.Path)]
		b.unit.Root = nearestRoot(roots, f.Path)
		if strings.HasSuffix(f.Path, ".tsx") || strings.HasSuffix(f.Path, ".jsx") {
			b.tags["ui"] = true
		}
		if mains[f.Path] {
			b.unit.Entry = true
		}
		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		for _, m := range jsImport.FindAllSubmatch(src, -1) {
			imp := string(m[1])
			if strings.HasPrefix(imp, ".") {
				if dir, ok := resolveRelative(repo, us, f.Path, imp); ok {
					b.dep(us.id(dir), f.Path, f.Test)
				}
				continue
			}
			if f.Test {
				continue
			}
			pkg := packageName(imp)
			if uiLibs[pkg] {
				b.tags["ui"] = true
			}
			b.use("javascript", imp)
		}
		if f.Test {
			continue
		}
		if jsUI.Match(src) {
			b.tags["ui"] = true
		}
		for _, m := range jsRoute.FindAllSubmatch(src, -1) {
			method := strings.ToUpper(string(m[1]))
			if method == "ALL" {
				b.routes[string(m[2])] = true
			} else {
				b.routes[method+" "+string(m[2])] = true
			}
			b.tags["http-server"] = true
		}
		if jsListen.Match(src) && (jsRoute.Match(src) || jsServer.Match(src)) {
			b.tags["http-server"] = true
			b.unit.Entry = true
		}
		for _, loc := range jsFetch.FindAllSubmatchIndex(src, -1) {
			p := string(src[loc[4]:loc[5]])
			// Look for an options object only inside this call's arguments.
			end := len(src)
			if i := strings.IndexByte(string(src[loc[1]:]), ')'); i >= 0 {
				end = loc[1] + i
			}
			method := ""
			if mm := jsMethod.FindSubmatch(src[loc[1]:end]); mm != nil {
				method = strings.ToUpper(string(mm[1]))
			}
			facts.Calls = append(facts.Calls, analyze.Call{File: f.Path, Method: method, Path: p})
		}
		for _, m := range jsAxios.FindAllSubmatch(src, -1) {
			facts.Calls = append(facts.Calls, analyze.Call{File: f.Path, Method: strings.ToUpper(string(m[1])), Path: string(m[2])})
		}
	}
	us.facts(&facts)
	return facts, nil
}

// packageRoots returns the directories holding a package.json and the
// files they declare as entry points (main/bin).
func packageRoots(repo *scan.Repo) (roots []string, mains map[string]bool) {
	mains = map[string]bool{}
	for _, f := range repo.Files {
		if path.Base(f.Path) != "package.json" {
			continue
		}
		dir := path.Dir(f.Path)
		roots = append(roots, dir)
		data, err := repo.Read(f.Path)
		if err != nil {
			continue
		}
		var pj struct {
			Main string          `json:"main"`
			Bin  json.RawMessage `json:"bin"`
		}
		if json.Unmarshal(data, &pj) != nil {
			continue
		}
		if pj.Main != "" {
			mains[path.Join(dir, pj.Main)] = true
		}
		var bin string
		if json.Unmarshal(pj.Bin, &bin) == nil && bin != "" {
			mains[path.Join(dir, bin)] = true
		}
		var bins map[string]string
		if json.Unmarshal(pj.Bin, &bins) == nil {
			for _, b := range bins {
				mains[path.Join(dir, b)] = true
			}
		}
	}
	return roots, mains
}

func nearestRoot(roots []string, file string) string {
	best := ""
	for _, r := range roots {
		if (r == "." || strings.HasPrefix(file, r+"/")) && len(r) >= len(best) {
			best = r
		}
	}
	if best == "" {
		return topDir(file)
	}
	return best
}

// resolveRelative maps a relative import to the directory unit it lands in.
func resolveRelative(repo *scan.Repo, us *units, from, imp string) (string, bool) {
	target := path.Join(path.Dir(from), imp)
	if us.has(target) {
		return target, true // directory import (index file)
	}
	dir := path.Dir(target)
	for _, f := range repo.Files {
		if strings.HasPrefix(f.Path, target+".") && path.Dir(f.Path) == dir {
			return dir, us.has(dir)
		}
	}
	return "", false
}

// packageName returns the npm package of an import ("@a/b/c" → "@a/b", "x/y" → "x").
func packageName(imp string) string {
	parts := strings.Split(imp, "/")
	if strings.HasPrefix(imp, "@") && len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}
