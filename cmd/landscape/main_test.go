package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/demo\n",
		"main.go": "package main\n\nimport \"net/http\"\n\nfunc main() { http.HandleFunc(\"GET /x\", nil); http.ListenAndServe(\":1\", nil) }\n",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestJSONMode(t *testing.T) {
	dir := writeRepo(t)
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"--json", "--llm", "none", dir}, &out, &errOut); err != nil {
		t.Fatalf("run: %v (stderr: %s)", err, errOut.String())
	}
	var doc struct {
		Summary struct{ Name string } `json:"summary"`
		Nodes   []struct{ ID string } `json:"nodes"`
	}
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if doc.Summary.Name != filepath.Base(dir) || len(doc.Nodes) < 3 {
		t.Errorf("json = %+v", doc)
	}
}

func TestServeModeStartsAndAnalyzes(t *testing.T) {
	dir := writeRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- run(ctx, []string{"--no-open", "--llm", "none", "--port", "0", dir}, pw, io.Discard)
		pw.Close()
	}()

	// The first line printed is the URL to open.
	line := make([]byte, 256)
	n, err := pr.Read(line)
	if err != nil {
		t.Fatal(err)
	}
	url := strings.TrimSpace(strings.TrimPrefix(strings.SplitN(string(line[:n]), "\n", 2)[0], "landscape: "))
	url, fragment, _ := strings.Cut(url, "#")
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.HasPrefix(fragment, "/a/") {
		t.Fatalf("first line = %q, want URL", line[:n])
	}
	go io.Copy(io.Discard, pr) //nolint:errcheck // drain remaining output

	var cfg map[string]string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "api/config")
		if err == nil {
			_ = json.NewDecoder(resp.Body).Decode(&cfg)
			resp.Body.Close()
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cfg["initial"] == "" {
		t.Fatalf("config = %v, want initial analysis id", cfg)
	}
	var status string
	for time.Now().Before(deadline) && status != "ready" {
		resp, err := http.Get(url + "api/analyses/" + cfg["initial"])
		if err != nil {
			t.Fatal(err)
		}
		var j struct{ Status, Error string }
		_ = json.NewDecoder(resp.Body).Decode(&j)
		resp.Body.Close()
		if j.Status == "failed" {
			t.Fatalf("analysis failed: %s", j.Error)
		}
		status = j.Status
		time.Sleep(20 * time.Millisecond)
	}
	if status != "ready" {
		t.Fatalf("status = %q, want ready", status)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("run returned %v after shutdown", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestBadArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	if err := run(context.Background(), []string{"--llm", "bogus", "--json", "."}, &out, &errOut); err == nil {
		t.Error("unknown llm provider should fail")
	}
	if err := run(context.Background(), []string{"a", "b"}, &out, &errOut); err == nil {
		t.Error("two sources should fail")
	}
	if err := run(context.Background(), []string{"--version"}, &out, &errOut); err != nil || !strings.Contains(out.String(), "landscape") {
		t.Errorf("--version = %q, %v", out.String(), err)
	}
}
