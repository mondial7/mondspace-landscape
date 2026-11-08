package scan

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func TestLanguage(t *testing.T) {
	tests := []struct{ path, want string }{
		{"main.go", "go"},
		{"web/app.js", "javascript"},
		{"web/app.mjs", "javascript"},
		{"src/App.tsx", "typescript"},
		{"x.ts", "typescript"},
		{"tool.py", "python"},
		{"App/View.swift", "swift"},
		{"templates/index.html", "html"},
		{"static/style.css", "css"},
		{"Dockerfile", "dockerfile"},
		{"deploy/api.Dockerfile", "dockerfile"},
		{"docker-compose.yml", "yaml"},
		{"fly.toml", "toml"},
		{"README.md", "markdown"},
		{"logo.png", ""},
	}
	for _, tt := range tests {
		if got := Language(tt.path); got != tt.want {
			t.Errorf("Language(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestScanSkipsNoiseAndCountsLines(t *testing.T) {
	fsys := fstest.MapFS{
		"main.go":                      {Data: []byte("package main\n\nfunc main() {}\n")},
		"internal/store/store.go":      {Data: []byte("package store\n")},
		"web/app.js":                   {Data: []byte("console.log(1)\nconsole.log(2)\n")},
		"logo.png":                     {Data: []byte{0x89, 'P', 'N', 'G'}},
		".git/HEAD":                    {Data: []byte("ref")},
		"node_modules/x/index.js":      {Data: []byte("x")},
		"vendor/y/y.go":                {Data: []byte("package y")},
		"dist/bundle.js":               {Data: []byte("x")},
		"generated/skip.go":            {Data: []byte("package g")},
		"internal/store/store_test.go": {Data: []byte("package store\n")},
		".gitignore":                   {Data: []byte("# comment\ngenerated/\n*.log\n")},
		"debug.log":                    {Data: []byte("noise")},
	}
	repo, err := Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range repo.Files {
		got = append(got, f.Path)
	}
	want := []string{"internal/store/store.go", "internal/store/store_test.go", "main.go", "web/app.js"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %v, want %v", got, want)
	}
	if f, _ := repo.File("main.go"); f.Lines != 3 || f.Language != "go" {
		t.Errorf("main.go = %+v, want 3 lines of go", f)
	}
	if f, _ := repo.File("internal/store/store_test.go"); !f.Test {
		t.Errorf("store_test.go should be flagged as test")
	}
	if got := repo.Languages(); !reflect.DeepEqual(got, map[string]int{"go": 3, "javascript": 1}) {
		t.Errorf("Languages() = %v", got)
	}
}

func TestReadReturnsContent(t *testing.T) {
	fsys := fstest.MapFS{"a.go": {Data: []byte("package a")}}
	repo, err := Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	b, err := repo.Read("a.go")
	if err != nil || string(b) != "package a" {
		t.Errorf("Read(a.go) = %q, %v", b, err)
	}
}

func TestIsTest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"x_test.go", true},
		{"src/a.test.ts", true},
		{"src/a.spec.js", true},
		{"tests/test_api.py", true},
		{"AppTests/FooTests.swift", true},
		{"GiftsKit/Tests/GiftsKitTests/TestSupport.swift", true},
		{"App/UITests/Flow.swift", true},
		{"src/__tests__/helpers.js", true},
		{"tests/conftest.py", true},
		{"main.go", false},
		{"contest.go", false},
		{"internal/attestation/a.go", false},
	}
	for _, tt := range tests {
		if got := IsTest(tt.path); got != tt.want {
			t.Errorf("IsTest(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
