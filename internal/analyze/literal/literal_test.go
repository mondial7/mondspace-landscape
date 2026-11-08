package literal

import (
	"context"
	"reflect"
	"sort"
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
