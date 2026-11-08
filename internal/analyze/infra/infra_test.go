package infra

import (
	"context"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

func run(t *testing.T, fsys fstest.MapFS) analyze.Facts {
	t.Helper()
	repo, err := scan.Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Analyze(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func service(t *testing.T, f analyze.Facts, name string) analyze.Service {
	t.Helper()
	for _, s := range f.Services {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("service %q not in %+v", name, f.Services)
	return analyze.Service{}
}

func TestDockerfile(t *testing.T) {
	f := run(t, fstest.MapFS{
		"Dockerfile": {Data: []byte("FROM golang:1.22 AS build\nRUN go build\nFROM gcr.io/distroless/static\nEXPOSE 8080\nVOLUME /data\nCMD [\"/app\"]\n")},
	})
	s := service(t, f, "demo")
	if s.Platform != "docker" || s.Image != "gcr.io/distroless/static" || s.BuildDir != "." {
		t.Errorf("service = %+v", s)
	}
	if !reflect.DeepEqual(s.Ports, []string{"8080"}) {
		t.Errorf("ports = %v", s.Ports)
	}
	if !reflect.DeepEqual(s.Details, []string{"build stage: golang:1.22", "volume: /data"}) {
		t.Errorf("details = %v", s.Details)
	}
}

func TestComposeServicesAndBackingStores(t *testing.T) {
	f := run(t, fstest.MapFS{
		"docker-compose.yml": {Data: []byte(`
services:
  api:
    build: ./api
    ports: ["8080:8080"]
    depends_on: [db, cache]
  db:
    image: postgres:16
  cache:
    image: redis:7-alpine
  worker:
    build:
      context: ./worker
    depends_on:
      db:
        condition: service_healthy
`)},
	})
	api := service(t, f, "api")
	if api.BuildDir != "api" || api.Platform != "docker-compose" {
		t.Errorf("api = %+v", api)
	}
	if !reflect.DeepEqual(api.DependsOn, []string{"cache", "db"}) {
		t.Errorf("api depends_on = %v", api.DependsOn)
	}
	if w := service(t, f, "worker"); w.BuildDir != "worker" || !reflect.DeepEqual(w.DependsOn, []string{"db"}) {
		t.Errorf("worker = %+v", w)
	}
	var exts []string
	for _, e := range f.Externals {
		exts = append(exts, e.ID+"="+e.Name)
	}
	if want := []string{"compose:cache=Redis", "compose:db=PostgreSQL"}; !reflect.DeepEqual(exts, want) {
		t.Errorf("externals = %v, want %v", exts, want)
	}
	if len(f.Services) != 2 {
		t.Errorf("services = %+v, want only buildable ones (api, worker)", f.Services)
	}
}

func TestFlyToml(t *testing.T) {
	f := run(t, fstest.MapFS{
		"fly.toml": {Data: []byte("app = 'mmtime'\nprimary_region = 'ams'\n\n[http_service]\n  internal_port = 8080\n  force_https = true\n\n[[mounts]]\n  source = 'data'\n  destination = '/data'\n")},
	})
	s := service(t, f, "mmtime")
	if s.Platform != "fly.io" || !reflect.DeepEqual(s.Ports, []string{"8080"}) {
		t.Errorf("fly service = %+v", s)
	}
	if !reflect.DeepEqual(s.Details, []string{"region: ams", "volume: data → /data"}) {
		t.Errorf("details = %v", s.Details)
	}
	if len(f.Externals) != 1 || f.Externals[0].ID != "fly.io" {
		t.Errorf("externals = %v, want fly.io hosting", f.Externals)
	}
}

func TestBrokenComposeIsAWarning(t *testing.T) {
	f := run(t, fstest.MapFS{"compose.yaml": {Data: []byte("services: [\n")}})
	if len(f.Warnings) != 1 {
		t.Errorf("warnings = %v, want 1", f.Warnings)
	}
}
