// Package landscape turns analyzer facts into a layered architecture model:
//
//	people & external systems
//	└─ system (the repository)
//	   └─ containers (deployable/runnable units)
//	      └─ components (packages / source directories)
//	         └─ files
//
// It is the only place that knows architecture heuristics; analyzers only
// report facts and the graph package only knows hierarchy and views.
package landscape

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/mondial7/mondspace-landscape/internal/analyze"
	"github.com/mondial7/mondspace-landscape/internal/graph"
	"github.com/mondial7/mondspace-landscape/internal/scan"
)

// Well-known node IDs.
const (
	SystemID   = "sys"
	UserID     = "person:user"
	OperatorID = "person:operator"
	sharedKey  = "shared"
)

// Input is everything the assembler needs.
type Input struct {
	Name   string
	Files  []scan.File
	Facts  analyze.Facts
	Readme string
}

// Summary describes the repository as a whole.
type Summary struct {
	Name       string         `json:"name"`
	Files      int            `json:"files"`
	Lines      int            `json:"lines"`
	Languages  map[string]int `json:"languages"`
	Containers int            `json:"containers"`
	Components int            `json:"components"`
	Externals  int            `json:"externals"`
	Warnings   []string       `json:"warnings,omitempty"`
}

// Model is the assembled landscape.
type Model struct {
	Graph   *graph.Graph
	Summary Summary
}

// component is the merge of all units sharing a directory.
type component struct {
	id, dir, name, root string
	langs               []string
	files               []string
	entry, test         bool // test: every file is a test
	routes, symbols     []string
	tags                map[string]bool
	doc                 string
	container           string
}

type container struct {
	key, name string
	tags      map[string]bool
	langs     []string
	deploy    []analyze.Service
}

type assembler struct {
	in         Input
	g          *graph.Graph
	files      map[string]scan.File
	comps      map[string]*component // by component id
	unitComp   map[string]string     // unit id → component id
	fileComp   map[string]string     // file path → component id
	containers map[string]*container
	ctrOrder   []string
}

// Build assembles the landscape model from analyzer output.
func Build(in Input) (*Model, error) {
	a := &assembler{
		in: in, g: graph.New(),
		files: map[string]scan.File{}, comps: map[string]*component{},
		unitComp: map[string]string{}, fileComp: map[string]string{},
		containers: map[string]*container{},
	}
	for _, f := range in.Files {
		a.files[f.Path] = f
	}
	a.mergeComponents()
	a.assignContainers()
	steps := []func() error{
		a.addSystem, a.addContainers, a.addComponents, a.addExternals,
		a.addDeps, a.addRefs, a.addUses, a.addDataFlows, a.addCalls, a.addServices, a.addPeople,
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return &Model{Graph: a.g, Summary: a.summary()}, nil
}

func componentID(dir string) string { return "cmp:" + dir }
func containerID(key string) string { return "ctr:" + key }
func externalID(id string) string   { return "ext:" + id }
func fileID(p string) string        { return "file:" + p }

// mergeComponents groups units by directory.
func (a *assembler) mergeComponents() {
	for _, u := range a.in.Facts.Units {
		id := componentID(u.Dir)
		c, ok := a.comps[id]
		if !ok {
			c = &component{id: id, dir: u.Dir, name: u.Name, root: u.Root, tags: map[string]bool{}}
			a.comps[id] = c
		}
		if u.Language == "go" && c.name != u.Name {
			c.name = u.Name // Go package names are the most meaningful
		}
		if c.root == "" {
			c.root = u.Root
		}
		c.langs = addUnique(c.langs, u.Language)
		c.entry = c.entry || u.Entry
		c.routes = append(c.routes, u.Routes...)
		c.symbols = append(c.symbols, u.Symbols...)
		if c.doc == "" {
			c.doc = u.Doc
		}
		for _, t := range u.Tags {
			c.tags[t] = true
		}
		for _, f := range u.Files {
			if _, dup := a.fileComp[f]; !dup {
				c.files = append(c.files, f)
				a.fileComp[f] = id
			}
		}
		a.unitComp[u.ID] = id
	}
	for _, c := range a.comps {
		sort.Strings(c.files)
		sort.Strings(c.routes)
		c.routes = dedup(c.routes)
		c.test = len(c.files) > 0
		for _, f := range c.files {
			c.test = c.test && a.files[f].Test
		}
		if c.test {
			c.tags["test"] = true
		}
	}
}

// assignContainers decides which runnable unit each component belongs to:
//
//  1. entry points seed containers (keyed by their module/project root when
//     the language has one, else by their directory);
//  2. components reachable from exactly one entry join it, from several
//     entries they become shared libraries;
//  3. embedded files join their embedder;
//  4. the rest is grouped by project root (joining an entry of that root);
//  5. test-only components join the container of the code they test.
func (a *assembler) assignContainers() {
	adj := map[string][]string{}
	testAdj := map[string][]string{}
	for _, d := range a.in.Facts.Deps {
		from, to := a.unitComp[d.From], a.unitComp[d.To]
		if from == "" || to == "" {
			continue
		}
		if d.Label == analyze.TestsLabel {
			testAdj[from] = append(testAdj[from], to)
		} else {
			adj[from] = append(adj[from], to)
		}
	}
	ids := a.sortedComponents()
	reached := map[string][]string{} // component → entries reaching it
	for _, id := range ids {
		c := a.comps[id]
		if !c.entry {
			continue
		}
		key, name := c.dir, c.name
		if c.root != "" {
			key, name = c.root, a.rootName(c.root)
		}
		c.container = key
		a.ensureContainer(key, name)
		for _, r := range a.reachable(adj, id) {
			reached[r] = addUnique(reached[r], key)
		}
	}
	for _, id := range ids {
		c := a.comps[id]
		if c.container != "" {
			continue
		}
		switch r := reached[id]; {
		case len(r) == 1:
			c.container = r[0]
		case len(r) > 1:
			c.container = sharedKey
			a.ensureContainer(sharedKey, "Shared libraries")
		}
	}
	for _, ref := range a.in.Facts.Refs {
		from := a.comps[a.unitComp[ref.From]]
		if from == nil || from.container == "" {
			continue
		}
		for _, id := range a.refTargets(ref) {
			if c := a.comps[id]; c.container == "" {
				c.container = from.container
			}
		}
	}
	group := func(c *component) {
		key := c.root
		if key == "" {
			key = topDir(c.dir)
		}
		c.container = key
		a.ensureContainer(key, a.rootName(key))
	}
	for _, id := range ids {
		if c := a.comps[id]; c.container == "" && !c.test {
			group(c)
		}
	}
	for _, id := range ids {
		c := a.comps[id]
		if c.container != "" {
			continue
		}
		for _, target := range append(testAdj[id], adj[id]...) {
			if t := a.comps[target]; t.container != "" {
				c.container = t.container
				break
			}
		}
		if c.container == "" {
			group(c)
		}
	}
	for _, id := range ids {
		c := a.comps[id]
		ctr := a.containers[c.container]
		for t := range c.tags {
			if t != "test" {
				ctr.tags[t] = true
			}
		}
		for _, l := range c.langs {
			ctr.langs = addUnique(ctr.langs, l)
		}
	}
}

// reachable returns the components reachable from start (excluding it).
func (a *assembler) reachable(adj map[string][]string, start string) []string {
	seen := map[string]bool{start: true}
	var out []string
	queue := []string{start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, nxt := range adj[cur] {
			if !seen[nxt] {
				seen[nxt] = true
				out = append(out, nxt)
				queue = append(queue, nxt)
			}
		}
	}
	return out
}

// rootName names a container keyed by a project root.
func (a *assembler) rootName(root string) string {
	if root == "." {
		return a.in.Name
	}
	return path.Base(root)
}

func (a *assembler) ensureContainer(key, name string) *container {
	if c, ok := a.containers[key]; ok {
		return c
	}
	c := &container{key: key, name: name, tags: map[string]bool{}}
	a.containers[key] = c
	a.ctrOrder = append(a.ctrOrder, key)
	return c
}

// refTargets resolves a path/glob reference to the components owning matching files.
func (a *assembler) refTargets(ref analyze.Ref) []string {
	seen := map[string]bool{}
	for f, id := range a.fileComp {
		ok, _ := path.Match(ref.Path, f)
		if ok || strings.HasPrefix(f, strings.TrimSuffix(ref.Path, "/")+"/") {
			seen[id] = true
		}
	}
	return sortedSet(seen)
}

func (a *assembler) sortedComponents() []string {
	ids := make([]string, 0, len(a.comps))
	for id := range a.comps {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (a *assembler) addSystem() error {
	return a.g.AddNode(graph.Node{
		ID: SystemID, Name: a.in.Name, Kind: graph.KindSystem,
		Description: readmeSummary(a.in.Readme),
		Technology:  strings.Join(topLanguages(a.in.Files, 4), " · "),
		Metrics:     map[string]int{"files": len(a.in.Files), "lines": totalLines(a.in.Files)},
	})
}

func (a *assembler) addContainers() error {
	for _, key := range a.ctrOrder {
		c := a.containers[key]
		n := graph.Node{
			ID: containerID(key), Name: c.name, Kind: graph.KindContainer, Parent: SystemID,
			Technology: technology(c.langs), Tags: sortedSet(c.tags), Path: key,
			Description: containerDescription(c),
		}
		if err := a.g.AddNode(n); err != nil {
			return err
		}
	}
	return nil
}

func (a *assembler) addComponents() error {
	for _, id := range a.sortedComponents() {
		c := a.comps[id]
		lines := 0
		for _, f := range c.files {
			lines += a.files[f].Lines
		}
		n := graph.Node{
			ID: id, Name: c.name, Kind: graph.KindComponent, Parent: containerID(c.container),
			Technology: technology(c.langs), Description: c.doc, Path: c.dir, Tags: sortedSet(c.tags),
			Metrics: map[string]int{"files": len(c.files), "lines": lines},
			Details: map[string]string{},
		}
		if len(c.routes) > 0 {
			n.Details["routes"] = strings.Join(c.routes, "\n")
			n.Metrics["routes"] = len(c.routes)
		}
		if len(c.symbols) > 0 {
			n.Details["symbols"] = strings.Join(c.symbols, ", ")
		}
		if err := a.g.AddNode(n); err != nil {
			return err
		}
		for _, f := range c.files {
			sf := a.files[f]
			fn := graph.Node{
				ID: fileID(f), Name: path.Base(f), Kind: graph.KindFile, Parent: id, Path: f,
				Technology: sf.Language, Metrics: map[string]int{"lines": sf.Lines},
			}
			if sf.Test {
				fn.Tags = []string{"test"}
			}
			if err := a.g.AddNode(fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *assembler) addExternals() error {
	for _, e := range a.in.Facts.Externals {
		if err := a.addExternal(e); err != nil {
			return err
		}
	}
	return nil
}

func (a *assembler) addExternal(e analyze.External) error {
	if a.g.Has(externalID(e.ID)) {
		return nil
	}
	tech := e.Technology
	if tech == "" {
		tech = string(e.Kind)
	}
	return a.g.AddNode(graph.Node{
		ID: externalID(e.ID), Name: e.Name, Kind: graph.KindExternal,
		Technology: tech, Tags: []string{string(e.Kind)},
	})
}

func (a *assembler) addDeps() error {
	for _, d := range a.in.Facts.Deps {
		from, to := a.unitComp[d.From], a.unitComp[d.To]
		if from == "" || to == "" {
			continue
		}
		if d.File != "" && a.fileComp[d.File] == from {
			from = fileID(d.File) // most precise origin; views lift it as needed
		}
		if err := a.g.AddEdge(graph.Edge{From: from, To: to, Kind: d.Kind, Label: d.Label}); err != nil {
			return err
		}
	}
	return nil
}

func (a *assembler) addRefs() error {
	for _, r := range a.in.Facts.Refs {
		from := a.unitComp[r.From]
		if from == "" {
			continue
		}
		for _, to := range a.refTargets(r) {
			if err := a.g.AddEdge(graph.Edge{From: from, To: to, Kind: r.Kind, Label: r.Label}); err != nil {
				return err
			}
		}
	}
	return nil
}

// addUses links code to external systems. Database uses of components whose
// SQL is known are left to addDataFlows, which adds labelled edges instead.
func (a *assembler) addUses() error {
	withSQL := map[string]bool{}
	for _, d := range a.in.Facts.Data {
		withSQL[a.fileComp[d.File]] = true
	}
	for _, u := range a.in.Facts.Uses {
		from := a.unitComp[u.Unit]
		if u.File != "" {
			if _, ok := a.fileComp[u.File]; ok {
				from = fileID(u.File)
			}
		}
		to := externalID(u.External)
		if from == "" || !a.g.Has(to) {
			continue
		}
		if u.Kind == graph.EdgeData && withSQL[from] {
			continue
		}
		if err := a.g.AddEdge(graph.Edge{From: from, To: to, Kind: u.Kind, Label: u.Label}); err != nil {
			return err
		}
	}
	return nil
}

// addDataFlows turns SQL table access into labelled edges to the database
// the component (or its container) uses.
func (a *assembler) addDataFlows() error {
	for _, d := range a.in.Facts.Data {
		comp := a.fileComp[d.File]
		if comp == "" {
			continue
		}
		dbs := a.databasesFor(comp)
		if len(dbs) == 0 {
			if err := a.addExternal(analyze.External{ID: "sql", Name: "SQL database", Kind: analyze.ExtDatabase}); err != nil {
				return err
			}
			dbs = []string{externalID("sql")}
		}
		for _, db := range dbs {
			if len(d.Reads) > 0 {
				if err := a.g.AddEdge(graph.Edge{From: comp, To: db, Kind: graph.EdgeData, Label: "reads " + strings.Join(d.Reads, ", ")}); err != nil {
					return err
				}
			}
			if len(d.Writes) > 0 {
				if err := a.g.AddEdge(graph.Edge{From: comp, To: db, Kind: graph.EdgeData, Label: "writes " + strings.Join(d.Writes, ", ")}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// databasesFor finds database externals used by a component, falling back
// to those used anywhere in its container.
func (a *assembler) databasesFor(comp string) []string {
	isDB := map[string]bool{}
	for _, e := range a.in.Facts.Externals {
		if e.Kind == analyze.ExtDatabase {
			isDB[e.ID] = true
		}
	}
	collect := func(match func(string) bool) []string {
		seen := map[string]bool{}
		for _, u := range a.in.Facts.Uses {
			if isDB[u.External] && match(a.unitComp[u.Unit]) {
				seen[externalID(u.External)] = true
			}
		}
		return sortedSet(seen)
	}
	if dbs := collect(func(c string) bool { return c == comp }); len(dbs) > 0 {
		return dbs
	}
	ctr := a.comps[comp].container
	return collect(func(c string) bool { return c != "" && a.comps[c].container == ctr })
}

// addCalls matches HTTP requests made by UI/client code to the routes that
// serve them, producing component → component HTTP edges.
func (a *assembler) addCalls() error {
	var routes []string
	owner := map[string]string{}
	for _, id := range a.sortedComponents() {
		for _, r := range a.comps[id].routes {
			if _, dup := owner[r]; !dup {
				routes = append(routes, r)
				owner[r] = id
			}
		}
	}
	if len(routes) == 0 {
		return nil
	}
	for _, c := range a.in.Facts.Calls {
		from := a.fileComp[c.File]
		if from == "" {
			continue
		}
		route, ok := analyze.MatchRoute(routes, c.Method, c.Path)
		if !ok {
			continue
		}
		label := route
		if m, _ := analyze.SplitRoute(route); m == "" && c.Method != "" {
			label = c.Method + " " + route
		}
		if err := a.g.AddEdge(graph.Edge{From: from, To: owner[route], Kind: graph.EdgeHTTP, Label: label}); err != nil {
			return err
		}
	}
	return nil
}

// addServices attaches deployment information to containers and adds the
// runtime relations declared by infrastructure files.
func (a *assembler) addServices() error {
	svcContainer := map[string]string{} // service name → container node id
	for _, s := range a.in.Facts.Services {
		key, ok := a.containerForService(s)
		if !ok {
			key = "svc:" + s.Name
			if !a.g.Has(containerID(key)) {
				tech := s.Image
				if tech == "" {
					tech = s.Platform
				}
				if err := a.g.AddNode(graph.Node{
					ID: containerID(key), Name: s.Name, Kind: graph.KindContainer, Parent: SystemID,
					Technology: tech, Description: "Runtime service declared in " + s.Source,
				}); err != nil {
					return err
				}
			}
			a.ensureContainer(key, s.Name)
		}
		a.containers[key].deploy = append(a.containers[key].deploy, s)
		svcContainer[s.Name] = containerID(key)
	}
	for _, key := range a.ctrOrder {
		c := a.containers[key]
		if len(c.deploy) == 0 {
			continue
		}
		deploy := deploySummary(c.deploy)
		if err := a.g.Update(containerID(key), func(n *graph.Node) {
			if n.Details == nil {
				n.Details = map[string]string{}
			}
			n.Details["deploy"] = deploy
		}); err != nil {
			return err
		}
	}
	for _, s := range a.in.Facts.Services {
		from := svcContainer[s.Name]
		if s.Platform == "fly.io" && a.g.Has(externalID("fly.io")) {
			if err := a.g.AddEdge(graph.Edge{From: from, To: externalID("fly.io"), Kind: graph.EdgeRuntime, Label: "deployed to"}); err != nil {
				return err
			}
		}
		for _, dep := range s.DependsOn {
			to := svcContainer[dep]
			if a.g.Has(externalID("compose:" + dep)) {
				to = externalID("compose:" + dep)
			}
			if to == "" {
				continue
			}
			if err := a.g.AddEdge(graph.Edge{From: from, To: to, Kind: graph.EdgeRuntime, Label: "depends on"}); err != nil {
				return err
			}
		}
	}
	return nil
}

// containerForService maps a service to the code container it builds.
func (a *assembler) containerForService(s analyze.Service) (string, bool) {
	if s.BuildDir == "" {
		return "", false
	}
	if _, ok := a.containers[s.BuildDir]; ok {
		return s.BuildDir, true
	}
	// A build context above the code: pick the single container under it.
	var under []string
	for _, key := range a.ctrOrder {
		if key == sharedKey || strings.HasPrefix(key, "svc:") {
			continue
		}
		if s.BuildDir == "." || key == s.BuildDir || strings.HasPrefix(key, s.BuildDir+"/") {
			under = append(under, key)
		}
	}
	if len(under) == 1 {
		return under[0], true
	}
	return "", false
}

// addPeople adds the humans interacting with the system: browser users of
// web UIs/HTTP servers, app users of native UIs, and operators of CLIs.
func (a *assembler) addPeople() error {
	var uiTargets, uiEntries, httpTargets, cliTargets []string
	for _, id := range a.sortedComponents() {
		c := a.comps[id]
		ctr := a.containers[c.container]
		switch {
		case c.test:
		case c.tags["ui"]:
			uiTargets = append(uiTargets, id)
			if c.entry {
				uiEntries = append(uiEntries, id)
			}
		case len(c.routes) > 0:
			httpTargets = append(httpTargets, id)
		}
		if c.entry && ctr.tags["cli"] && !ctr.tags["http-server"] {
			cliTargets = append(cliTargets, id)
		}
	}
	if len(uiEntries) > 0 {
		uiTargets = uiEntries // native apps: the user launches the app entry
	}
	label, targets := "uses (browser)", uiTargets
	if len(targets) == 0 {
		label, targets = "calls (HTTP)", httpTargets
	}
	if len(targets) > 0 {
		if err := a.g.AddNode(graph.Node{ID: UserID, Name: "User", Kind: graph.KindPerson, Description: "Person using the application"}); err != nil {
			return err
		}
		for _, t := range targets {
			kind, l := graph.EdgeUses, label
			if label == "calls (HTTP)" {
				kind = graph.EdgeHTTP
			} else if isNative(a.comps[t].langs) {
				l = "uses (app)"
			}
			if err := a.g.AddEdge(graph.Edge{From: UserID, To: t, Kind: kind, Label: l}); err != nil {
				return err
			}
		}
	}
	if len(cliTargets) > 0 {
		if err := a.g.AddNode(graph.Node{ID: OperatorID, Name: "Operator", Kind: graph.KindPerson, Description: "Person running the command-line tools"}); err != nil {
			return err
		}
		for _, t := range cliTargets {
			if err := a.g.AddEdge(graph.Edge{From: OperatorID, To: t, Kind: graph.EdgeUses, Label: "runs"}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (a *assembler) summary() Summary {
	s := Summary{
		Name: a.in.Name, Files: len(a.in.Files), Lines: totalLines(a.in.Files),
		Languages: map[string]int{}, Components: len(a.comps), Warnings: a.in.Facts.Warnings,
	}
	for _, f := range a.in.Files {
		s.Languages[f.Language]++
	}
	for _, n := range a.g.Nodes() {
		switch n.Kind {
		case graph.KindContainer:
			s.Containers++
		case graph.KindExternal:
			s.Externals++
		}
	}
	return s
}

// isNative reports whether code in these languages builds native apps.
func isNative(langs []string) bool {
	for _, l := range langs {
		if l == "swift" || l == "kotlin" || l == "java" || l == "dart" {
			return true
		}
	}
	return false
}

func containerDescription(c *container) string {
	tech := technology(c.langs)
	switch {
	case c.key == sharedKey:
		return "Code shared by several executables"
	case c.tags["http-server"]:
		return tech + " web server"
	case c.tags["cli"]:
		return tech + " command-line tool"
	case c.tags["ui"]:
		return tech + " user interface"
	}
	return tech + " module"
}

func deploySummary(svcs []analyze.Service) string {
	var platforms, ports, details []string
	for _, s := range svcs {
		p := s.Platform
		if s.Image != "" && s.Platform == "docker" {
			p += " (" + s.Image + ")"
		}
		platforms = addUnique(platforms, p)
		for _, port := range s.Ports {
			ports = addUnique(ports, port)
		}
		for _, d := range s.Details {
			if !strings.HasPrefix(d, "build stage") {
				details = addUnique(details, d)
			}
		}
	}
	sort.Strings(platforms)
	parts := platforms
	if len(ports) > 0 {
		parts = append(parts, "ports "+strings.Join(ports, ", "))
	}
	return strings.Join(append(parts, details...), " · ")
}

var languageNames = map[string]string{
	"go": "Go", "javascript": "JavaScript", "typescript": "TypeScript", "python": "Python",
	"swift": "Swift", "html": "HTML", "css": "CSS", "kotlin": "Kotlin", "java": "Java",
}

func technology(langs []string) string {
	var out []string
	for _, l := range langs {
		if n, ok := languageNames[l]; ok {
			out = append(out, n)
		} else if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, " · ")
}

func topLanguages(files []scan.File, n int) []string {
	lines := map[string]int{}
	for _, f := range files {
		if languageNames[f.Language] != "" {
			lines[f.Language] += f.Lines
		}
	}
	langs := make([]string, 0, len(lines))
	for l := range lines {
		langs = append(langs, l)
	}
	sort.Slice(langs, func(i, j int) bool {
		if lines[langs[i]] != lines[langs[j]] {
			return lines[langs[i]] > lines[langs[j]]
		}
		return langs[i] < langs[j]
	})
	if len(langs) > n {
		langs = langs[:n]
	}
	for i, l := range langs {
		langs[i] = languageNames[l]
	}
	return langs
}

// readmeSummary returns the first prose paragraph of a README.
func readmeSummary(readme string) string {
	for _, para := range strings.Split(strings.ReplaceAll(readme, "\r\n", "\n"), "\n\n") {
		p := strings.TrimSpace(para)
		if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "<") || strings.HasPrefix(p, "[!") ||
			strings.HasPrefix(p, "![") || strings.HasPrefix(p, "```") || strings.HasPrefix(p, "|") {
			continue
		}
		p = stripMarkdown(strings.Join(strings.Fields(p), " "))
		if len(p) > 400 {
			p = p[:397] + "…"
		}
		return p
	}
	return ""
}

var (
	mdLink     = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	mdEmphasis = regexp.MustCompile("(\\*\\*|__|\\*|`)")
)

// stripMarkdown removes inline markdown (links, emphasis, code spans).
func stripMarkdown(s string) string {
	return mdEmphasis.ReplaceAllString(mdLink.ReplaceAllString(s, "$1"), "")
}

func totalLines(files []scan.File) int {
	n := 0
	for _, f := range files {
		n += f.Lines
	}
	return n
}

func topDir(p string) string {
	if i := strings.IndexByte(p, '/'); i >= 0 {
		return p[:i]
	}
	return "."
}

func addUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func dedup(sorted []string) []string {
	out := sorted[:0]
	for i, s := range sorted {
		if i == 0 || s != sorted[i-1] {
			out = append(out, s)
		}
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// String renders a short textual overview (handy for the CLI and debugging).
func (m *Model) String() string {
	s := m.Summary
	return fmt.Sprintf("%s: %d files, %d lines, %d containers, %d components, %d external systems",
		s.Name, s.Files, s.Lines, s.Containers, s.Components, s.Externals)
}
