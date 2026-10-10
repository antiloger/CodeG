package query

import (
	"fmt"
	"strings"

	"codeg/internal/model"
)

// FunctionGraph is level 2: one function with its signature, locals, closures,
// what it calls, the types it constructs or uses, and the globals and fields it
// touches. Edges carry source-order numbers: the order the occurrences appear
// in the text, which is not the order they execute in.
func (ix *Index) FunctionGraph(fn model.Key, o GraphOptions) (*Graph, error) {
	r, err := ix.Reader(fn.Pkg)
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
	bySym := make(map[model.Key]*model.Symbol, len(syms))
	for i := range syms {
		bySym[syms[i].Key] = &syms[i]
	}
	fsym := bySym[fn]
	if fsym == nil {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, fn)
	}
	switch fsym.Kind {
	case model.KindFunc, model.KindMethod, model.KindFuncLit:
	default:
		return nil, fmt.Errorf("%s is a %s, not a function", fn, fsym.Kind)
	}

	g := &Graph{Level: "function", Root: fn.String(), Nodes: []GraphNode{}}
	g.Notes = append(g.Notes, "edge numbers are source order, not execution order")
	nodes := map[string]*GraphNode{}
	var order []string
	add := func(n GraphNode) {
		if _, ok := nodes[n.ID]; !ok {
			nodes[n.ID] = &n
			order = append(order, n.ID)
		}
	}
	edges := newEdgeSet()

	center := ix.symbolNode(fn.Pkg, files, fsym)
	center.Kind, center.Label = "function", fn.Name
	if fsym.Kind == model.KindFuncLit {
		center.Kind, center.Label = "closure", "func literal "+fn.Name
	}
	add(center)

	// Closures, parameters, results and locals declared in the function or in
	// its closures.
	inTree := func(owner string) bool { return owner == fn.Name || strings.HasPrefix(owner, fn.Name+"$") }
	members := 0
	for i := range syms {
		s := &syms[i]
		name := s.Key.Name
		switch s.Kind {
		case model.KindFuncLit:
			if !strings.HasPrefix(name, fn.Name+"$") {
				continue
			}
			n := ix.symbolNode(fn.Pkg, files, s)
			n.Kind, n.Label = "closure", "func literal "+name[len(fn.Name):]
			n.Parent = model.Key{Pkg: fn.Pkg, Name: name[:strings.LastIndexByte(name, '$')]}.String()
			add(n)
			edges.add(n.Parent, n.ID, "contains", "", "", 0)
		case model.KindLocal, model.KindParam, model.KindResult:
			owner := name
			if j := strings.Index(name, "::"); j >= 0 {
				owner = name[:j]
			} else if j := strings.IndexByte(name, '#'); j >= 0 {
				owner = name[:j]
			}
			if !inTree(owner) || members >= 400 {
				continue
			}
			members++
			n := ix.symbolNode(fn.Pkg, files, s)
			n.Kind = s.Kind.String()
			n.Label = s.Name
			if n.Label == "" {
				n.Label = "(unnamed)"
			}
			add(n)
			rel := map[model.Kind]string{model.KindLocal: "declares", model.KindParam: "has_param", model.KindResult: "has_result"}[s.Kind]
			edges.add(model.Key{Pkg: fn.Pkg, Name: owner}.String(), n.ID, rel, "", "", 0)
		}
	}

	seq := 0
	unknown := 0
	for _, oc := range occs {
		if !enclosedBy(oc.Enclosing, fn) || oc.Role == model.RoleDefinition || oc.Role == model.RoleImport {
			continue
		}
		if int(oc.File) < len(files) && (files[oc.File].Test && !o.Tests || files[oc.File].Generated && !o.Generated) {
			continue
		}
		kind := edgeKindOf(oc.Role)
		if kind == "" {
			continue
		}
		src := fn.String()
		if oc.Enclosing != fn {
			if _, ok := nodes[oc.Enclosing.String()]; ok {
				src = oc.Enclosing.String()
			}
		}
		seq++
		var dst string
		disp, cert := "", Certainty("")
		if kind == "call" {
			disp, cert = oc.Dispatch.String(), certaintyOf(oc.Dispatch, oc.Symbol)
		}
		switch {
		case oc.Symbol == (model.Key{}):
			unknown++
			dst = fmt.Sprintf("unknown:%d", oc.Call)
			add(GraphNode{ID: dst, Label: "computed call", Kind: "unknown",
				Detail: "the called value is a call result, map or slice element: target unknown"})
		case oc.Symbol.Pkg == "":
			continue // universe symbols are not shown
		default:
			id, ok := ix.functionNode(oc.Symbol, nodes, add)
			if !ok {
				continue
			}
			dst = id
		}
		edges.add(src, dst, kind, disp, cert, seq)

		if oc.FlowTo != (model.Key{}) {
			if to, ok := nodes[oc.FlowTo.String()]; ok && oc.Symbol != (model.Key{}) {
				edges.add(oc.Symbol.String(), to.ID, "flow", "", "", seq)
			}
		}
		// An interface call reaches one of its implementations: show them as
		// candidates, never as facts.
		if kind == "call" && oc.Dispatch == model.DispatchInterface && oc.Symbol != (model.Key{}) {
			if cands, err := ix.candidateMethods(oc.Symbol); err == nil {
				for i, c := range cands {
					if i >= 8 {
						break
					}
					add(GraphNode{ID: c.Key, Label: c.Name, Kind: "candidate", Detail: c.Detail, Location: c.Location, Group: c.Package})
					edges.add(dst, c.Key, "may_call", "interface", Candidate, 0)
				}
			}
		}
	}

	g.Edges = edges.finish()
	max := o.max()
	if len(order) > max {
		g.Truncated = true
		order = order[:max]
		g.Notes = append(g.Notes, "large function: some nodes were left out")
	}
	keep := map[string]bool{}
	for _, id := range order {
		g.Nodes = append(g.Nodes, *nodes[id])
		keep[id] = true
	}
	kept := g.Edges[:0]
	for _, e := range g.Edges {
		if keep[e.Source] && keep[e.Target] {
			kept = append(kept, e)
		}
	}
	g.Edges = kept
	return g, nil
}

// functionNode returns the id of the node for a symbol that a function refers
// to, creating it if needed. It reports false for symbols the view leaves out.
func (ix *Index) functionNode(k model.Key, nodes map[string]*GraphNode, add func(GraphNode)) (string, bool) {
	id := k.String()
	if _, ok := nodes[id]; ok {
		return id, true
	}
	ref := ix.symbolRef(k)
	var kind string
	switch ref.Kind {
	case "func", "method", "func_lit":
		kind = "callee"
	case "type", "alias", "type_param":
		kind = "type"
	case "field":
		kind = "field"
	case "var", "const":
		kind = "global"
	default:
		return "", false // packages, labels, other functions' locals, unknown
	}
	n := GraphNode{ID: id, Label: k.Name, Kind: kind, Detail: ref.Detail, Location: ref.Location, External: ref.External, Group: k.Pkg}
	if kind == "field" || kind == "callee" {
		n.Label = shortName(k)
	}
	add(n)
	return id, true
}

// shortName drops a leading package from the label: pkg.Type.Field -> Type.Field.
func shortName(k model.Key) string { return k.Name }
