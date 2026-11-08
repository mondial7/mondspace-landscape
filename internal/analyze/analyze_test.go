package analyze

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

type fakeAnalyzer struct {
	name  string
	facts Facts
	err   error
}

func (f fakeAnalyzer) Name() string { return f.name }
func (f fakeAnalyzer) Analyze(context.Context, *scan.Repo) (Facts, error) {
	return f.facts, f.err
}

func TestRunMergesFactsAndDedupsExternals(t *testing.T) {
	a := fakeAnalyzer{name: "a", facts: Facts{
		Units:     []Unit{{ID: "go:store", Name: "store"}},
		Externals: []External{{ID: "sqlite", Name: "SQLite", Kind: ExtDatabase}},
		Uses:      []Use{{Unit: "go:store", External: "sqlite", Kind: graph.EdgeData}},
	}}
	b := fakeAnalyzer{name: "b", facts: Facts{
		Externals: []External{{ID: "sqlite", Name: "SQLite", Kind: ExtDatabase}, {ID: "api.github.com", Kind: ExtHTTP}},
		Calls:     []Call{{File: "web/app.js", Method: "GET", Path: "/api/x"}},
	}}
	got, err := Run(context.Background(), &scan.Repo{}, a, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Units) != 1 || len(got.Uses) != 1 || len(got.Calls) != 1 {
		t.Errorf("merged facts = %+v", got)
	}
	if len(got.Externals) != 2 {
		t.Errorf("externals = %v, want 2 deduplicated", got.Externals)
	}
}

func TestRunReportsAnalyzerErrorsButKeepsOthers(t *testing.T) {
	ok := fakeAnalyzer{name: "ok", facts: Facts{Units: []Unit{{ID: "u"}}}}
	bad := fakeAnalyzer{name: "bad", err: errors.New("boom")}
	got, err := Run(context.Background(), &scan.Repo{}, ok, bad)
	if err == nil {
		t.Error("Run with failing analyzer = nil error, want error")
	}
	if len(got.Units) != 1 {
		t.Errorf("facts from healthy analyzer lost: %+v", got)
	}
	if len(got.Warnings) != 1 {
		t.Errorf("warnings = %v, want 1", got.Warnings)
	}
}

func TestLookupCatalog(t *testing.T) {
	tests := []struct {
		lang, imp string
		wantID    string
		wantOK    bool
	}{
		{"go", "modernc.org/sqlite", "sqlite", true},
		{"go", "github.com/mattn/go-sqlite3", "sqlite", true},
		{"go", "github.com/jackc/pgx/v5/pgxpool", "postgres", true},
		{"go", "github.com/aws/aws-sdk-go-v2/service/s3", "aws", true},
		{"go", "net/smtp", "smtp", true},
		{"go", "fmt", "", false},
		{"javascript", "@anthropic-ai/sdk", "anthropic", true},
		{"typescript", "pg", "postgres", true},
		{"typescript", "pgx", "", false},
		{"python", "boto3", "aws", true},
		{"python", "sqlalchemy.orm", "sql", true},
		{"swift", "CloudKit", "icloud", true},
		{"swift", "SwiftUI", "", false},
	}
	for _, tt := range tests {
		ext, ok := Lookup(tt.lang, tt.imp)
		if ok != tt.wantOK || ext.ID != tt.wantID {
			t.Errorf("Lookup(%s, %s) = %q,%v want %q,%v", tt.lang, tt.imp, ext.ID, ok, tt.wantID, tt.wantOK)
		}
	}
}

func TestExtractURLs(t *testing.T) {
	src := `
	const a = "https://api.stripe.com/v1/charges"
	resp, _ := http.Get("https://api.github.com/repos/" + name)
	// see https://docs.example.org (comment, not a literal)
	x := 'http://localhost:8080/x'
	y := "https://www.w3.org/2000/svg"
	z := "https://api.github.com/users"
	t := "https://{{.Host}}/x"
	u := "https://${host}/y"
	`
	got := ExtractHosts([]byte(src))
	want := []string{"api.github.com", "api.stripe.com"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ExtractHosts = %v, want %v", got, want)
	}
}

func TestExtractSQLTables(t *testing.T) {
	src := "db.Exec(`CREATE TABLE IF NOT EXISTS subs (id INTEGER)`)\n" +
		`db.Query("SELECT id, name FROM subs WHERE id = ?")` + "\n" +
		`db.Exec("INSERT INTO events (a) VALUES (?)")` + "\n" +
		`db.Exec("UPDATE users SET x = 1")` + "\n" +
		`db.Exec("DELETE FROM sessions WHERE t < ?")` + "\n" +
		`fmt.Println("select from the menu")` + "\n"
	reads, writes := ExtractSQLTables([]byte(src))
	if want := []string{"subs"}; !reflect.DeepEqual(reads, want) {
		t.Errorf("reads = %v, want %v", reads, want)
	}
	if want := []string{"events", "sessions", "subs", "users"}; !reflect.DeepEqual(writes, want) {
		t.Errorf("writes = %v, want %v", writes, want)
	}
}

func TestNormalizeRoute(t *testing.T) {
	tests := []struct{ in, method, path string }{
		{"/api/items", "", "/api/items"},
		{"GET /api/items/{id}", "GET", "/api/items/{id}"},
		{"POST /api/items/", "POST", "/api/items"},
		{"/", "", "/"},
	}
	for _, tt := range tests {
		m, p := SplitRoute(tt.in)
		if m != tt.method || p != tt.path {
			t.Errorf("SplitRoute(%q) = %q %q, want %q %q", tt.in, m, p, tt.method, tt.path)
		}
	}
}

func TestMatchRoute(t *testing.T) {
	routes := []string{"/", "GET /api/items", "GET /api/items/{id}", "/static/"}
	tests := []struct{ method, path, want string }{
		{"GET", "/api/items", "GET /api/items"},
		{"GET", "/api/items/42", "GET /api/items/{id}"},
		{"GET", "/api/items/${id}", "GET /api/items/{id}"},
		{"", "/static/app.js", "/static/"},
		{"POST", "/unknown", "/"},
	}
	for _, tt := range tests {
		if got, _ := MatchRoute(routes, tt.method, tt.path); got != tt.want {
			t.Errorf("MatchRoute(%s %s) = %q, want %q", tt.method, tt.path, got, tt.want)
		}
	}
}

func TestExtractSQLTablesIgnoresProse(t *testing.T) {
	src := "// It selects the components from their containers and updates them.\n" +
		"/* insert into notes when you delete from it */\n" +
		"msg := \"Select a box from it to see details\"\n" +
		"q := `select id from sessions where x = 1`\n"
	reads, writes := ExtractSQLTables([]byte(src))
	if want := []string{"sessions"}; !reflect.DeepEqual(reads, want) || len(writes) != 0 {
		t.Errorf("reads = %v, writes = %v; want only [sessions] read from the string literal", reads, writes)
	}
}
