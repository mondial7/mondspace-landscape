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

// Swift analyzes Swift sources. Modules are top-level directories (Xcode
// targets) or SwiftPM "Sources/<Module>" directories.
type Swift struct{}

// NewSwift returns a Swift analyzer.
func NewSwift() Swift { return Swift{} }

// Name implements analyze.Analyzer.
func (Swift) Name() string { return "swift" }

var (
	swImport = regexp.MustCompile(`(?m)^\s*(?:@testable\s+)?import\s+(?:class\s+|struct\s+|enum\s+|func\s+)?(\w+)`)
	swMain   = regexp.MustCompile(`(?m)^\s*@main\b`)
)

var swiftUI = map[string]bool{"SwiftUI": true, "UIKit": true, "AppKit": true, "WidgetKit": true}

// Analyze implements analyze.Analyzer.
func (Swift) Analyze(ctx context.Context, repo *scan.Repo) (analyze.Facts, error) {
	var facts analyze.Facts
	files := repo.ByLanguage("swift")
	if len(files) == 0 {
		return facts, nil
	}
	packages := map[string]bool{} // SwiftPM package roots
	var code []scan.File
	for _, f := range files {
		if path.Base(f.Path) == "Package.swift" {
			packages[path.Dir(f.Path)] = true
			continue
		}
		code = append(code, f)
	}
	us := newUnits("swift", "swift")
	for _, f := range code {
		us.get(f.Path).unit.Root = swiftModule(packages, f.Path)
	}

	// Pass 1: read sources and index which unit declares which type.
	sources := map[string][]byte{}
	declared := map[string][]string{} // type name → unit IDs
	for _, f := range code {
		src, err := repo.Read(f.Path)
		if err != nil {
			return facts, fmt.Errorf("read %s: %w", f.Path, err)
		}
		sources[f.Path] = src
		if f.Test {
			continue
		}
		id := us.byDir[path.Dir(f.Path)].unit.ID
		for _, m := range swDecl.FindAllSubmatch(src, -1) {
			declared[string(m[1])] = addUniq(declared[string(m[1])], id)
		}
	}

	// Pass 2: imports give externals and visible modules; references to
	// declared types give precise unit-to-unit dependencies, both inside a
	// module (where Swift has no imports) and across imported modules.
	for _, f := range code {
		if err := ctx.Err(); err != nil {
			return facts, err
		}
		b := us.byDir[path.Dir(f.Path)]
		src := sources[f.Path]
		visible := map[string]bool{b.unit.Root: true}
		for _, m := range swImport.FindAllSubmatch(src, -1) {
			mod := string(m[1])
			if len(moduleUnits(us, mod)) > 0 {
				visible[mod] = true
				continue
			}
			if f.Test {
				continue
			}
			if swiftUI[mod] {
				b.tags["ui"] = true
			}
			b.use("swift", mod)
		}
		for _, ident := range uniqueIdents(src) {
			for _, target := range declared[ident] {
				if t := us.unitByID(target); t != nil && visible[t.unit.Root] && target != b.unit.ID {
					b.dep(target, f.Path, f.Test)
				}
			}
		}
		if swMain.Match(src) {
			b.unit.Entry = true
		}
	}
	us.facts(&facts)
	return facts, nil
}

var (
	swDecl  = regexp.MustCompile(`(?m)^\s*(?:(?:public|internal|private|fileprivate|open|final|indirect|@\w+)\s+)*(?:class|struct|enum|protocol|actor|typealias)\s+([A-Z]\w*)`)
	swIdent = regexp.MustCompile(`\b[A-Z]\w+\b`)
)

// uniqueIdents returns the distinct capitalised identifiers in src.
func uniqueIdents(src []byte) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range swIdent.FindAll(src, -1) {
		if s := string(m); !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func addUniq(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// swiftModule infers the module a Swift file belongs to: inside a SwiftPM
// package, "Sources/<Module>" or "Tests/<Module>"; otherwise the top-level
// directory (an Xcode target).
func swiftModule(packages map[string]bool, p string) string {
	parts := strings.Split(path.Dir(p), "/")
	for i, seg := range parts {
		if (seg == "Sources" || seg == "Tests") && i+1 < len(parts) && packages[packageDir(parts[:i])] {
			return parts[i+1]
		}
	}
	return topDir(p)
}

func packageDir(parts []string) string {
	if len(parts) == 0 {
		return "."
	}
	return strings.Join(parts, "/")
}

// moduleUnits returns the shallowest units of an in-repo module.
func moduleUnits(us *units, module string) []string {
	var out []string
	depth := -1
	for dir, b := range us.byDir {
		if b.unit.Root != module {
			continue
		}
		d := strings.Count(dir, "/")
		switch {
		case depth == -1 || d < depth:
			out, depth = []string{b.unit.ID}, d
		case d == depth:
			out = append(out, b.unit.ID)
		}
	}
	return out
}
