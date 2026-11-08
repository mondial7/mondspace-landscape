// Package llm talks to local language models served by Ollama or by any
// OpenAI-compatible server (LM Studio, llama.cpp server, vLLM, …).
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Client completes prompts with a local model.
type Client interface {
	Complete(ctx context.Context, prompt string) (string, error)
	Name() string
	Model() string
}

// ErrUnavailable means no usable local model server was found.
var ErrUnavailable = errors.New("no local LLM available")

// Defaults for local model servers.
const (
	DefaultOllamaURL = "http://localhost:11434"
	DefaultOpenAIURL = "http://localhost:1234" // LM Studio
)

var thinkBlock = regexp.MustCompile(`(?s)<think>.*?</think>`)

// clean strips reasoning blocks emitted by "thinking" models.
func clean(s string) string {
	return strings.TrimSpace(thinkBlock.ReplaceAllString(s, ""))
}

func defaultHTTP(c *http.Client) *http.Client {
	if c != nil {
		return c
	}
	return &http.Client{Timeout: 3 * time.Minute}
}

// postJSON sends body as JSON and decodes the JSON response into out.
func postJSON(ctx context.Context, hc *http.Client, url string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return do(hc, req, out)
}

func getJSON(ctx context.Context, hc *http.Client, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	return do(hc, req, out)
}

// statusError is a non-200 HTTP response.
type statusError struct {
	method, path string
	code         int
	body         string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: %d %s", e.method, e.path, e.code, e.body)
}

func do(hc *http.Client, req *http.Request, out any) error {
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &statusError{req.Method, req.URL.Path, resp.StatusCode, firstLine(strings.TrimSpace(string(msg)))}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// defaultRetryDelay is the pause before retrying a 5xx (e.g. a model that
// is still loading).
const defaultRetryDelay = 3 * time.Second

// withRetry runs fn, retrying once after a server-side (5xx) error.
func withRetry(ctx context.Context, delay time.Duration, fn func() error) error {
	err := fn()
	var se *statusError
	if !errors.As(err, &se) || se.code < 500 {
		return err
	}
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return err
	}
	return fn()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Ollama is a client for the Ollama API.
type Ollama struct {
	baseURL, model string
	http           *http.Client
	retryDelay     time.Duration
}

// NewOllama returns an Ollama client (hc may be nil).
func NewOllama(baseURL, model string, hc *http.Client) *Ollama {
	return &Ollama{baseURL: strings.TrimSuffix(baseURL, "/"), model: model, http: defaultHTTP(hc), retryDelay: defaultRetryDelay}
}

// Name implements Client.
func (o *Ollama) Name() string { return "ollama" }

// Model implements Client.
func (o *Ollama) Model() string { return o.model }

// Complete implements Client.
func (o *Ollama) Complete(ctx context.Context, prompt string) (string, error) {
	var out struct {
		Response string `json:"response"`
	}
	err := withRetry(ctx, o.retryDelay, func() error {
		return postJSON(ctx, o.http, o.baseURL+"/api/generate", map[string]any{
			"model": o.model, "prompt": prompt, "stream": false, "think": false,
			"options": map[string]any{"temperature": 0.2},
		}, &out)
	})
	if err != nil {
		return "", fmt.Errorf("ollama: %w", err)
	}
	return clean(out.Response), nil
}

func (o *Ollama) models(ctx context.Context) ([]string, error) {
	var out struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := getJSON(ctx, o.http, o.baseURL+"/api/tags", &out); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(out.Models))
	for _, m := range out.Models {
		names = append(names, m.Name)
	}
	return names, nil
}

// OpenAI is a client for OpenAI-compatible chat completion servers.
type OpenAI struct {
	baseURL, model string
	http           *http.Client
	retryDelay     time.Duration
}

// NewOpenAI returns an OpenAI-compatible client (hc may be nil).
func NewOpenAI(baseURL, model string, hc *http.Client) *OpenAI {
	return &OpenAI{baseURL: strings.TrimSuffix(strings.TrimSuffix(baseURL, "/"), "/v1"), model: model, http: defaultHTTP(hc), retryDelay: defaultRetryDelay}
}

// Name implements Client.
func (o *OpenAI) Name() string { return "openai" }

// Model implements Client.
func (o *OpenAI) Model() string { return o.model }

// Complete implements Client.
func (o *OpenAI) Complete(ctx context.Context, prompt string) (string, error) {
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	err := withRetry(ctx, o.retryDelay, func() error {
		return postJSON(ctx, o.http, o.baseURL+"/v1/chat/completions", map[string]any{
			"model":       o.model,
			"messages":    []map[string]string{{"role": "user", "content": prompt}},
			"temperature": 0.2,
			// Short descriptions need no chain of thought; thinking models
			// otherwise spend minutes reasoning per answer.
			"reasoning_effort": "none",
		}, &out)
	})
	if err != nil {
		return "", fmt.Errorf("openai-compatible: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("openai-compatible: empty response")
	}
	return clean(out.Choices[0].Message.Content), nil
}

func (o *OpenAI) models(ctx context.Context) ([]string, error) {
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := getJSON(ctx, o.http, o.baseURL+"/v1/models", &out); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(out.Data))
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// Config selects a provider and model.
type Config struct {
	Provider  string // "auto", "ollama", "lmstudio"/"openai", "none"
	Model     string // empty: pick automatically
	OllamaURL string
	OpenAIURL string
	HTTP      *http.Client
}

// preferredModels are tried in order when no model is configured: small,
// instruction-following models that run well on a laptop.
var preferredModels = []string{"qwen3", "qwen2.5", "gemma", "llama3", "mistral", "phi"}

// Detect finds a reachable local model server and picks a model.
func Detect(ctx context.Context, cfg Config) (Client, error) {
	if cfg.OllamaURL == "" {
		cfg.OllamaURL = DefaultOllamaURL
	}
	if cfg.OpenAIURL == "" {
		cfg.OpenAIURL = DefaultOpenAIURL
	}
	probe := &http.Client{Timeout: 2 * time.Second}
	type candidate struct {
		list  func(context.Context) ([]string, error)
		build func(model string) Client
	}
	ollama := candidate{
		list:  NewOllama(cfg.OllamaURL, "", probe).models,
		build: func(m string) Client { return NewOllama(cfg.OllamaURL, m, cfg.HTTP) },
	}
	openai := candidate{
		list:  NewOpenAI(cfg.OpenAIURL, "", probe).models,
		build: func(m string) Client { return NewOpenAI(cfg.OpenAIURL, m, cfg.HTTP) },
	}
	var order []candidate
	switch strings.ToLower(cfg.Provider) {
	case "", "auto":
		order = []candidate{ollama, openai}
	case "ollama":
		order = []candidate{ollama}
	case "lmstudio", "openai":
		order = []candidate{openai}
	case "none", "off":
		return nil, fmt.Errorf("%w: disabled", ErrUnavailable)
	default:
		return nil, fmt.Errorf("unknown LLM provider %q", cfg.Provider)
	}
	var errs []error
	for _, c := range order {
		models, err := c.list(ctx)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		model := cfg.Model
		if model == "" {
			model = pickModel(models)
		}
		if model != "" {
			return c.build(model), nil
		}
		errs = append(errs, errors.New("server has no chat models"))
	}
	return nil, fmt.Errorf("%w: %v", ErrUnavailable, errors.Join(errs...))
}

func pickModel(models []string) string {
	var chat []string
	for _, m := range models {
		if !strings.Contains(strings.ToLower(m), "embed") {
			chat = append(chat, m)
		}
	}
	for _, pref := range preferredModels {
		for _, m := range chat {
			if strings.Contains(strings.ToLower(m), pref) {
				return m
			}
		}
	}
	if len(chat) > 0 {
		return chat[0]
	}
	return ""
}
