package graph

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fixture builds a small C4-like hierarchy:
//
//	user (person)            db (external)
//	shop (system)
//	├── api (container)
//	│   ├── http (component) ── file http/handler.go
//	│   └── store (component)
//	└── web (container)
//	    └── ui (component)
func fixture(t *testing.T) *Graph {
	t.Helper()
	g := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.AddNode(Node{ID: "user", Name: "User", Kind: KindPerson}))
	must(g.AddNode(Node{ID: "db", Name: "SQLite", Kind: KindExternal}))
	must(g.AddNode(Node{ID: "shop", Name: "Shop", Kind: KindSystem}))
	must(g.AddNode(Node{ID: "api", Name: "API", Kind: KindContainer, Parent: "shop"}))
	must(g.AddNode(Node{ID: "web", Name: "Web", Kind: KindContainer, Parent: "shop"}))
	must(g.AddNode(Node{ID: "http", Name: "http", Kind: KindComponent, Parent: "api"}))
	must(g.AddNode(Node{ID: "store", Name: "store", Kind: KindComponent, Parent: "api"}))
	must(g.AddNode(Node{ID: "ui", Name: "ui", Kind: KindComponent, Parent: "web"}))
	must(g.AddNode(Node{ID: "handler.go", Name: "handler.go", Kind: KindFile, Parent: "http"}))

	must(g.AddEdge(Edge{From: "user", To: "ui", Kind: EdgeUses, Label: "browses"}))
	must(g.AddEdge(Edge{From: "ui", To: "http", Kind: EdgeHTTP, Label: "GET /api/items"}))
	must(g.AddEdge(Edge{From: "handler.go", To: "store", Kind: EdgeDepends}))
	must(g.AddEdge(Edge{From: "store", To: "db", Kind: EdgeData, Label: "reads/writes"}))
	return g
}

func TestAddNodeRejectsInvalidNodes(t *testing.T) {
	tests := []struct {
		name string
		node Node
	}{
		{"empty id", Node{Name: "x", Kind: KindSystem}},
		{"duplicate id", Node{ID: "shop", Kind: KindSystem}},
		{"unknown parent", Node{ID: "orphan", Kind: KindComponent, Parent: "nope"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := fixture(t)
			if err := g.AddNode(tt.node); err == nil {
				t.Errorf("AddNode(%+v) = nil, want error", tt.node)
			}
		})
	}
}

func TestAddEdgeRejectsUnknownEndpoints(t *testing.T) {
	g := fixture(t)
	if err := g.AddEdge(Edge{From: "api", To: "ghost", Kind: EdgeDepends}); err == nil {
		t.Error("AddEdge to unknown node = nil, want error")
	}
	if err := g.AddEdge(Edge{From: "ghost", To: "api", Kind: EdgeDepends}); err == nil {
		t.Error("AddEdge from unknown node = nil, want error")
	}
}

func TestAddEdgeMergesDuplicates(t *testing.T) {
	g := fixture(t)
	if err := g.AddEdge(Edge{From: "store", To: "db", Kind: EdgeData, Label: "reads/writes"}); err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range g.Edges() {
		if e.From == "store" && e.To == "db" {
			count++
			if e.Weight != 2 {
				t.Errorf("merged edge weight = %d, want 2", e.Weight)
			}
		}
	}
	if count != 1 {
		t.Errorf("store->db edges = %d, want 1 (merged)", count)
	}
}

func TestSelfEdgesAreIgnored(t *testing.T) {
	g := fixture(t)
	before := len(g.Edges())
	if err := g.AddEdge(Edge{From: "api", To: "api", Kind: EdgeDepends}); err != nil {
		t.Fatal(err)
	}
	if got := len(g.Edges()); got != before {
		t.Errorf("edges after self-edge = %d, want %d", got, before)
	}
}

func TestChildrenAndAncestors(t *testing.T) {
	g := fixture(t)
	if got, want := ids(g.Children("api")), []string{"http", "store"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children(api) = %v, want %v", got, want)
	}
	if got, want := ids(g.Children("")), []string{"db", "shop", "user"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Children(root) = %v, want %v", got, want)
	}
	if got, want := g.Ancestors("handler.go"), []string{"http", "api", "shop"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Ancestors(handler.go) = %v, want %v", got, want)
	}
}

func TestViewLiftsEdgesToVisibleLevel(t *testing.T) {
	tests := []struct {
		name      string
		focus     string
		wantNodes []string // id:role, role is "in" for focus children and "ctx" for context
		wantEdges []string // from->to
	}{
		{
			name:      "root shows system and its context",
			focus:     "",
			wantNodes: []string{"db:in", "shop:in", "user:in"},
			wantEdges: []string{"shop->db", "user->shop"},
		},
		{
			name:      "system shows containers plus externals",
			focus:     "shop",
			wantNodes: []string{"api:in", "db:ctx", "user:ctx", "web:in"},
			wantEdges: []string{"api->db", "user->web", "web->api"},
		},
		{
			name:      "container shows components; sibling container becomes context",
			focus:     "api",
			wantNodes: []string{"db:ctx", "http:in", "store:in", "web:ctx"},
			wantEdges: []string{"http->store", "store->db", "web->http"},
		},
		{
			name:      "component shows files",
			focus:     "http",
			wantNodes: []string{"handler.go:in", "store:ctx"},
			wantEdges: []string{"handler.go->store"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := fixture(t)
			v, err := g.View(tt.focus)
			if err != nil {
				t.Fatal(err)
			}
			var gotNodes, gotEdges []string
			for _, n := range v.Nodes {
				role := "in"
				if n.Context {
					role = "ctx"
				}
				gotNodes = append(gotNodes, n.ID+":"+role)
			}
			for _, e := range v.Edges {
				gotEdges = append(gotEdges, e.From+"->"+e.To)
			}
			sort.Strings(gotNodes)
			sort.Strings(gotEdges)
			if !reflect.DeepEqual(gotNodes, tt.wantNodes) {
				t.Errorf("View(%q) nodes = %v, want %v", tt.focus, gotNodes, tt.wantNodes)
			}
			if !reflect.DeepEqual(gotEdges, tt.wantEdges) {
				t.Errorf("View(%q) edges = %v, want %v", tt.focus, gotEdges, tt.wantEdges)
			}
		})
	}
}

func TestViewAggregatesLiftedEdges(t *testing.T) {
	g := fixture(t)
	v, err := g.View("shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range v.Edges {
		if e.From == "web" && e.To == "api" {
			if !reflect.DeepEqual(e.Kinds, []EdgeKind{EdgeHTTP}) {
				t.Errorf("web->api kinds = %v, want [http]", e.Kinds)
			}
			if !reflect.DeepEqual(e.Labels, []string{"GET /api/items"}) {
				t.Errorf("web->api labels = %v", e.Labels)
			}
			return
		}
	}
	t.Fatal("web->api edge not found")
}

func TestViewMarksExpandableNodesAndBreadcrumb(t *testing.T) {
	g := fixture(t)
	v, err := g.View("api")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := ids(v.Breadcrumb), []string{"shop", "api"}; !reflect.DeepEqual(got, want) {
		t.Errorf("breadcrumb = %v, want %v", got, want)
	}
	for _, n := range v.Nodes {
		switch n.ID {
		case "http":
			if !n.Expandable {
				t.Error("http has files, should be expandable")
			}
		case "store":
			if n.Expandable {
				t.Error("store has no children, should not be expandable")
			}
		}
	}
}

func TestViewUnknownFocus(t *testing.T) {
	if _, err := fixture(t).View("nope"); err == nil {
		t.Error("View(nope) = nil error, want error")
	}
}

func ids(nodes []Node) []string {
	out := make([]string, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestParentOf(t *testing.T) {
	g := fixture(t)
	if got := g.ParentOf("store"); got != "api" {
		t.Errorf("ParentOf(store) = %q, want api", got)
	}
	if got := g.ParentOf("shop"); got != "" {
		t.Errorf("ParentOf(shop) = %q, want root", got)
	}
	if got := g.ParentOf("ghost"); got != "" {
		t.Errorf("ParentOf(ghost) = %q, want empty", got)
	}
}

func TestViewJSONUsesEmptyArrays(t *testing.T) {
	g := New()
	if err := g.AddNode(Node{ID: "lonely", Kind: KindSystem}); err != nil {
		t.Fatal(err)
	}
	for _, focus := range []string{"", "lonely"} {
		v, err := g.View(focus)
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		for _, field := range []string{`"nodes":[`, `"edges":[]`, `"breadcrumb":[`} {
			if !strings.Contains(string(b), field) {
				t.Errorf("View(%q) JSON %s lacks %s (null breaks clients)", focus, b, field)
			}
		}
	}
}
