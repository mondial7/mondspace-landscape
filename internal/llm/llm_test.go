package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func ollamaServer(t *testing.T, models []string, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			var out struct {
				Models []map[string]string `json:"models"`
			}
			for _, m := range models {
				out.Models = append(out.Models, map[string]string{"name": m})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/api/generate":
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Errorf("decode: %v", err)
			}
			if req["stream"] != false || req["think"] != false {
				t.Errorf("request = %v, want stream=false think=false", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": reply})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func openAIServer(t *testing.T, models []string, reply string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			var out struct {
				Data []map[string]string `json:"data"`
			}
			for _, m := range models {
				out.Data = append(out.Data, map[string]string{"id": m})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/v1/chat/completions":
			var req struct {
				Model           string `json:"model"`
				Messages        []struct{ Role, Content string }
				ReasoningEffort string `json:"reasoning_effort"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Messages) == 0 {
				t.Errorf("bad request: %v %+v", err, req)
			}
			if req.ReasoningEffort != "none" {
				t.Errorf("reasoning_effort = %q, want none (thinking models are 70x slower)", req.ReasoningEffort)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []any{map[string]any{"message": map[string]string{"content": reply}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestOllamaComplete(t *testing.T) {
	srv := ollamaServer(t, []string{"qwen3:8b"}, "<think>hmm</think>\n  Stores sessions.  ")
	c := NewOllama(srv.URL, "qwen3:8b", nil)
	got, err := c.Complete(context.Background(), "describe")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Stores sessions." {
		t.Errorf("Complete = %q, want think block stripped and trimmed", got)
	}
}

func TestOpenAICompatibleComplete(t *testing.T) {
	srv := openAIServer(t, []string{"qwen/qwen3.5-9b"}, "Serves HTTP.")
	c := NewOpenAI(srv.URL, "qwen/qwen3.5-9b", nil)
	got, err := c.Complete(context.Background(), "describe")
	if err != nil || got != "Serves HTTP." {
		t.Errorf("Complete = %q, %v", got, err)
	}
}

func TestCompleteReportsHTTPErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "model not found", http.StatusNotFound)
	}))
	defer srv.Close()
	for _, c := range []Client{NewOllama(srv.URL, "m", nil), NewOpenAI(srv.URL, "m", nil)} {
		if _, err := c.Complete(context.Background(), "x"); err == nil || !strings.Contains(err.Error(), "404") {
			t.Errorf("%s: err = %v, want 404 error", c.Name(), err)
		}
	}
}

func TestDetect(t *testing.T) {
	ollama := ollamaServer(t, []string{"nomic-embed-text:latest", "llama3.2:3b", "qwen3:8b"}, "")
	lmstudio := openAIServer(t, []string{"text-embedding-nomic", "google/gemma-4-e4b"}, "")
	down := "http://127.0.0.1:1" // nothing listens here

	tests := []struct {
		name      string
		cfg       Config
		wantName  string
		wantModel string
		wantErr   bool
	}{
		{"ollama first, skips embedding models, prefers listed", Config{Provider: "auto", OllamaURL: ollama.URL, OpenAIURL: lmstudio.URL}, "ollama", "qwen3:8b", false},
		{"falls back to lm studio", Config{Provider: "auto", OllamaURL: down, OpenAIURL: lmstudio.URL}, "openai", "google/gemma-4-e4b", false},
		{"explicit provider and model", Config{Provider: "lmstudio", OpenAIURL: lmstudio.URL, Model: "my-model"}, "openai", "my-model", false},
		{"explicit ollama", Config{Provider: "ollama", OllamaURL: ollama.URL}, "ollama", "qwen3:8b", false},
		{"nothing available", Config{Provider: "auto", OllamaURL: down, OpenAIURL: down}, "", "", true},
		{"disabled", Config{Provider: "none"}, "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := Detect(context.Background(), tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Detect err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if c.Name() != tt.wantName || c.Model() != tt.wantModel {
				t.Errorf("Detect = %s/%s, want %s/%s", c.Name(), c.Model(), tt.wantName, tt.wantModel)
			}
		})
	}
}

func TestCompleteRetriesTransientServerErrors(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			http.Error(w, "model loading", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": "ok"}}},
		})
	}))
	defer srv.Close()
	c := NewOpenAI(srv.URL, "m", nil)
	c.retryDelay = 0
	got, err := c.Complete(context.Background(), "x")
	if err != nil || got != "ok" || calls != 2 {
		t.Errorf("Complete = %q, %v after %d calls; want ok after one retry", got, err, calls)
	}
}
