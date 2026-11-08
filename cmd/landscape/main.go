// Command landscape analyzes a code repository and opens an interactive,
// zoomable architecture diagram of it in the browser.
//
//	landscape                      analyze the current directory
//	landscape ./path/to/repo       analyze a local repository
//	landscape github.com/o/r       clone and analyze a public GitHub repository
//	landscape serve                only start the web UI (paste repos there)
//	landscape --json ./repo        print the landscape as JSON and exit
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mondial7/mondspace-landscape/internal/enrich"
	"github.com/mondial7/mondspace-landscape/internal/llm"
	"github.com/mondial7/mondspace-landscape/internal/pipeline"
	"github.com/mondial7/mondspace-landscape/internal/server"
	"github.com/mondial7/mondspace-landscape/internal/source"
)

var version = "dev"

const defaultPort = 7878

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "landscape:", err)
		os.Exit(1)
	}
}

type options struct {
	port         int
	noOpen       bool
	jsonOut      bool
	showVersion  bool
	llm          llm.Config
	maxAI        int
	workers      int
	cacheDir     string
	serveOnly    bool
	sourceTarget string
}

func parse(args []string, stderr io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("landscape", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, `Usage: landscape [flags] [path | github.com/owner/repo | serve]

Analyzes a repository with static analysis (and a local LLM when available)
and opens a zoomable architecture diagram in your browser.

Flags:
`)
		fs.PrintDefaults()
	}
	fs.IntVar(&o.port, "port", defaultPort, "port for the local web UI (0 = any free port)")
	fs.BoolVar(&o.noOpen, "no-open", false, "do not open the browser")
	fs.BoolVar(&o.jsonOut, "json", false, "print the landscape as JSON and exit (no web UI)")
	fs.BoolVar(&o.showVersion, "version", false, "print version and exit")
	fs.StringVar(&o.llm.Provider, "llm", "auto", "local model provider: auto, ollama, lmstudio, none")
	fs.StringVar(&o.llm.Model, "model", "", "model name (default: picked from what is installed)")
	fs.StringVar(&o.llm.OllamaURL, "ollama-url", envOr("OLLAMA_HOST", llm.DefaultOllamaURL), "Ollama base URL")
	fs.StringVar(&o.llm.OpenAIURL, "lmstudio-url", envOr("LMSTUDIO_URL", llm.DefaultOpenAIURL), "LM Studio / OpenAI-compatible base URL")
	fs.IntVar(&o.maxAI, "max-ai", 60, "maximum components described by the model (largest first)")
	fs.IntVar(&o.workers, "ai-workers", 2, "concurrent model requests")
	fs.StringVar(&o.cacheDir, "cache", defaultCache(), "where remote repositories are cloned")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	switch rest := fs.Args(); {
	case len(rest) > 1:
		return o, fmt.Errorf("expected at most one repository, got %d", len(rest))
	case len(rest) == 1 && rest[0] == "serve":
		o.serveOnly = true
	case len(rest) == 1:
		o.sourceTarget = rest[0]
	default:
		o.sourceTarget = "."
	}
	if o.llm.OllamaURL != "" && !hasScheme(o.llm.OllamaURL) {
		o.llm.OllamaURL = "http://" + o.llm.OllamaURL // OLLAMA_HOST is often host:port
	}
	return o, nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	o, err := parse(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if o.showVersion {
		fmt.Fprintf(stdout, "landscape %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return nil
	}
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	enricher, err := newEnricher(ctx, o, logger)
	if err != nil {
		return err
	}
	resolver := source.NewResolver(o.cacheDir, source.GitCLI{})
	if o.jsonOut {
		return printJSON(ctx, o, resolver, enricher, stdout)
	}

	cfg := server.Config{Resolver: resolver, Analyzer: pipeline.New(), Logger: logger}
	if enricher != nil {
		cfg.Enricher = enricher
	}
	if !o.serveOnly {
		cfg.Initial = o.sourceTarget
	}
	srv := server.New(cfg)

	ln, err := listen(o.port)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("http://%s/", ln.Addr().String())
	if id := srv.InitialID(); id != "" {
		url += "#/a/" + id
	}
	fmt.Fprintf(stdout, "landscape: %s\n", url)

	httpSrv := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- httpSrv.Serve(ln) }()
	if !o.noOpen {
		if err := openBrowser(url); err != nil {
			logger.Warn("could not open browser; open the URL manually", "err", err)
		}
	}
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return httpSrv.Shutdown(shutdownCtx)
	}
}

// newEnricher connects to a local model if one is configured and reachable.
// A missing model is not an error: the diagram is still fully useful.
func newEnricher(ctx context.Context, o options, logger *slog.Logger) (*enrich.Enricher, error) {
	client, err := llm.Detect(ctx, o.llm)
	if errors.Is(err, llm.ErrUnavailable) {
		if o.llm.Provider != "none" {
			logger.Info("no local model found; continuing with static analysis only (start Ollama or LM Studio for descriptions)")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	logger.Info("using local model for descriptions", "provider", client.Name(), "model", client.Model())
	return enrich.New(client, enrich.WithMaxComponents(o.maxAI), enrich.WithWorkers(o.workers)), nil
}

func printJSON(ctx context.Context, o options, r *source.Resolver, e *enrich.Enricher, stdout io.Writer) error {
	repo, err := r.Resolve(ctx, o.sourceTarget)
	if err != nil {
		return err
	}
	var opts []pipeline.Option
	if e != nil {
		opts = append(opts, pipeline.WithEnricher(e))
	}
	m, err := pipeline.New(opts...).Run(ctx, os.DirFS(repo.Dir), repo.Name)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"summary": m.Summary, "nodes": m.Graph.Nodes(), "edges": m.Graph.Edges()})
}

// listen binds to loopback; the default port falls forward if it is taken.
func listen(port int) (net.Listener, error) {
	attempts := 1
	if port == defaultPort {
		attempts = 20
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port+i)))
		if err == nil {
			return ln, nil
		}
		lastErr = err
		if port == 0 {
			break
		}
	}
	return nil, fmt.Errorf("listen: %w", lastErr)
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func defaultCache() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "mondspace-landscape", "repos")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func hasScheme(u string) bool {
	for i := 0; i < len(u); i++ {
		if u[i] == ':' {
			return i+2 < len(u) && u[i+1] == '/' && u[i+2] == '/'
		}
	}
	return false
}
