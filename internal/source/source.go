// Package source resolves what the user wants analyzed — the current
// directory, a local path or a public GitHub repository — to a local
// directory, cloning remote repositories into a cache.
package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// Repo is a resolved, locally available repository.
type Repo struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Remote string `json:"remote,omitempty"` // canonical web URL for remote repos
}

// GitHub identifies a GitHub repository.
type GitHub struct{ Owner, Repo string }

// CloneURL returns the HTTPS clone URL.
func (g GitHub) CloneURL() string { return "https://github.com/" + g.Owner + "/" + g.Repo + ".git" }

// WebURL returns the repository's web page.
func (g GitHub) WebURL() string { return "https://github.com/" + g.Owner + "/" + g.Repo }

var (
	ghName  = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	ghHTTPS = regexp.MustCompile(`^(?:https?://)?(?:www\.)?github\.com/([^/]+)/([^/]+)(?:/.*)?$`)
	ghSSH   = regexp.MustCompile(`^git@github\.com:([^/]+)/([^/]+)$`)
)

// ParseGitHub recognises GitHub repository URLs in their common forms.
func ParseGitHub(s string) (GitHub, bool) {
	s = strings.TrimSpace(s)
	m := ghHTTPS.FindStringSubmatch(s)
	if m == nil {
		m = ghSSH.FindStringSubmatch(s)
	}
	if m == nil {
		return GitHub{}, false
	}
	owner, repo := m[1], strings.TrimSuffix(m[2], ".git")
	if !ghName.MatchString(owner) || !ghName.MatchString(repo) || strings.HasPrefix(owner, ".") || strings.HasPrefix(repo, ".") {
		return GitHub{}, false
	}
	return GitHub{Owner: owner, Repo: repo}, true
}

// Git clones and updates repositories.
type Git interface {
	Clone(ctx context.Context, url, dir string) error
	Update(ctx context.Context, dir string) error
}

// Resolver turns user input into a local repository. Safe for concurrent use.
type Resolver struct {
	cacheDir string
	git      Git
	mu       sync.Mutex // serialises clones into the cache
}

// NewResolver returns a resolver cloning into cacheDir.
func NewResolver(cacheDir string, git Git) *Resolver {
	return &Resolver{cacheDir: cacheDir, git: git}
}

// ErrUnsupported is returned for inputs that are neither a directory nor a GitHub URL.
var ErrUnsupported = errors.New("unsupported source")

// Resolve maps input ("" means the current directory) to a local repository.
func (r *Resolver) Resolve(ctx context.Context, input string) (Repo, error) {
	input = strings.TrimSpace(input)
	if gh, ok := ParseGitHub(input); ok {
		return r.github(ctx, gh)
	}
	if strings.Contains(input, "://") || strings.HasPrefix(input, "git@") {
		return Repo{}, fmt.Errorf("%w: %q (only local paths and public github.com repositories are supported)", ErrUnsupported, input)
	}
	if input == "" {
		input = "."
	}
	dir, err := filepath.Abs(input)
	if err != nil {
		return Repo{}, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return Repo{}, fmt.Errorf("open %s: %w", input, err)
	}
	if !info.IsDir() {
		return Repo{}, fmt.Errorf("%s is not a directory", input)
	}
	return Repo{Dir: dir, Name: filepath.Base(dir)}, nil
}

func (r *Resolver) github(ctx context.Context, gh GitHub) (Repo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	dir := filepath.Join(r.cacheDir, "github.com", gh.Owner, gh.Repo)
	repo := Repo{Dir: dir, Name: gh.Repo, Remote: gh.WebURL()}
	if _, err := os.Stat(dir); err == nil {
		if err := r.git.Update(ctx, dir); err != nil {
			return Repo{}, fmt.Errorf("update %s: %w", gh.WebURL(), err)
		}
		return repo, nil
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Repo{}, err
	}
	if err := r.git.Clone(ctx, gh.CloneURL(), dir); err != nil {
		_ = os.RemoveAll(dir)
		return Repo{}, fmt.Errorf("clone %s: %w", gh.WebURL(), err)
	}
	return repo, nil
}

// GitCLI implements Git with the git command-line tool.
type GitCLI struct{}

// Clone makes a shallow clone of url into dir.
func (GitCLI) Clone(ctx context.Context, url, dir string) error {
	return git(ctx, "", "clone", "--depth", "1", "--single-branch", "--no-tags", "--", url, dir)
}

// Update fetches the latest commit of the cloned branch and checks it out.
func (GitCLI) Update(ctx context.Context, dir string) error {
	if err := git(ctx, dir, "fetch", "--depth", "1", "--no-tags", "origin"); err != nil {
		return err
	}
	return git(ctx, dir, "reset", "--hard", "-q", "FETCH_HEAD")
}

func git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	// Never prompt for credentials: private repositories fail fast instead of hanging.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(lastLine(string(out))))
	}
	return nil
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}
