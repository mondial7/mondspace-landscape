// Package scan walks a repository, filters out noise (VCS metadata,
// dependencies, build output, .gitignore'd paths) and classifies files.
package scan

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// File is a source file found in the repository.
type File struct {
	Path     string `json:"path"` // slash-separated, relative to the repo root
	Language string `json:"language"`
	Lines    int    `json:"lines"`
	Test     bool   `json:"test"`
}

// Repo is the scanned view of a repository.
type Repo struct {
	Name  string
	FS    fs.FS
	Files []File
	index map[string]int
}

// Limits protecting the scanner from huge repositories.
const (
	MaxFileSize = 1 << 20 // bytes
	MaxFiles    = 20000
)

// skipDirs are never descended into.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true, "vendor": true,
	"dist": true, "build": true, ".build": true, "out": true, "target": true,
	".next": true, ".nuxt": true, ".venv": true, "venv": true, "__pycache__": true,
	"Pods": true, "DerivedData": true, ".idea": true, ".vscode": true,
	".codeboarding": true, "coverage": true, ".cache": true, ".gradle": true,
}

var extLanguage = map[string]string{
	".go": "go", ".js": "javascript", ".mjs": "javascript", ".cjs": "javascript", ".jsx": "javascript",
	".ts": "typescript", ".tsx": "typescript", ".mts": "typescript",
	".py": "python", ".swift": "swift", ".kt": "kotlin", ".java": "java", ".rb": "ruby",
	".rs": "rust", ".c": "c", ".h": "c", ".cpp": "cpp", ".cs": "csharp", ".php": "php",
	".html": "html", ".htm": "html", ".tmpl": "html", ".gohtml": "html", ".css": "css", ".scss": "css",
	".sql": "sql", ".sh": "shell", ".yml": "yaml", ".yaml": "yaml", ".toml": "toml",
	".json": "json", ".md": "markdown", ".tf": "terraform", ".proto": "protobuf",
}

// Language returns a language identifier for path, or "" if unknown.
func Language(p string) string {
	base := path.Base(p)
	if base == "Dockerfile" || strings.HasSuffix(base, ".Dockerfile") || strings.HasPrefix(base, "Dockerfile.") {
		return "dockerfile"
	}
	if base == "Makefile" {
		return "make"
	}
	return extLanguage[strings.ToLower(path.Ext(base))]
}

// testDirs are directories whose whole content is test code.
var testDirs = map[string]bool{"test": true, "tests": true, "Tests": true, "__tests__": true, "spec": true, "UITests": true}

// IsTest reports whether path looks like a test file.
func IsTest(p string) bool {
	base := path.Base(p)
	switch {
	case strings.HasSuffix(base, "_test.go"):
		return true
	case strings.Contains(base, ".test.") || strings.Contains(base, ".spec."):
		return true
	case strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py"):
		return true
	case strings.HasSuffix(base, "Tests.swift") || strings.HasSuffix(base, "Test.swift"):
		return true
	}
	for _, dir := range strings.Split(path.Dir(p), "/") {
		switch {
		case testDirs[dir], len(dir) > len("Tests") && strings.HasSuffix(dir, "Tests"):
			return true
		}
	}
	return false
}

// Scan walks fsys and returns the classified source files, sorted by path.
func Scan(fsys fs.FS, name string) (*Repo, error) {
	ign := loadGitignore(fsys)
	repo := &Repo{Name: name, FS: fsys, index: map[string]int{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == "." {
			return nil
		}
		if d.IsDir() {
			if skipDirs[d.Name()] || ign.match(p, true) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || ign.match(p, false) {
			return nil
		}
		lang := Language(p)
		if lang == "" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > MaxFileSize {
			return nil
		}
		if len(repo.Files) >= MaxFiles {
			return fs.SkipAll
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		repo.Files = append(repo.Files, File{Path: p, Language: lang, Lines: countLines(data), Test: IsTest(p)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("scan %s: %w", name, err)
	}
	sort.Slice(repo.Files, func(i, j int) bool { return repo.Files[i].Path < repo.Files[j].Path })
	for i, f := range repo.Files {
		repo.index[f.Path] = i
	}
	return repo, nil
}

// File looks up a scanned file by path.
func (r *Repo) File(p string) (File, bool) {
	i, ok := r.index[p]
	if !ok {
		return File{}, false
	}
	return r.Files[i], true
}

// Read returns the content of a file in the repository.
func (r *Repo) Read(p string) ([]byte, error) {
	return fs.ReadFile(r.FS, p)
}

// Languages counts files per language.
func (r *Repo) Languages() map[string]int {
	out := map[string]int{}
	for _, f := range r.Files {
		out[f.Language]++
	}
	return out
}

// ByLanguage returns the files of one language.
func (r *Repo) ByLanguage(lang string) []File {
	var out []File
	for _, f := range r.Files {
		if f.Language == lang {
			out = append(out, f)
		}
	}
	return out
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	n := bytes.Count(b, []byte{'\n'})
	if b[len(b)-1] != '\n' {
		n++
	}
	return n
}

// gitignore supports the common subset of .gitignore: comments, directory
// patterns ("dir/"), anchored patterns ("/x") and globs ("*.log").
type gitignore struct{ patterns []ignorePattern }

type ignorePattern struct {
	glob     string
	dirOnly  bool
	anchored bool
}

func loadGitignore(fsys fs.FS) gitignore {
	var g gitignore
	data, err := fs.ReadFile(fsys, ".gitignore")
	if err != nil {
		return g
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		p := ignorePattern{}
		if strings.HasSuffix(line, "/") {
			p.dirOnly = true
			line = strings.TrimSuffix(line, "/")
		}
		if strings.HasPrefix(line, "/") {
			p.anchored = true
			line = strings.TrimPrefix(line, "/")
		} else if strings.Contains(line, "/") {
			p.anchored = true
		}
		p.glob = line
		g.patterns = append(g.patterns, p)
	}
	return g
}

func (g gitignore) match(p string, isDir bool) bool {
	for _, pat := range g.patterns {
		if pat.dirOnly && !isDir {
			continue
		}
		target := path.Base(p)
		if pat.anchored {
			target = p
		}
		if ok, _ := path.Match(pat.glob, target); ok {
			return true
		}
	}
	return false
}
