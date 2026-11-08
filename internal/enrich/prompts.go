package enrich

import (
	"fmt"
	"sort"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/landscape"
)

const componentInstructions = `You are documenting a software architecture diagram.
Describe the responsibility of this COMPONENT in ONE plain sentence (max 25 words).
Focus on what it does for the system, not on how the code is written.
Reply with the sentence only: no preamble, no markdown, no quotes.`

const containerInstructions = `You are documenting a software architecture diagram.
Describe this CONTAINER (a runnable/deployable part of the system) in ONE plain sentence (max 30 words):
what it does and for whom. Reply with the sentence only: no preamble, no markdown.`

const systemInstructions = `You are documenting a software architecture diagram.
Describe this SYSTEM in at most TWO plain sentences (max 50 words): its purpose, its users and its main parts.
Reply with the sentences only: no preamble, no markdown.`

func componentPrompt(g *graph.Graph, n graph.Node, src Source) string {
	var b strings.Builder
	b.WriteString(componentInstructions + "\n\n")
	if sys, ok := g.Node(landscape.SystemID); ok {
		fmt.Fprintf(&b, "System: %s — %s\n", sys.Name, sys.Description)
	}
	fmt.Fprintf(&b, "Name: %s\nPath: %s\nTechnology: %s\n", n.Name, n.Path, n.Technology)
	writeList(&b, "Tags", n.Tags)
	if doc := n.Description; doc != "" {
		fmt.Fprintf(&b, "Documentation: %s\n", doc)
	}
	if r := n.Details["routes"]; r != "" {
		writeList(&b, "HTTP routes served", strings.Split(r, "\n"))
	}
	if s := n.Details["symbols"]; s != "" {
		fmt.Fprintf(&b, "Exported symbols: %s\n", s)
	}
	out, in, ext := neighbours(g, n.ID)
	writeList(&b, "Depends on", out)
	writeList(&b, "Used by", in)
	writeList(&b, "External systems", ext)

	files := g.Children(n.ID)
	var names []string
	var code []graph.Node
	for _, f := range files {
		names = append(names, f.Path)
		if !hasTag(f, "test") {
			code = append(code, f)
		}
	}
	writeList(&b, "Files", capList(names, 20))
	sort.SliceStable(code, func(i, j int) bool { return code[i].Metrics["lines"] > code[j].Metrics["lines"] })
	if len(code) > excerptFiles {
		code = code[:excerptFiles]
	}
	budget := excerptChars
	for _, f := range code {
		data, err := src.Read(f.Path)
		if err != nil || budget <= 0 {
			continue
		}
		ex := head(string(data), excerptLines)
		if len(ex) > budget {
			ex = ex[:budget]
		}
		budget -= len(ex)
		fmt.Fprintf(&b, "\n--- %s ---\n%s\n", f.Path, ex)
	}
	return b.String()
}

func containerPrompt(g *graph.Graph, n graph.Node) string {
	var b strings.Builder
	b.WriteString(containerInstructions + "\n\n")
	if sys, ok := g.Node(landscape.SystemID); ok {
		fmt.Fprintf(&b, "System: %s\n", sys.Name)
	}
	fmt.Fprintf(&b, "Name: %s\nTechnology: %s\n", n.Name, n.Technology)
	writeList(&b, "Tags", n.Tags)
	if d := n.Details["deploy"]; d != "" {
		fmt.Fprintf(&b, "Deployment: %s\n", d)
	}
	b.WriteString("Components:\n")
	for _, c := range capNodes(g.Children(n.ID), 25) {
		if hasTag(c, "test") {
			continue
		}
		fmt.Fprintf(&b, "- %s (%s): %s\n", c.Name, c.Path, c.Description)
	}
	out, in, ext := neighbours(g, n.ID)
	writeList(&b, "Talks to", append(out, ext...))
	writeList(&b, "Used by", in)
	return b.String()
}

func systemPrompt(g *graph.Graph, n graph.Node, src Source) string {
	var b strings.Builder
	b.WriteString(systemInstructions + "\n\n")
	fmt.Fprintf(&b, "Name: %s\nTechnology: %s\n", n.Name, n.Technology)
	for _, name := range []string{"README.md", "readme.md", "README"} {
		if data, err := src.Read(name); err == nil {
			ex := string(data)
			if len(ex) > 1500 {
				ex = ex[:1500]
			}
			fmt.Fprintf(&b, "README excerpt:\n%s\n", ex)
			break
		}
	}
	b.WriteString("Containers:\n")
	for _, c := range g.Children(n.ID) {
		fmt.Fprintf(&b, "- %s (%s): %s\n", c.Name, c.Technology, c.Description)
	}
	var people, ext []string
	for _, top := range g.Children("") {
		switch top.Kind {
		case graph.KindPerson:
			people = append(people, top.Name)
		case graph.KindExternal:
			ext = append(ext, top.Name)
		}
	}
	writeList(&b, "People", people)
	writeList(&b, "External systems", ext)
	return b.String()
}

// neighbours lists, by name, what the subtree rooted at id depends on, what
// depends on it, and the external systems it talks to.
func neighbours(g *graph.Graph, id string) (out, in, ext []string) {
	v, err := g.View(g.ParentOf(id))
	if err != nil {
		return nil, nil, nil
	}
	names := map[string]graph.ViewNode{}
	for _, n := range v.Nodes {
		names[n.ID] = n
	}
	for _, e := range v.Edges {
		switch {
		case e.From == id && names[e.To].Kind == graph.KindExternal:
			ext = append(ext, names[e.To].Name)
		case e.From == id:
			out = append(out, names[e.To].Name)
		case e.To == id:
			in = append(in, names[e.From].Name)
		}
	}
	return out, in, ext
}

func writeList(b *strings.Builder, label string, items []string) {
	if len(items) > 0 {
		fmt.Fprintf(b, "%s: %s\n", label, strings.Join(items, ", "))
	}
}

func capList(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], fmt.Sprintf("… and %d more", len(xs)-n))
	}
	return xs
}

func capNodes(xs []graph.Node, n int) []graph.Node {
	if len(xs) > n {
		return xs[:n]
	}
	return xs
}

func head(s string, lines int) string {
	parts := strings.SplitN(s, "\n", lines+1)
	if len(parts) > lines {
		parts = parts[:lines]
	}
	return strings.Join(parts, "\n")
}
