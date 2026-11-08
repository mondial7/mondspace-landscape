package graph

import (
	"fmt"
	"sort"
)

// View is one zoom level of the graph: the children of a focus node, the
// nodes outside it that they interact with ("context"), and every underlying
// edge lifted to the nearest visible ancestor on both ends.
type View struct {
	Focus      *Node      `json:"focus,omitempty"`
	Breadcrumb []Node     `json:"breadcrumb"`
	Nodes      []ViewNode `json:"nodes"`
	Edges      []ViewEdge `json:"edges"`
}

// ViewNode is a node as shown in a view.
type ViewNode struct {
	Node
	Context    bool `json:"context"`    // lives outside the focus (neighbour)
	Expandable bool `json:"expandable"` // has children to zoom into
	Children   int  `json:"children"`   // number of descendants
}

// ViewEdge aggregates every underlying edge between two visible nodes.
type ViewEdge struct {
	From   string     `json:"from"`
	To     string     `json:"to"`
	Kinds  []EdgeKind `json:"kinds"`
	Labels []string   `json:"labels,omitempty"`
	Weight int        `json:"weight"`
}

// maxLabels caps labels per aggregated edge to keep diagrams readable.
const maxLabels = 8

// View computes the view focused on id ("" is the top level).
func (g *Graph) View(focus string) (View, error) {
	v := View{Breadcrumb: []Node{}, Nodes: []ViewNode{}, Edges: []ViewEdge{}}
	if focus != "" {
		n, ok := g.nodes[focus]
		if !ok {
			return v, fmt.Errorf("%w: %q", ErrUnknownNode, focus)
		}
		f := *n
		v.Focus = &f
		anc := g.Ancestors(focus)
		for i := len(anc) - 1; i >= 0; i-- {
			v.Breadcrumb = append(v.Breadcrumb, *g.nodes[anc[i]])
		}
		v.Breadcrumb = append(v.Breadcrumb, f)
	}

	// Ancestors of the focus (including the focus and the virtual root "").
	focusLine := map[string]bool{"": true, focus: true}
	for _, a := range g.Ancestors(focus) {
		focusLine[a] = true
	}

	inside := map[string]bool{}
	for _, c := range g.children[focus] {
		inside[c] = true
	}

	// visible maps any node to the node that represents it in this view
	// (or "" when it is hidden: the focus itself or an ancestor of it).
	visible := func(id string) (string, bool) {
		cur := id
		for {
			n := g.nodes[cur]
			if focusLine[cur] {
				return "", false
			}
			if focusLine[n.Parent] {
				return cur, !inside[cur]
			}
			cur = n.Parent
		}
	}

	type pair struct{ from, to string }
	agg := map[pair]*ViewEdge{}
	var pairs []pair
	context := map[string]bool{}
	for _, e := range g.edges {
		from, fromCtx := visible(e.From)
		to, toCtx := visible(e.To)
		if from == "" || to == "" || from == to || (fromCtx && toCtx) {
			continue
		}
		if fromCtx {
			context[from] = true
		}
		if toCtx {
			context[to] = true
		}
		p := pair{from, to}
		ve, ok := agg[p]
		if !ok {
			ve = &ViewEdge{From: from, To: to}
			agg[p] = ve
			pairs = append(pairs, p)
		}
		ve.Weight += e.Weight
		ve.Kinds = addUnique(ve.Kinds, e.Kind)
		if e.Label != "" && len(ve.Labels) < maxLabels {
			ve.Labels = addUnique(ve.Labels, e.Label)
		}
	}

	add := func(id string, ctx bool) {
		n := g.nodes[id]
		d := g.Descendants(id)
		v.Nodes = append(v.Nodes, ViewNode{Node: *n, Context: ctx, Expandable: d > 0, Children: d})
	}
	for _, c := range sortedKeys(inside) {
		add(c, false)
	}
	for _, c := range sortedKeys(context) {
		add(c, true)
	}
	for _, p := range pairs {
		ve := agg[p]
		sort.Slice(ve.Kinds, func(i, j int) bool { return ve.Kinds[i] < ve.Kinds[j] })
		v.Edges = append(v.Edges, *ve)
	}
	return v, nil
}

func addUnique[T comparable](xs []T, x T) []T {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
