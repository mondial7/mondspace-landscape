// Package server exposes analyses over a small local JSON API and serves
// the interactive diagram UI.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mondial7/mondspace-landscape/internal/enrich"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
	"github.com/mondial7/mondspace-landscape/internal/scan"
	"github.com/mondial7/mondspace-landscape/internal/source"
	"github.com/mondial7/mondspace-landscape/internal/web"
)

// Resolver maps user input to a local repository.
type Resolver interface {
	Resolve(ctx context.Context, input string) (source.Repo, error)
}

// Analyzer builds a landscape from a repository file system.
type Analyzer interface {
	Run(ctx context.Context, fsys fs.FS, name string) (*landscape.Model, error)
}

// Enricher describes a model, applying results through hooks.
type Enricher interface {
	Run(ctx context.Context, m *landscape.Model, src enrich.Source, h enrich.Hooks) error
	Label() string
}

// Config wires the server's collaborators.
type Config struct {
	Resolver Resolver
	Analyzer Analyzer
	Enricher Enricher                            // optional
	OpenFS   func(string) (fs.FS, func(), error) // defaults to scan.OpenDir
	Initial  string                              // optional source analysed at startup
	Timeout  time.Duration                       // per analysis; default 15 minutes
	Logger   *slog.Logger
	// MaxRunning caps analyses in progress at once (default 3); more are refused.
	MaxRunning int
	// MaxJobs caps analyses kept in memory (default 50); the oldest finished go first.
	MaxJobs int
}

// Status is the lifecycle state of an analysis.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusResolving Status = "resolving"
	StatusAnalyzing Status = "analyzing"
	StatusEnriching Status = "enriching" // diagram available, descriptions in progress
	StatusReady     Status = "ready"
	StatusFailed    Status = "failed"
)

// Job is the public view of an analysis.
type Job struct {
	ID       string             `json:"id"`
	Source   string             `json:"source"`
	Remote   string             `json:"remote,omitempty"`
	Dir      string             `json:"dir,omitempty"` // resolved local directory
	Status   Status             `json:"status"`
	Error    string             `json:"error,omitempty"`
	Summary  *landscape.Summary `json:"summary,omitempty"`
	Progress *enrich.Progress   `json:"progress,omitempty"`
	LLM      string             `json:"llm,omitempty"`
	Started  time.Time          `json:"started"`
	Finished *time.Time         `json:"finished,omitempty"`
}

type job struct {
	mu    sync.RWMutex // guards info and model (including graph writes)
	src   string       // the source as the user gave it
	info  Job
	model *landscape.Model
}

func (j *job) snapshot() Job {
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.info
}

func (j *job) update(fn func(*Job)) {
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(&j.info)
}

// Server holds analyses in memory. Construct with New.
type Server struct {
	cfg     Config
	mu      sync.RWMutex
	jobs    map[string]*job
	order   []string
	initial string
}

// New returns a server; if cfg.Initial is set its analysis starts immediately.
func New(cfg Config) *Server {
	if cfg.OpenFS == nil {
		cfg.OpenFS = scan.OpenDir
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Minute
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	if cfg.MaxRunning <= 0 {
		cfg.MaxRunning = 3
	}
	if cfg.MaxJobs <= 0 {
		cfg.MaxJobs = 50
	}
	s := &Server{cfg: cfg, jobs: map[string]*job{}}
	if cfg.Initial != "" {
		s.initial = s.Start(cfg.Initial).ID
	}
	return s
}

// InitialID returns the job started for Config.Initial ("" if none).
func (s *Server) InitialID() string { return s.initial }

// Start begins analysing src in the background and returns the new job.
func (s *Server) Start(src string) Job {
	// The public Source and Dir show the home directory as "~"; src keeps the input as given.
	j := &job{src: src, info: Job{ID: newID(), Source: source.Display(src), Status: StatusQueued, Started: time.Now()}}
	s.mu.Lock()
	s.jobs[j.info.ID] = j
	s.order = append(s.order, j.info.ID)
	s.evictLocked()
	s.mu.Unlock()
	go s.run(j)
	return j.snapshot()
}

// evictLocked drops the oldest finished analyses beyond MaxJobs, so a long
// running server does not keep every model in memory. Callers hold s.mu.
func (s *Server) evictLocked() {
	for i := 0; len(s.order) > s.cfg.MaxJobs && i < len(s.order); {
		id := s.order[i]
		if !s.jobs[id].snapshot().done() {
			i++
			continue
		}
		delete(s.jobs, id)
		s.order = append(s.order[:i], s.order[i+1:]...)
	}
}

// running counts analyses still in progress.
func (s *Server) running() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := 0
	for _, j := range s.jobs {
		if !j.snapshot().done() {
			n++
		}
	}
	return n
}

func (j Job) done() bool { return j.Status == StatusReady || j.Status == StatusFailed }

func (s *Server) run(j *job) {
	ctx, cancel := context.WithTimeout(context.Background(), s.cfg.Timeout)
	defer cancel()
	log := s.cfg.Logger.With("job", j.info.ID, "source", j.info.Source)
	fail := func(err error) {
		log.Error("analysis failed", "err", err)
		j.update(func(i *Job) { i.Status, i.Error = StatusFailed, err.Error(); i.Finished = ptr(time.Now()) })
	}

	j.update(func(i *Job) { i.Status = StatusResolving })
	repo, err := s.cfg.Resolver.Resolve(ctx, j.src)
	if err != nil {
		fail(err)
		return
	}
	j.update(func(i *Job) { i.Status, i.Remote, i.Dir = StatusAnalyzing, repo.Remote, source.Display(repo.Dir) })
	fsys, closeFS, err := s.cfg.OpenFS(repo.Dir)
	if err != nil {
		fail(err)
		return
	}
	defer closeFS()
	m, err := s.cfg.Analyzer.Run(ctx, fsys, repo.Name)
	if err != nil {
		fail(err)
		return
	}
	log.Info("analysis complete", "summary", m.String())
	if s.cfg.Enricher == nil {
		j.mu.Lock()
		j.model, j.info.Summary, j.info.Status, j.info.Finished = m, &m.Summary, StatusReady, ptr(time.Now())
		j.mu.Unlock()
		return
	}

	label := s.cfg.Enricher.Label()
	j.mu.Lock()
	j.model, j.info.Summary, j.info.Status, j.info.LLM = m, &m.Summary, StatusEnriching, label
	j.mu.Unlock()
	hooks := enrich.Hooks{
		Apply: func(r enrich.Results) {
			j.mu.Lock()
			defer j.mu.Unlock()
			enrich.Apply(m.Graph, r, label)
		},
		Progress: func(p enrich.Progress) { j.update(func(i *Job) { i.Progress = &p }) },
	}
	if err := s.cfg.Enricher.Run(ctx, m, fsSource{fsys}, hooks); err != nil {
		log.Warn("enrichment incomplete", "err", err)
		j.update(func(i *Job) { i.Summary.Warnings = append(i.Summary.Warnings, "enrichment: "+firstLine(err.Error())) })
	}
	j.update(func(i *Job) { i.Status, i.Progress, i.Finished = StatusReady, nil, ptr(time.Now()) })
}

func (s *Server) job(id string) *job {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

// Handler returns the HTTP handler (API + UI).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.handleConfig)
	mux.HandleFunc("GET /api/analyses", s.handleList)
	mux.HandleFunc("POST /api/analyses", s.handleCreate)
	mux.HandleFunc("GET /api/analyses/{id}", s.handleGet)
	mux.HandleFunc("GET /api/analyses/{id}/view", s.handleView)
	mux.HandleFunc("GET /api/analyses/{id}/graph", s.handleGraph)
	mux.Handle("GET /", web.Handler())
	return localOnly(mux)
}

func (s *Server) handleConfig(w http.ResponseWriter, _ *http.Request) {
	cfg := map[string]string{"initial": s.initial}
	if s.cfg.Enricher != nil {
		cfg["llm"] = s.cfg.Enricher.Label()
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleList(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	out := make([]Job, 0, len(s.order))
	for _, id := range s.order {
		out = append(out, s.jobs[id].snapshot())
	}
	s.mu.RUnlock()
	sort.SliceStable(out, func(i, j int) bool { return out[i].Started.After(out[j].Started) })
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "content type must be application/json")
		return
	}
	var req struct {
		Source string `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	src := strings.TrimSpace(req.Source)
	if src == "" {
		writeError(w, http.StatusBadRequest, "source is required (a GitHub URL or a local path)")
		return
	}
	if s.running() >= s.cfg.MaxRunning {
		writeError(w, http.StatusTooManyRequests, "too many analyses in progress; wait for one to finish")
		return
	}
	writeJSON(w, http.StatusAccepted, s.Start(src))
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	j := s.job(r.PathValue("id"))
	if j == nil {
		writeError(w, http.StatusNotFound, "analysis not found")
		return
	}
	writeJSON(w, http.StatusOK, j.snapshot())
}

// withModel runs fn with read access to a job's model.
func (s *Server) withModel(w http.ResponseWriter, r *http.Request, fn func(*landscape.Model)) {
	j := s.job(r.PathValue("id"))
	if j == nil {
		writeError(w, http.StatusNotFound, "analysis not found")
		return
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.model == nil {
		writeError(w, http.StatusConflict, "analysis is "+string(j.info.Status))
		return
	}
	fn(j.model)
}

func (s *Server) handleView(w http.ResponseWriter, r *http.Request) {
	s.withModel(w, r, func(m *landscape.Model) {
		v, err := m.Graph.View(r.URL.Query().Get("focus"))
		if errors.Is(err, graph.ErrUnknownNode) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, v)
	})
}

func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	s.withModel(w, r, func(m *landscape.Model) {
		writeJSON(w, http.StatusOK, map[string]any{
			"summary": m.Summary, "nodes": m.Graph.Nodes(), "edges": m.Graph.Edges(),
		})
	})
}

// contentSecurityPolicy allows only the app's own scripts, styles and API.
// The UI never builds HTML from data, so no inline code is needed.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"img-src 'self' data: blob:; connect-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

// localOnly rejects requests whose Host is not a loopback name, defeating
// DNS-rebinding attacks against this local server, refuses state-changing
// requests from other origins (CSRF) and sets the security headers.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		switch host {
		case "localhost", "127.0.0.1", "::1":
		default:
			writeError(w, http.StatusForbidden, "forbidden host")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", contentSecurityPolicy)
		h.Set("X-Frame-Options", "DENY")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether a browser request comes from this server's own
// pages. Requests without these headers (curl, the CLI) are not from a
// browser page and are allowed; the Host check above already applies.
func sameOrigin(r *http.Request) bool {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return false
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		return false
	}
	return true
}

type fsSource struct{ fsys fs.FS }

func (s fsSource) Read(p string) ([]byte, error) { return scan.ReadFile(s.fsys, p) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func ptr[T any](v T) *T { return &v }

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
