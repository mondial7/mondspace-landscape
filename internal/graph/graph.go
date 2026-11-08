// Package graph holds the landscape domain model: a hierarchy of nodes
// (system → container → component → file, plus people and external systems)
// connected by typed edges, and the zoomable views computed from it.
package graph

import (
	"errors"
	"fmt"
	"sort"
)

// Kind classifies a node by its abstraction level (C4-inspired).
type Kind string

const (
	KindPerson    Kind = "person"
	KindSystem    Kind = "system"
	KindExternal  Kind = "external"
	KindContainer Kind = "container"
	KindComponent Kind = "component"
	KindFile      Kind = "file"
)

// EdgeKind classifies the nature of a relationship.
type EdgeKind string

const (
	EdgeDepends EdgeKind = "depends" // source-level import / dependency
	EdgeHTTP    EdgeKind = "http"    // HTTP request / API call
	EdgeData    EdgeKind = "data"    // reads/writes a data store
	EdgeUses    EdgeKind = "uses"    // generic usage (person → UI, process exec, …)
	EdgeRuntime EdgeKind = "runtime" // deployment/runtime relation (depends_on, serves, …)
)

// Node is one element of the landscape.
type Node struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Kind        Kind              `json:"kind"`
	Parent      string            `json:"parent,omitempty"`
	Technology  string            `json:"technology,omitempty"`
	Description string            `json:"description,omitempty"`
	Path        string            `json:"path,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metrics     map[string]int    `json:"metrics,omitempty"`
	Details     map[string]string `json:"details,omitempty"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	From   string   `json:"from"`
	To     string   `json:"to"`
	Kind   EdgeKind `json:"kind"`
	Label  string   `json:"label,omitempty"`
	Weight int      `json:"weight"`
}

// Graph is a hierarchical, typed graph. The zero value is not usable; call New.
type Graph struct {
	nodes    map[string]*Node
	order    []string
	children map[string][]string
	edges    []Edge
	edgeIdx  map[edgeKey]int
}

type edgeKey struct {
	from, to string
	kind     EdgeKind
	label    string
}

var (
	ErrInvalidNode = errors.New("invalid node")
	ErrUnknownNode = errors.New("unknown node")
)

// New returns an empty graph.
func New() *Graph {
	return &Graph{
		nodes:    map[string]*Node{},
		children: map[string][]string{},
		edgeIdx:  map[edgeKey]int{},
	}
}

// AddNode inserts a node. Its parent, if any, must already exist.
func (g *Graph) AddNode(n Node) error {
	if n.ID == "" {
		return fmt.Errorf("%w: empty id", ErrInvalidNode)
	}
	if _, dup := g.nodes[n.ID]; dup {
		return fmt.Errorf("%w: duplicate id %q", ErrInvalidNode, n.ID)
	}
	if n.Parent != "" {
		if _, ok := g.nodes[n.Parent]; !ok {
			return fmt.Errorf("%w: parent %q of %q", ErrUnknownNode, n.Parent, n.ID)
		}
	}
	if n.Name == "" {
		n.Name = n.ID
	}
	g.nodes[n.ID] = &n
	g.order = append(g.order, n.ID)
	g.children[n.Parent] = append(g.children[n.Parent], n.ID)
	return nil
}

// Has reports whether a node exists.
func (g *Graph) Has(id string) bool {
	_, ok := g.nodes[id]
	return ok
}

// Node returns a copy of the node with the given id.
func (g *Graph) Node(id string) (Node, bool) {
	n, ok := g.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// Update applies fn to the stored node (e.g. to add a description).
func (g *Graph) Update(id string, fn func(*Node)) error {
	n, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownNode, id)
	}
	parent := n.Parent
	fn(n)
	n.ID, n.Parent = id, parent // identity and position are immutable
	return nil
}

// AddEdge inserts a relationship. Identical edges (same endpoints, kind and
// label) are merged by incrementing their weight; self-edges are dropped.
func (g *Graph) AddEdge(e Edge) error {
	if !g.Has(e.From) {
		return fmt.Errorf("%w: edge source %q", ErrUnknownNode, e.From)
	}
	if !g.Has(e.To) {
		return fmt.Errorf("%w: edge target %q", ErrUnknownNode, e.To)
	}
	if e.From == e.To {
		return nil
	}
	if e.Weight <= 0 {
		e.Weight = 1
	}
	k := edgeKey{e.From, e.To, e.Kind, e.Label}
	if i, ok := g.edgeIdx[k]; ok {
		g.edges[i].Weight += e.Weight
		return nil
	}
	g.edgeIdx[k] = len(g.edges)
	g.edges = append(g.edges, e)
	return nil
}

// Nodes returns all nodes in insertion order.
func (g *Graph) Nodes() []Node {
	out := make([]Node, 0, len(g.order))
	for _, id := range g.order {
		out = append(out, *g.nodes[id])
	}
	return out
}

// Edges returns all edges in insertion order.
func (g *Graph) Edges() []Edge {
	return append([]Edge(nil), g.edges...)
}

// Children returns the direct children of id ("" for top-level nodes), sorted by id.
func (g *Graph) Children(id string) []Node {
	ids := append([]string(nil), g.children[id]...)
	sort.Strings(ids)
	out := make([]Node, 0, len(ids))
	for _, c := range ids {
		out = append(out, *g.nodes[c])
	}
	return out
}

// Ancestors returns the parent chain of id, nearest first.
func (g *Graph) Ancestors(id string) []string {
	var out []string
	for n, ok := g.nodes[id]; ok && n.Parent != ""; n, ok = g.nodes[n.Parent] {
		out = append(out, n.Parent)
	}
	return out
}

// Descendants counts all nodes below id.
func (g *Graph) Descendants(id string) int {
	total := 0
	for _, c := range g.children[id] {
		total += 1 + g.Descendants(c)
	}
	return total
}

// ParentOf returns the parent of id ("" for top-level or unknown nodes).
func (g *Graph) ParentOf(id string) string {
	if n, ok := g.nodes[id]; ok {
		return n.Parent
	}
	return ""
}
