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

// HTML analyzes HTML templates and stylesheets: which pages exist and which
// server routes they link, submit or request (forms, links, htmx).
type HTML struct{}

// NewHTML returns an HTML/CSS analyzer.
func NewHTML() HTML { return HTML{} }

// Name implements analyze.Analyzer.
func (HTML) Name() string { return "html" }

var (
	htmlHX     = regexp.MustCompile(`\bhx-(get|post|put|patch|delete)\s*=\s*["'](/[^"']*)["']`)
	htmlForm   = regexp.MustCompile(`(?is)<form\b([^>]*)>`)
	htmlAttr   = regexp.MustCompile(`(?i)\b(action|method)\s*=\s*["']([^"']*)["']`)
	htmlLink   = regexp.MustCompile(`(?i)<(?:a|link|script|img)\b[^>]*?\b(?:href|src)\s*=\s*["'](/[^"']*)["']`)
	templateRe = regexp.MustCompile(`\{\{|\{%`)
)

// Analyze implements analyze.Analyzer.
func (HTML) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	files := append(repo.ByLanguage("html"), repo.ByLanguage("css")...)
	if len(files) == 0 {
		return facts, nil
	}
	us := newUnits("web", "html")
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		b := us.get(f.Path)
		b.unit.Root = topDir(f.Path)
		if f.Language == "css" {
			b.tags["assets"] = true
			if !b.tags["ui"] {
				b.unit.Language = "css"
			}
			continue
		}
		b.tags["ui"] = true
		b.unit.Language = "html"
		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		if templateRe.Match(src) {
			b.tags["templates"] = true
		}
		add := func(method, p string) {
			if strings.HasPrefix(p, "//") {
				return // protocol-relative: an external host, not a route
			}
			facts.Calls = append(facts.Calls, analyze.Call{File: f.Path, Method: method, Path: p})
		}
		for _, m := range htmlHX.FindAllSubmatch(src, -1) {
			add(strings.ToUpper(string(m[1])), string(m[2]))
		}
		for _, m := range htmlForm.FindAllSubmatch(src, -1) {
			method, action := "GET", ""
			for _, a := range htmlAttr.FindAllSubmatch(m[1], -1) {
				switch strings.ToLower(string(a[1])) {
				case "method":
					method = strings.ToUpper(string(a[2]))
				case "action":
					action = string(a[2])
				}
			}
			if strings.HasPrefix(action, "/") {
				add(method, action)
			}
		}
		for _, m := range htmlLink.FindAllSubmatch(src, -1) {
			add("GET", string(m[1]))
		}
	}
	for _, b := range us.byDir {
		if b.tags["ui"] {
			delete(b.tags, "assets")
		}
		if path.Base(b.unit.Dir) == "templates" {
			b.tags["templates"] = true
		}
	}
	us.facts(&facts)
	return facts, nil
}
