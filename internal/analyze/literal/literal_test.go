package literal

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

func TestHostsAndSQLAreAttributedToFiles(t *testing.T) {
	fsys := fstest.MapFS{
		"internal/gh/client.go": {Data: []byte(`const base = "https://api.github.com/repos"`)},
		"web/index.html":        {Data: []byte(`<script src="https://unpkg.com/htmx.org@2.0.0"></script>`)},
		"store/store.go":        {Data: []byte("db.Exec(`INSERT INTO subs (a) VALUES (?)`)\ndb.Query(\"SELECT * FROM users\")")},
		"store/store_test.go":   {Data: []byte(`x := "https://api.stripe.com"`)},
		"README.md":             {Data: []byte("see https://api.openai.com")},
	}
	repo, err := scan.Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Analyze(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var uses []string
	for _, u := range f.Uses {
		uses = append(uses, u.File+"->"+u.External+":"+string(u.Kind))
	}
	sort.Strings(uses)
	want := []string{"internal/gh/client.go->host:api.github.com:http", "web/index.html->host:unpkg.com:http"}
	if !reflect.DeepEqual(uses, want) {
		t.Errorf("uses = %v, want %v", uses, want)
	}
	if len(f.Externals) != 2 || f.Externals[0].Kind != analyze.ExtHTTP {
		t.Errorf("externals = %+v", f.Externals)
	}
	if len(f.Data) != 1 || f.Data[0].File != "store/store.go" ||
		!reflect.DeepEqual(f.Data[0].Reads, []string{"users"}) || !reflect.DeepEqual(f.Data[0].Writes, []string{"subs"}) {
		t.Errorf("data = %+v", f.Data)
	}
}

func TestManyHostsInOneFileAreGrouped(t *testing.T) {
	src := `var feeds = []string{"https://a.example.net/rss", "https://feeds.one.org/x", "https://two.org/r",
		"https://three.org/r", "https://four.org/r", "https://five.org/r", "https://six.org/r"}
	const api = "https://api.mistral.ai/v1"`
	fsys := fstest.MapFS{"internal/feeds/sources.go": {Data: []byte(src)}, "internal/llm/llm.go": {Data: []byte(`u := "https://api.mistral.ai/v1"`)}}
	repo, err := scan.Scan(fsys, "demo")
	if err != nil {
		t.Fatal(err)
	}
	f, err := New().Analyze(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	var exts []string
	for _, e := range f.Externals {
		exts = append(exts, e.ID+"="+e.Name)
	}
	sort.Strings(exts)
	want := []string{"host:api.mistral.ai=api.mistral.ai", "hosts:internal/feeds/sources.go=7 web hosts (sources.go)"}
	if !reflect.DeepEqual(exts, want) {
		t.Errorf("externals = %v, want %v", exts, want)
	}
	for _, u := range f.Uses {
		if u.External == "hosts:internal/feeds/sources.go" && !strings.Contains(u.Label, "feeds.one.org") {
			t.Errorf("grouped use label = %q, want the host list", u.Label)
		}
	}
}
