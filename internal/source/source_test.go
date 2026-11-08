package source

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestParseGitHub(t *testing.T) {
	tests := []struct {
		in, owner, repo string
		ok              bool
	}{
		{"https://github.com/mondial7/mmtime", "mondial7", "mmtime", true},
		{"https://github.com/mondial7/mmtime.git", "mondial7", "mmtime", true},
		{"https://github.com/golang/go/tree/master/src", "golang", "go", true},
		{"github.com/a-b/c.d_e", "a-b", "c.d_e", true},
		{"git@github.com:owner/name.git", "owner", "name", true},
		{"http://github.com/x/y/", "x", "y", true},
		{"https://gitlab.com/x/y", "", "", false},
		{"https://github.com/x", "", "", false},
		{"https://github.com/x/y;rm -rf", "", "", false},
		{"https://github.com/../etc", "", "", false},
		{"./local/path", "", "", false},
	}
	for _, tt := range tests {
		gh, ok := ParseGitHub(tt.in)
		if ok != tt.ok || gh.Owner != tt.owner || gh.Repo != tt.repo {
			t.Errorf("ParseGitHub(%q) = %+v,%v want %s/%s,%v", tt.in, gh, ok, tt.owner, tt.repo, tt.ok)
		}
	}
	gh, _ := ParseGitHub("github.com/a/b")
	if gh.CloneURL() != "https://github.com/a/b.git" {
		t.Errorf("CloneURL = %q", gh.CloneURL())
	}
}

type fakeGit struct {
	cloned, updated []string
	err             error
}

func (f *fakeGit) Clone(_ context.Context, url, dir string) error {
	f.cloned = append(f.cloned, url+" -> "+dir)
	if f.err != nil {
		return f.err
	}
	return os.MkdirAll(dir, 0o755)
}

func (f *fakeGit) Update(_ context.Context, dir string) error {
	f.updated = append(f.updated, dir)
	return f.err
}

func TestResolveLocalPath(t *testing.T) {
	dir := t.TempDir()
	r := NewResolver(t.TempDir(), &fakeGit{})
	got, err := r.Resolve(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Dir != dir || got.Name != filepath.Base(dir) || got.Remote != "" {
		t.Errorf("Resolve(local) = %+v", got)
	}
	if _, err := r.Resolve(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Error("Resolve(missing) = nil error")
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Resolve(context.Background(), file); err == nil {
		t.Error("Resolve(file) = nil error, want 'not a directory'")
	}
}

func TestResolveGitHubClonesThenUpdates(t *testing.T) {
	cache := t.TempDir()
	git := &fakeGit{}
	r := NewResolver(cache, git)
	got, err := r.Resolve(context.Background(), "https://github.com/mondial7/mmtime")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(cache, "github.com", "mondial7", "mmtime")
	if got.Dir != want || got.Name != "mmtime" || got.Remote != "https://github.com/mondial7/mmtime" {
		t.Errorf("Resolve(github) = %+v, want dir %s", got, want)
	}
	if len(git.cloned) != 1 || git.cloned[0] != "https://github.com/mondial7/mmtime.git -> "+want {
		t.Errorf("cloned = %v", git.cloned)
	}
	if _, err := r.Resolve(context.Background(), "github.com/mondial7/mmtime"); err != nil {
		t.Fatal(err)
	}
	if len(git.cloned) != 1 || len(git.updated) != 1 {
		t.Errorf("second resolve should update, not clone: cloned=%v updated=%v", git.cloned, git.updated)
	}
}

func TestResolveGitHubCloneFailureCleansUp(t *testing.T) {
	cache := t.TempDir()
	r := NewResolver(cache, &fakeGit{err: errors.New("repository not found")})
	if _, err := r.Resolve(context.Background(), "https://github.com/x/private"); err == nil {
		t.Fatal("want clone error")
	}
	if _, err := os.Stat(filepath.Join(cache, "github.com", "x", "private")); !os.IsNotExist(err) {
		t.Errorf("failed clone left a directory behind (stat err = %v)", err)
	}
}

func TestResolveRejectsOtherURLs(t *testing.T) {
	r := NewResolver(t.TempDir(), &fakeGit{})
	if _, err := r.Resolve(context.Background(), "https://gitlab.com/a/b"); err == nil {
		t.Error("non-GitHub URL should be rejected")
	}
}

// TestGitCLI exercises the real git binary against a local repository.
func TestGitCLI(t *testing.T) {
	if testing.Short() {
		t.Skip("uses git")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	origin := t.TempDir()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", ".")
	run(origin, "commit", "-q", "-m", "one")

	dest := filepath.Join(t.TempDir(), "clone")
	g := GitCLI{}
	if err := g.Clone(context.Background(), "file://"+origin, dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "a.go")); err != nil {
		t.Fatalf("clone missing file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(origin, "b.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", ".")
	run(origin, "commit", "-q", "-m", "two")
	if err := g.Update(context.Background(), dest); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "b.go")); err != nil {
		t.Fatalf("update did not fetch new commit: %v", err)
	}
}
