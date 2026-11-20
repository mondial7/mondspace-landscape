package scan

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

// hostileRepo builds a repository whose README and .gitignore are symlinks
// to a secret outside it, as a malicious clone could contain.
func hostileRepo(t *testing.T) (dir, secret string) {
	t.Helper()
	base := t.TempDir()
	secret = "PRIVATE KEY MATERIAL"
	outside := filepath.Join(base, "id_rsa")
	if err := os.WriteFile(outside, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(base, "repo")
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644))
	must(os.Symlink("../id_rsa", filepath.Join(dir, "README.md")))  // relative escape
	must(os.Symlink(outside, filepath.Join(dir, ".gitignore")))     // absolute escape
	must(os.Symlink(outside, filepath.Join(dir, "pkg", "leak.go"))) // a "source file"
	return dir, secret
}

func TestOpenDirRefusesSymlinksLeavingTheRepository(t *testing.T) {
	dir, secret := hostileRepo(t)
	fsys, closeFS, err := OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFS()

	for _, p := range []string{"README.md", ".gitignore", "pkg/leak.go"} {
		if data, err := ReadFile(fsys, p); err == nil || strings.Contains(string(data), secret) {
			t.Errorf("ReadFile(%s) = %q, %v; want an error and no secret", p, data, err)
		}
	}
	repo, err := Scan(fsys, "repo")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range repo.Files {
		if f.Path != "main.go" {
			t.Errorf("scanned %s, want only main.go", f.Path)
		}
	}
	if data, err := repo.Read("README.md"); err == nil {
		t.Errorf("Repo.Read(README.md) = %q, want an error", data)
	}
}

func TestReadFileRefusesNonRegularAndOversizedFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"big.txt": {Data: bytes.Repeat([]byte("x"), MaxFileSize+1)},
		"ok.txt":  {Data: []byte("fine")},
		"pipe":    {Mode: fs.ModeNamedPipe},
		"dir":     {Mode: fs.ModeDir},
	}
	if data, err := ReadFile(fsys, "ok.txt"); err != nil || string(data) != "fine" {
		t.Errorf("ReadFile(ok.txt) = %q, %v", data, err)
	}
	for _, p := range []string{"big.txt", "pipe", "dir", "missing"} {
		if _, err := ReadFile(fsys, p); err == nil {
			t.Errorf("ReadFile(%s) succeeded, want an error", p)
		}
	}
}

func TestGitignorePatternsAreCapped(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxIgnorePatterns+500; i++ {
		b.WriteString("*.tmp\n")
	}
	g := loadGitignore(fstest.MapFS{".gitignore": {Data: []byte(b.String())}})
	if len(g.patterns) != MaxIgnorePatterns {
		t.Errorf("patterns = %d, want %d", len(g.patterns), MaxIgnorePatterns)
	}
}
