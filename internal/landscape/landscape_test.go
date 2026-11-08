package landscape

import (
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// shopFacts models a Go web app with an admin CLI, embedded templates, an
// SQLite store, a GitHub API client and a fly.io deployment.
func shopFacts() ([]scan.File, analyze.Facts) {
	files := []scan.File{
		{Path: "main.go", Language: "go", Lines: 40},
		{Path: "cmd/admin/main.go", Language: "go", Lines: 20},
		{Path: "internal/server/server.go", Language: "go", Lines: 200},
		{Path: "internal/store/store.go", Language: "go", Lines: 120},
		{Path: "internal/store/store_test.go", Language: "go", Lines: 60, Test: true},
		{Path: "internal/gh/gh.go", Language: "go", Lines: 30},
		{Path: "web/templates/index.html", Language: "html", Lines: 50},
		{Path: "web/static/app.js", Language: "javascript", Lines: 10},
		{Path: "Dockerfile", Language: "dockerfile", Lines: 8},
	}
	facts := analyze.Facts{
		Units: []analyze.Unit{
			{ID: "go:.", Name: "shop", Dir: ".", Language: "go", Files: []string{"main.go"}, Entry: true, Tags: []string{"http-server"}},
			{ID: "go:cmd/admin", Name: "admin", Dir: "cmd/admin", Language: "go", Files: []string{"cmd/admin/main.go"}, Entry: true, Tags: []string{"cli"}},
			{ID: "go:internal/server", Name: "server", Dir: "internal/server", Language: "go", Files: []string{"internal/server/server.go"},
				Routes: []string{"GET /items", "POST /items/{id}/buy", "GET /static/"}, Tags: []string{"http-server", "templates"}, Doc: "Package server exposes the HTTP API."},
			{ID: "go:internal/store", Name: "store", Dir: "internal/store", Language: "go", Files: []string{"internal/store/store.go", "internal/store/store_test.go"}},
			{ID: "go:internal/gh", Name: "gh", Dir: "internal/gh", Language: "go", Files: []string{"internal/gh/gh.go"}},
			{ID: "web:web/templates", Name: "templates", Dir: "web/templates", Language: "html", Files: []string{"web/templates/index.html"}, Tags: []string{"templates", "ui"}},
			{ID: "js:web/static", Name: "static", Dir: "web/static", Language: "javascript", Files: []string{"web/static/app.js"}, Root: "web", Tags: []string{"ui"}},
		},
		Deps: []analyze.Dep{
			{From: "go:.", To: "go:internal/server", Kind: graph.EdgeDepends},
			{From: "go:.", To: "go:internal/store", Kind: graph.EdgeDepends},
			{From: "go:internal/server", To: "go:internal/store", Kind: graph.EdgeDepends},
			{From: "go:internal/server", To: "go:internal/gh", Kind: graph.EdgeDepends},
			{From: "go:cmd/admin", To: "go:internal/store", Kind: graph.EdgeDepends},
		},
		Externals: []analyze.External{
			{ID: "sqlite", Name: "SQLite", Kind: analyze.ExtDatabase},
			{ID: "host:api.github.com", Name: "api.github.com", Kind: analyze.ExtHTTP},
			{ID: "fly.io", Name: "Fly.io", Kind: analyze.ExtHosting},
		},
		Uses: []analyze.Use{
			{Unit: "go:internal/store", External: "sqlite", Kind: graph.EdgeData},
			{File: "internal/gh/gh.go", External: "host:api.github.com", Kind: graph.EdgeHTTP},
		},
		Calls: []analyze.Call{
			{File: "web/templates/index.html", Method: "POST", Path: "/items/{{.ID}}/buy"},
			{File: "web/static/app.js", Method: "", Path: "/items"},
			{File: "web/templates/index.html", Method: "GET", Path: "/static/app.js"},
		},
		Data: []analyze.DataAccess{
			{File: "internal/store/store.go", Reads: []string{"items"}, Writes: []string{"items", "orders"}},
		},
		Refs: []analyze.Ref{
			{From: "go:.", Path: "web/templates/*.html", Kind: graph.EdgeDepends, Label: "embeds"},
			{From: "go:.", Path: "web/static", Kind: graph.EdgeDepends, Label: "embeds"},
		},
		Services: []analyze.Service{
			{Name: "shop", Source: "fly.toml", Platform: "fly.io", BuildDir: ".", Ports: []string{"8080"}, Details: []string{"region: ams"}},
			{Name: "shop", Source: "Dockerfile", Platform: "docker", BuildDir: ".", Image: "gcr.io/distroless/static", Ports: []string{"8080"}},
		},
	}
	return files, facts
}

func build(t *testing.T) *Model {
	t.Helper()
	files, facts := shopFacts()
	m, err := Build(Input{Name: "shop", Files: files, Facts: facts, Readme: "# Shop\n\nA tiny shop that sells items.\n\nMore text."})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func parentOf(t *testing.T, m *Model, id string) string {
	t.Helper()
	n, ok := m.Graph.Node(id)
	if !ok {
		t.Fatalf("node %q missing", id)
	}
	return n.Parent
}

func TestSystemNodeAndSummary(t *testing.T) {
	m := build(t)
	sys, ok := m.Graph.Node(SystemID)
	if !ok || sys.Kind != graph.KindSystem || sys.Name != "shop" {
		t.Fatalf("system = %+v", sys)
	}
	if sys.Description != "A tiny shop that sells items." {
		t.Errorf("system description = %q, want README first paragraph", sys.Description)
	}
	if m.Summary.Files != 9 || m.Summary.Lines != 538 || m.Summary.Languages["go"] != 6 {
		t.Errorf("summary = %+v", m.Summary)
	}
}

func TestContainersFromEntryPointsAndClosure(t *testing.T) {
	m := build(t)
	tests := []struct{ component, container string }{
		{"cmp:.", "ctr:."},
		{"cmp:internal/server", "ctr:."},
		{"cmp:internal/gh", "ctr:."},
		{"cmp:web/templates", "ctr:."}, // embedded into the binary
		{"cmp:web/static", "ctr:."},
		{"cmp:cmd/admin", "ctr:cmd/admin"},
		{"cmp:internal/store", "ctr:shared"}, // imported by both binaries
	}
	for _, tt := range tests {
		if got := parentOf(t, m, tt.component); got != tt.container {
			t.Errorf("parent(%s) = %q, want %q", tt.component, got, tt.container)
		}
	}
	web, _ := m.Graph.Node("ctr:.")
	if web.Name != "shop" || !strings.Contains(web.Technology, "Go") || !hasTag(web, "http-server") {
		t.Errorf("web container = %+v", web)
	}
	if web.Details["deploy"] != "docker (gcr.io/distroless/static) · fly.io · ports 8080 · region: ams" {
		t.Errorf("deploy details = %q", web.Details["deploy"])
	}
	admin, _ := m.Graph.Node("ctr:cmd/admin")
	if !hasTag(admin, "cli") {
		t.Errorf("admin tags = %v, want cli", admin.Tags)
	}
	if got := parentOf(t, m, "ctr:."); got != SystemID {
		t.Errorf("container parent = %q, want system", got)
	}
}

func TestFilesBelongToComponents(t *testing.T) {
	m := build(t)
	f, ok := m.Graph.Node("file:internal/store/store_test.go")
	if !ok || f.Parent != "cmp:internal/store" || f.Metrics["lines"] != 60 || !hasTag(f, "test") {
		t.Errorf("test file node = %+v", f)
	}
	if _, ok := m.Graph.Node("file:Dockerfile"); ok {
		t.Error("infra files without a unit should not become code nodes")
	}
	store, _ := m.Graph.Node("cmp:internal/store")
	if store.Metrics["files"] != 2 || store.Metrics["lines"] != 180 {
		t.Errorf("store metrics = %v", store.Metrics)
	}
}

func TestEdges(t *testing.T) {
	m := build(t)
	got := map[string]bool{}
	for _, e := range m.Graph.Edges() {
		got[e.From+" -"+string(e.Kind)+"-> "+e.To+" "+e.Label] = true
	}
	want := []string{
		"cmp:. -depends-> cmp:internal/server ",
		"cmp:internal/server -depends-> cmp:internal/gh ",
		"cmp:. -depends-> cmp:web/templates embeds",
		"cmp:web/templates -http-> cmp:internal/server POST /items/{id}/buy",
		"cmp:web/static -http-> cmp:internal/server GET /items",
		"cmp:web/templates -http-> cmp:internal/server GET /static/",
		"cmp:internal/store -data-> ext:sqlite reads items",
		"cmp:internal/store -data-> ext:sqlite writes items, orders",
		"file:internal/gh/gh.go -http-> ext:host:api.github.com ",
		"ctr:. -runtime-> ext:fly.io deployed to",
		"person:user -uses-> cmp:web/templates uses (browser)",
		"person:user -uses-> cmp:web/static uses (browser)",
		"person:operator -uses-> cmp:cmd/admin runs",
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing edge %q", w)
		}
	}
	if got["cmp:internal/store -data-> ext:sqlite "] {
		t.Error("unlabelled db edge should be replaced by labelled data-flow edges")
	}
	if t.Failed() {
		var all []string
		for k := range got {
			all = append(all, k)
		}
		sort.Strings(all)
		t.Logf("edges:\n%s", strings.Join(all, "\n"))
	}
}

func TestTopLevelViewShowsContext(t *testing.T) {
	m := build(t)
	v, err := m.Graph.View("")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, n := range v.Nodes {
		ids = append(ids, n.ID)
	}
	sort.Strings(ids)
	want := []string{"ext:fly.io", "ext:host:api.github.com", "ext:sqlite", "person:operator", "person:user", SystemID}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("top level = %v, want %v", ids, want)
	}
}

func TestDataAccessWithoutKnownDriverUsesGenericDatabase(t *testing.T) {
	m, err := Build(Input{
		Name:  "x",
		Files: []scan.File{{Path: "db/q.py", Language: "python", Lines: 3}},
		Facts: analyze.Facts{
			Units: []analyze.Unit{{ID: "py:db", Name: "db", Dir: "db", Language: "python", Files: []string{"db/q.py"}, Root: "db"}},
			Data:  []analyze.DataAccess{{File: "db/q.py", Reads: []string{"t"}}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if n, ok := m.Graph.Node("ext:sql"); !ok || n.Kind != graph.KindExternal {
		t.Fatalf("generic database node missing: %+v", n)
	}
}

func TestComposeDependenciesBecomeRuntimeEdges(t *testing.T) {
	m, err := Build(Input{
		Name:  "x",
		Files: []scan.File{{Path: "api/main.go", Language: "go", Lines: 3}},
		Facts: analyze.Facts{
			Units:     []analyze.Unit{{ID: "go:api", Name: "api", Dir: "api", Language: "go", Files: []string{"api/main.go"}, Entry: true}},
			Externals: []analyze.External{{ID: "compose:db", Name: "PostgreSQL", Kind: analyze.ExtDatabase}},
			Services: []analyze.Service{
				{Name: "api", Platform: "docker-compose", BuildDir: "api", DependsOn: []string{"db", "worker"}},
				{Name: "worker", Platform: "docker-compose", Image: "acme/worker:1"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, e := range m.Graph.Edges() {
		got[e.From+"->"+e.To] = true
	}
	for _, w := range []string{"ctr:api->ext:compose:db", "ctr:api->ctr:svc:worker"} {
		if !got[w] {
			t.Errorf("missing runtime edge %s in %v", w, got)
		}
	}
	if n, _ := m.Graph.Node("ctr:svc:worker"); n.Technology != "acme/worker:1" {
		t.Errorf("image-only service container = %+v", n)
	}
}

func hasTag(n graph.Node, tag string) bool {
	for _, t := range n.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

func TestReadmeSummaryStripsMarkdown(t *testing.T) {
	got := readmeSummary("# T\n\nA **small** tool for `go` and [docs](https://x.y).\n")
	if want := "A small tool for go and docs."; got != want {
		t.Errorf("readmeSummary = %q, want %q", got, want)
	}
}

// appFacts models an iOS app: an @main entry in an Xcode target with sibling
// folders (same module, no imports between them), a SwiftPM library and its tests.
func appFacts() Input {
	sw := func(id, dir, root string, entry bool, files ...string) analyze.Unit {
		return analyze.Unit{ID: id, Name: path.Base(dir), Dir: dir, Language: "swift", Root: root, Entry: entry, Files: files, Tags: []string{"ui"}}
	}
	units := []analyze.Unit{
		sw("swift:App/Sources/App", "App/Sources/App", "App", true, "App/Sources/App/App.swift"),
		sw("swift:App/Sources/UI", "App/Sources/UI", "App", false, "App/Sources/UI/List.swift"),
		sw("swift:Kit/Sources/Kit", "Kit/Sources/Kit", "Kit", false, "Kit/Sources/Kit/Store.swift"),
		sw("swift:Kit/Tests/KitTests", "Kit/Tests/KitTests", "KitTests", false, "Kit/Tests/KitTests/StoreTests.swift"),
	}
	units[2].Tags, units[3].Tags = nil, nil
	var files []scan.File
	for _, u := range units {
		for _, f := range u.Files {
			files = append(files, scan.File{Path: f, Language: "swift", Lines: 10, Test: strings.HasSuffix(f, "Tests.swift")})
		}
	}
	return Input{Name: "gifts", Files: files, Facts: analyze.Facts{
		Units: units,
		Deps: []analyze.Dep{
			{From: "swift:App/Sources/App", To: "swift:Kit/Sources/Kit", Kind: graph.EdgeDepends},
			{From: "swift:App/Sources/UI", To: "swift:Kit/Sources/Kit", Kind: graph.EdgeDepends},
			{From: "swift:Kit/Tests/KitTests", To: "swift:Kit/Sources/Kit", Kind: graph.EdgeDepends, Label: "tests"},
		},
	}}
}

func TestModuleEntryClaimsItsWholeModule(t *testing.T) {
	m, err := Build(appFacts())
	if err != nil {
		t.Fatal(err)
	}
	for comp, want := range map[string]string{
		"cmp:App/Sources/App":    "ctr:App",
		"cmp:App/Sources/UI":     "ctr:App",
		"cmp:Kit/Sources/Kit":    "ctr:App", // library linked into the app
		"cmp:Kit/Tests/KitTests": "ctr:App", // tests join what they test
	} {
		if got := parentOf(t, m, comp); got != want {
			t.Errorf("parent(%s) = %q, want %q", comp, got, want)
		}
	}
	if n, _ := m.Graph.Node("ctr:App"); n.Name != "App" {
		t.Errorf("container name = %q, want module name App", n.Name)
	}
	if n, _ := m.Graph.Node("cmp:Kit/Tests/KitTests"); !hasTag(n, "test") {
		t.Errorf("test-only component tags = %v, want test", n.Tags)
	}
}

func TestNativeUIUsersTouchTheEntryOnly(t *testing.T) {
	m, err := Build(appFacts())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range m.Graph.Edges() {
		if e.From == UserID {
			got = append(got, e.To+" "+e.Label)
		}
	}
	if want := []string{"cmp:App/Sources/App uses (app)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("user edges = %v, want %v", got, want)
	}
}

func TestFileLevelDepsStartAtTheFile(t *testing.T) {
	files, facts := shopFacts()
	facts.Deps[2].File = "internal/server/server.go" // server → store
	m, err := Build(Input{Name: "shop", Files: files, Facts: facts})
	if err != nil {
		t.Fatal(err)
	}
	v, err := m.Graph.View("cmp:internal/server")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range v.Edges {
		if e.From == "file:internal/server/server.go" && e.To == "ctr:shared" { // store lives in the shared container
			found = true
		}
	}
	if !found {
		t.Errorf("code view edges = %+v, want server.go → shared libraries", v.Edges)
	}
	if got := parentOf(t, m, "cmp:internal/store"); got != "ctr:shared" {
		t.Errorf("container assignment must still use unit deps, got %q", got)
	}
}
