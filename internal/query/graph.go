package query

import (
	"path"
	"sort"
	"strings"

	"codeg/internal/model"
)

// GraphNode is one node of a graph slice. The UI lays nodes out; the server
// only decides what is in a slice.
type GraphNode struct {
	ID        string    `json:"id"`
	Label     string    `json:"label"`
	Kind      string    `json:"kind"`
	Group     string    `json:"group,omitempty"`  // grouping key: a directory, package or module
	Parent    string    `json:"parent,omitempty"` // methods point at their type
	Detail    string    `json:"detail,omitempty"`
	Exported  bool      `json:"exported,omitempty"`
	Test      bool      `json:"test,omitempty"`
	Generated bool      `json:"generated,omitempty"`
	External  bool      `json:"external,omitempty"`
	Errors    bool      `json:"errors,omitempty"` // the package has type errors
	Count     int       `json:"count,omitempty"`  // files (package), members, or uses, by kind
	Location  *Location `json:"location,omitempty"`
}

// GraphEdge is a directed, aggregated edge.
type GraphEdge struct {
	ID        string    `json:"id"`
	Source    string    `json:"source"`
	Target    string    `json:"target"`
	Kind      string    `json:"kind"`
	Count     int       `json:"count"`
	Dispatch  string    `json:"dispatch,omitempty"`
	Certainty Certainty `json:"certainty,omitempty"`
	// Orders are the source-order numbers of the occurrences behind the edge
	// (function view). Source order, not execution order.
	Orders []int `json:"orders,omitempty"`
}

// Graph is a slice of the code graph at one level.
type Graph struct {
	Level     string      `json:"level"` // packages | package | function
	Root      string      `json:"root,omitempty"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated,omitempty"`
	Notes     []string    `json:"notes,omitempty"`
}

// edgeSet aggregates edges by (source, target, kind, dispatch).
type edgeSet struct {
	idx   map[[4]string]int
	edges []GraphEdge
}

func newEdgeSet() *edgeSet { return &edgeSet{idx: map[[4]string]int{}} }

func (s *edgeSet) add(source, target, kind, dispatch string, cert Certainty, order int) {
	k := [4]string{source, target, kind, dispatch}
	i, ok := s.idx[k]
	if !ok {
		i = len(s.edges)
		s.idx[k] = i
		s.edges = append(s.edges, GraphEdge{Source: source, Target: target, Kind: kind, Dispatch: dispatch, Certainty: cert})
	}
	s.edges[i].Count++
	if order > 0 && len(s.edges[i].Orders) < 24 {
		s.edges[i].Orders = append(s.edges[i].Orders, order)
	}
}

func (s *edgeSet) finish() []GraphEdge {
	sort.SliceStable(s.edges, func(i, j int) bool {
		a, b := s.edges[i], s.edges[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Target != b.Target {
			return a.Target < b.Target
		}
		return a.Kind+a.Dispatch < b.Kind+b.Dispatch
	})
	for i := range s.edges {
		e := &s.edges[i]
		e.ID = e.Source + " -" + e.Kind
		if e.Dispatch != "" {
			e.ID += "/" + e.Dispatch
		}
		e.ID += "-> " + e.Target
	}
	return s.edges
}

// PackagesOptions selects what the package graph shows.
type PackagesOptions struct {
	Tests     bool // external test packages (p_test)
	Externals bool // dependencies, collapsed to one node per module
}

// PackagesGraph is level 0: the project's packages and their imports, grouped
// by directory. Dependencies are left out unless asked for, and then appear
// once per module.
func (ix *Index) PackagesGraph(o PackagesOptions) *Graph {
	g := &Graph{Level: "packages", Root: ix.Root, Nodes: []GraphNode{}}
	visible := map[string]bool{}
	for i := range ix.Manifest.Packages {
		p := &ix.Manifest.Packages[i]
		test := strings.HasSuffix(p.Path, "_test")
		if test && !o.Tests {
			continue
		}
		visible[p.Path] = true
		label, dir := packageLabel(p.Path, p.Module)
		files := 0
		for _, f := range p.Files {
			if f.Kind == 0 {
				files++
			}
		}
		g.Nodes = append(g.Nodes, GraphNode{
			ID: p.Path, Label: label, Kind: "package", Group: dir, Test: test,
			Errors: p.HasErrors, Count: files, Detail: p.Module,
		})
	}

	edges := newEdgeSet()
	extNodes := map[string]*GraphNode{}
	for _, sp := range ix.Summary.Packages {
		if !visible[sp.Path] {
			continue
		}
		perModule := map[string]int{}
		for _, imp := range sp.Imports {
			if visible[imp] {
				edges.add(sp.Path, imp, "import", "", "", 0)
				continue
			}
			if ix.Manifest.Package(imp) != nil || !o.Externals {
				continue // a hidden test package, or externals collapsed away
			}
			dep, _ := ix.Summary.Dep(imp)
			mod := dep.Module
			id, label := "ext:"+mod, mod
			if mod == "" {
				id, label = "ext:std", "standard library"
			}
			perModule[id]++
			if extNodes[id] == nil {
				extNodes[id] = &GraphNode{ID: id, Label: label, Kind: "module", Group: "external", External: true, Detail: dep.Version}
			}
		}
		for id, n := range perModule {
			for i := 0; i < n; i++ {
				edges.add(sp.Path, id, "import", "", "", 0)
			}
		}
	}
	for _, n := range extNodes {
		g.Nodes = append(g.Nodes, *n)
	}
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	g.Edges = edges.finish()
	if !o.Externals {
		g.Notes = append(g.Notes, "dependencies are collapsed; enable them to see one node per module")
	}
	return g
}

// packageLabel returns a short label and the directory to group under: the
// path relative to the module, so nested directories group naturally.
func packageLabel(pkg, module string) (label, dir string) {
	rel := pkg
	switch {
	case module != "" && pkg == module:
		rel = path.Base(pkg)
	case module != "" && strings.HasPrefix(pkg, module+"/"):
		rel = strings.TrimPrefix(pkg, module+"/")
	}
	if d := path.Dir(rel); d != "." {
		return path.Base(rel), d
	}
	return rel, ""
}

// GraphOptions selects what a package or function slice shows.
type GraphOptions struct {
	Tests     bool
	Generated bool
	Methods   bool // package level: methods as their own nodes (else folded into their type)
	Max       int  // node cap; 0 means the default
}

const defaultMaxNodes = 1200

func (o GraphOptions) max() int {
	if o.Max <= 0 {
		return defaultMaxNodes
	}
	return o.Max
}

// topLevelKind reports whether a symbol is something the package view shows.
func topLevelKind(s *model.Symbol) bool {
	switch s.Kind {
	case model.KindFunc, model.KindMethod, model.KindType, model.KindAlias, model.KindVar, model.KindConst:
	default:
		return false
	}
	// Function-local declarations and blank package-level ones are not nodes.
	return !strings.ContainsAny(s.Key.Name, "@$#~")
}

func isBlankOrInit(k model.Key) bool { return strings.HasPrefix(k.Name, "init#") }

type declRange struct {
	line, end int32
	key       model.Key
}

// PackageGraph is level 1: a package's own functions, types and variables,
// and what it uses from other packages (only what is used), with edges
// aggregated by kind. A big package is capped at the most connected nodes.
func (ix *Index) PackageGraph(pkg string, o GraphOptions) (*Graph, error) {
	entry := ix.Manifest.Package(pkg)
	if entry == nil {
		return nil, ErrNotFound
	}
	r, err := ix.Reader(pkg)
	if err != nil {
		return nil, err
	}
	files, err := r.Files()
	if err != nil {
		return nil, err
	}
	syms, err := r.Symbols()
	if err != nil {
		return nil, err
	}
	occs, err := r.Occurrences()
	if err != nil {
		return nil, err
	}
	structural, err := r.Edges()
	if err != nil {
		return nil, err
	}
	externals, err := r.Externals()
	if err != nil {
		return nil, err
	}

	g := &Graph{Level: "package", Root: pkg, Nodes: []GraphNode{}}
	nodes := map[string]*GraphNode{}
	order := []string{}
	bySym := map[model.Key]*model.Symbol{}
	for i := range syms {
		bySym[syms[i].Key] = &syms[i]
	}
	fileHidden := func(f int32) bool {
		if int(f) >= len(files) {
			return false
		}
		return files[f].Test && !o.Tests || files[f].Generated && !o.Generated
	}

	// The package's own symbols. Methods fold into their type unless asked for.
	fold := map[model.Key]model.Key{} // method -> type
	members := map[model.Key]int{}    // type -> methods+fields
	for _, e := range structural {
		if e.Kind == model.EdgeHasMethod || e.Kind == model.EdgeHasField {
			members[e.From]++
		}
	}
	perFile := map[int32][]declRange{}
	for i := range syms {
		s := &syms[i]
		if !topLevelKind(s) || fileHidden(s.Pos.File) {
			continue
		}
		if s.Kind == model.KindMethod && !o.Methods {
			if t, _, ok := methodOf(s.Key); ok {
				fold[s.Key] = t
			}
		} else {
			n := ix.symbolNode(pkg, files, s)
			n.Group = pkg
			n.Count = members[s.Key]
			if s.Kind == model.KindMethod {
				if t, _, ok := methodOf(s.Key); ok {
					n.Parent = t.String()
				}
			}
			nodes[n.ID] = &n
			order = append(order, n.ID)
		}
		if s.Decl.Line > 0 {
			perFile[s.Decl.File] = append(perFile[s.Decl.File], declRange{s.Decl.Line, s.Decl.EndLine, s.Key})
		}
	}
	for f := range perFile {
		d := perFile[f]
		sort.Slice(d, func(i, j int) bool {
			if d[i].line != d[j].line {
				return d[i].line < d[j].line
			}
			return d[i].end > d[j].end
		})
	}
	// The top-level symbol whose declaration contains a line.
	container := func(file int32, line int32) (model.Key, bool) {
		d := perFile[file]
		i := sort.Search(len(d), func(i int) bool { return d[i].line > line }) - 1
		for steps := 0; i >= 0 && steps < 64; i, steps = i-1, steps+1 {
			if line <= d[i].end {
				return d[i].key, true
			}
		}
		return model.Key{}, false
	}
	// Where a symbol of this package is shown: methods and fields fold into
	// their type unless they have nodes of their own.
	fieldOwner := map[model.Key]model.Key{}
	for _, e := range structural {
		if e.Kind == model.EdgeHasField || e.Kind == model.EdgeHasMethod {
			fieldOwner[e.To] = e.From
		}
	}
	local := func(k model.Key) (string, bool) {
		for hops := 0; hops < 8; hops++ {
			if t, ok := fold[k]; ok {
				k = t
			}
			if n := nodes[k.String()]; n != nil {
				return n.ID, true
			}
			owner, ok := fieldOwner[k]
			if !ok {
				return "", false
			}
			k = owner
		}
		return "", false
	}

	ext := map[model.Key]*model.External{}
	for i := range externals {
		ext[externals[i].Key] = &externals[i]
	}
	edges := newEdgeSet()
	imported := map[string]*GraphNode{}
	var importedOrder []string
	importedNode := func(k model.Key) string {
		// A field of another package's type is shown as the type: the graph
		// says "reads model.Key", not one node per field.
		if kindOfImported(ix, k, ext) == "field" {
			if i := strings.IndexByte(k.Name, '.'); i > 0 {
				k = model.Key{Pkg: k.Pkg, Name: k.Name[:i]}
			}
		}
		id := k.String()
		if _, ok := imported[id]; ok {
			return id
		}
		n := GraphNode{ID: id, Label: k.Name, Group: k.Pkg, External: ix.Manifest.Package(k.Pkg) == nil}
		if e := ext[k]; e != nil {
			n.Kind, n.Detail = e.Kind.String(), e.Detail
			if n.Label == "" {
				n.Label = e.Name
			}
		} else {
			ref := ix.symbolRef(k)
			n.Kind, n.Detail, n.Location = ref.Kind, ref.Detail, ref.Location
		}
		n.Exported = k.Name != "" && k.Name[0] >= 'A' && k.Name[0] <= 'Z'
		imported[id] = &n
		importedOrder = append(importedOrder, id)
		return id
	}

	for _, oc := range occs {
		kind := edgeKindOf(oc.Role)
		if kind == "" || oc.Symbol == (model.Key{}) || oc.Symbol.Pkg == "" || fileHidden(oc.File) {
			continue
		}
		src, ok := local(topLevel(oc.Enclosing))
		if !ok {
			key, found := container(oc.File, oc.Line)
			if !found {
				continue
			}
			if src, ok = local(key); !ok {
				continue
			}
		}
		var dst string
		if oc.Symbol.Pkg == pkg {
			if dst, ok = local(oc.Symbol); !ok {
				continue // a local variable or parameter: internal to a function
			}
		} else {
			switch bySymKind(ix, oc.Symbol, ext) {
			case "package", "":
				continue
			}
			dst = importedNode(oc.Symbol)
		}
		if src == dst {
			continue
		}
		disp := ""
		cert := Certainty("")
		if kind == "call" {
			disp = oc.Dispatch.String()
			cert = certaintyOf(oc.Dispatch, oc.Symbol)
		}
		edges.add(src, dst, kind, disp, cert, 0)
	}
	for _, e := range structural {
		if e.Kind != model.EdgeEmbeds && !(e.Kind == model.EdgeHasMethod && o.Methods) {
			continue
		}
		src, ok := local(e.From)
		if !ok {
			continue
		}
		var dst string
		if e.To.Pkg == pkg {
			if dst, ok = local(e.To); !ok {
				continue
			}
		} else {
			dst = importedNode(e.To)
		}
		edges.add(src, dst, e.Kind.String(), "", "", 0)
	}

	// Cap the slice: keep the most connected nodes, then whatever they touch.
	all := edges.finish()
	max := o.max()
	if len(order)+len(importedOrder) > max {
		g.Truncated = true
		deg := map[string]int{}
		for _, e := range all {
			deg[e.Source] += e.Count
			deg[e.Target] += e.Count
		}
		sort.SliceStable(order, func(i, j int) bool { return deg[order[i]] > deg[order[j]] })
		if len(order) > max/2 {
			order = order[:max/2]
		}
		keep := map[string]bool{}
		for _, id := range order {
			keep[id] = true
		}
		var kept []GraphEdge
		touched := map[string]bool{}
		for _, e := range all {
			if keep[e.Source] && (keep[e.Target] || imported[e.Target] != nil) {
				kept = append(kept, e)
				touched[e.Target] = true
			} else if keep[e.Target] && imported[e.Source] != nil {
				kept = append(kept, e)
				touched[e.Source] = true
			}
		}
		all = kept
		var ii []string
		for _, id := range importedOrder {
			if touched[id] {
				ii = append(ii, id)
			}
		}
		importedOrder = ii
		g.Notes = append(g.Notes, "large package: showing the most connected nodes; filter by kind or open a function")
	}
	sort.Strings(order)
	for _, id := range order {
		g.Nodes = append(g.Nodes, *nodes[id])
	}
	sort.Strings(importedOrder)
	for _, id := range importedOrder {
		g.Nodes = append(g.Nodes, *imported[id])
	}
	g.Edges = all
	if !o.Methods {
		g.Notes = append(g.Notes, "methods are folded into their types")
	}
	return g, nil
}

func bySymKind(ix *Index, k model.Key, ext map[model.Key]*model.External) string {
	return kindOfImported(ix, k, ext)
}

func kindOfImported(ix *Index, k model.Key, ext map[model.Key]*model.External) string {
	if e := ext[k]; e != nil {
		return e.Kind.String()
	}
	return ix.symbolRef(k).Kind
}

// edgeKindOf maps an occurrence role to a coarse edge kind, or "" for roles
// that are not relationships (definitions, imports).
func edgeKindOf(r model.Role) string {
	switch r {
	case model.RoleCall:
		return "call"
	case model.RoleRead:
		return "read"
	case model.RoleWrite, model.RoleReadWrite, model.RoleWritePartial, model.RoleAddressTaken:
		return "write"
	case model.RoleValueRef:
		return "ref"
	case model.RoleConstruct:
		return "construct"
	case model.RoleTypeUse:
		return "type"
	}
	return ""
}

func (ix *Index) symbolNode(pkg string, files []model.FileFact, s *model.Symbol) GraphNode {
	ref := ix.refFromSymbol(pkg, files, s)
	n := GraphNode{
		ID: s.Key.String(), Label: s.Key.Name, Kind: s.Kind.String(), Detail: s.Detail,
		Exported: s.Exported, Location: ref.Location,
	}
	if ref.Location != nil {
		n.Test, n.Generated = ref.Location.Test, ref.Location.Generated
	}
	if i := strings.LastIndexByte(s.Key.Name, '.'); i > 0 && s.Kind == model.KindMethod {
		n.Label = s.Key.Name // Type.Method
	}
	if isBlankOrInit(s.Key) {
		n.Label = "init"
	}
	return n
}
