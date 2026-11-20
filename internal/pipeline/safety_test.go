package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// A cloned repository whose README is a symlink to a file outside it must
// not get that file into the model (and so the UI, the JSON or the LLM).
func TestSymlinkedReadmeDoesNotLeakOutsideFiles(t *testing.T) {
	base := t.TempDir()
	secret := "BEGIN OPENSSH PRIVATE KEY"
	if err := os.WriteFile(filepath.Join(base, "id_rsa"), []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(base, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../id_rsa", filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}

	fsys, closeFS, err := scan.OpenDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer closeFS()
	m, err := New().Run(context.Background(), fsys, "repo")
	if err != nil {
		t.Fatal(err)
	}
	out, _ := json.Marshal(map[string]any{"s": m.Summary, "n": m.Graph.Nodes()})
	if strings.Contains(string(out), secret) {
		t.Fatalf("model contains the outside file: %s", out)
	}
}
