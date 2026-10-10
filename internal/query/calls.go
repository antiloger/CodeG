package query

import (
	"sort"
	"strings"

	"codeg/internal/model"
)

func certaintyOf(d model.Dispatch, callee model.Key) Certainty {
	switch {
	case callee == (model.Key{}):
		return Unknown
	case d == model.DispatchInterface:
		return Candidate
	case d == model.DispatchDynamic:
		return Unknown
	}
	return Certain
}

// Callers returns the functions that call key, grouped by caller. If key is a
// method, calls through interfaces it may satisfy are included as candidates:
// the index cannot tell whether such a call reaches this implementation.
func (ix *Index) Callers(key model.Key, f Filter) ([]CallEdge, error) {
	f.Roles = []model.Role{model.RoleCall}
	occs, err := ix.Occurrences(key, f)
	if err != nil {
		return nil, err
	}
	callee := ix.symbolRef(key)
	edges := groupCalls(ix, occs, func(o Occ) callGroup {
		return callGroup{caller: o.encl, callee: &callee, dispatch: o.Dispatch}
	})

	if typ, name, ok := methodOf(key); ok {
		ifaces, err := ix.InterfacesOf(typ)
		if err == nil {
			qn := qualifiedMethodName(key.Pkg, name)
			for _, im := range ifaces {
				ims, err := ix.methodSetOf(im.Interface.k)
				if err != nil || ims == nil || !hasMethod(ims, qn) {
					continue
				}
				imKey := model.Key{Pkg: im.Interface.k.Pkg, Name: im.Interface.k.Name + "." + name}
				calls, err := ix.Occurrences(imKey, f)
				if err != nil {
					return nil, err
				}
				via := ix.symbolRef(imKey)
				for _, e := range groupCalls(ix, calls, func(o Occ) callGroup {
					return callGroup{caller: o.encl, callee: &callee, dispatch: o.Dispatch}
				}) {
					e.Certainty = Candidate
					e.Via = via.Key
					e.Note = "calls the interface method; this implementation is one possible target"
					edges = append(edges, e)
				}
			}
		}
	}
	sortEdges(edges)
	return edges, nil
}

func hasMethod(ms *model.MethodSet, qualified string) bool {
	for _, m := range ms.Methods {
		if m.Name == qualified {
			return true
		}
	}
	return false
}

type callGroup struct {
	caller   model.Key
	callee   *SymbolRef
	dispatch string
}

// groupCalls groups call-site occurrences into edges.
func groupCalls(ix *Index, occs []Occ, group func(Occ) callGroup) []CallEdge {
	type gk struct {
		caller   model.Key
		callee   model.Key
		dispatch string
	}
	idx := map[gk]int{}
	var edges []CallEdge
	for _, o := range occs {
		g := group(o)
		k := gk{g.caller, model.Key{}, g.dispatch}
		if g.callee != nil {
			k.callee = g.callee.k
		}
		i, ok := idx[k]
		if !ok {
			i = len(edges)
			idx[k] = i
			e := CallEdge{Caller: ix.symbolRef(g.caller), Callee: g.callee, Dispatch: g.dispatch}
			d := model.DispatchStatic
			switch g.dispatch {
			case "interface":
				d = model.DispatchInterface
			case "dynamic":
				d = model.DispatchDynamic
			}
			var ck model.Key
			if g.callee != nil {
				ck = g.callee.k
			}
			e.Certainty = certaintyOf(d, ck)
			edges = append(edges, e)
		}
		edges[i].Sites = append(edges[i].Sites, o)
	}
	return edges
}

func sortEdges(e []CallEdge) {
	sort.SliceStable(e, func(i, j int) bool {
		a, b := e[i], e[j]
		if c := a.Caller.k.Compare(b.Caller.k); c != 0 {
			return c < 0
		}
		var ak, bk model.Key
		if a.Callee != nil {
			ak = a.Callee.k
		}
		if b.Callee != nil {
			bk = b.Callee.k
		}
		return ak.Compare(bk) < 0
	})
}

// Callees returns what fn calls, including from its closures. Interface calls
// list the possible concrete targets as candidates; calls whose target cannot
// be determined (function values from maps, fields or call results) appear
// with no callee.
func (ix *Index) Callees(fn model.Key, f Filter) ([]CallEdge, error) {
	r, err := ix.Reader(fn.Pkg)
	if err != nil {
		return nil, err
	}
	files, err := r.Files()
	if err != nil {
		return nil, err
	}
	all, err := r.Occurrences()
	if err != nil {
		return nil, err
	}
	var occs []Occ
	for _, o := range all {
		if o.Role != model.RoleCall || !enclosedBy(o.Enclosing, fn) {
			continue
		}
		oc := ix.occFrom(fn.Pkg, files, o)
		if f.NoTests && oc.Test || f.NoGenerated && oc.Generated {
			continue
		}
		occs = append(occs, oc)
	}
	edges := groupCalls(ix, occs, func(o Occ) callGroup {
		g := callGroup{caller: o.encl, dispatch: o.Dispatch}
		if o.sym != (model.Key{}) {
			ref := ix.symbolRef(o.sym)
			g.callee = &ref
		}
		return g
	})
	for i := range edges {
		e := &edges[i]
		switch {
		case e.Callee == nil:
			e.Note = "the called value is computed (a call result, map or slice element): target unknown"
		case e.Dispatch == "interface":
			cands, err := ix.candidateMethods(e.Callee.k)
			if err != nil {
				return nil, err
			}
			e.Candidates = cands
			if _, _, isMethod := methodOf(e.Callee.k); isMethod && ix.Manifest.Package(e.Callee.k.Pkg) == nil {
				e.Note = "interface from a dependency: implementations are not indexed"
			} else if len(cands) == 0 {
				e.Note = "no implementation found in the indexed packages"
			}
		case e.Dispatch == "dynamic":
			e.Note = "calls a function value; which function it holds is not tracked"
		}
	}
	sortEdges(edges)
	return edges, nil
}

// topLevel maps a closure key (F$0, T.M$1$0) to the function that contains it.
func topLevel(k model.Key) model.Key {
	if i := strings.IndexByte(k.Name, '$'); i > 0 {
		k.Name = k.Name[:i]
	}
	return k
}
