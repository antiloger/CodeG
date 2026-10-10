// Package plan holds the pure logic that decides what to re-index: the
// per-package input and API hashes, and the order to process packages in.
package plan

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/types"
	"hash"
	"sort"
)

// APIHash hashes what importers of pkg can observe, so an edit that leaves it
// unchanged does not force importers to be re-indexed (early cutoff).
//
// It covers every exported package-level object and, transitively, every named
// type reachable from them (exported or not): kind, name, type parameters,
// structure including unexported fields and methods (embedding promotes them),
// and method signatures. It leaves out everything importers' facts cannot
// depend on: function bodies, comments, source positions (which go/gcexport
// data would include, and which shift when a comment is added), and constant
// values.
//
// When the reachable types include types declared in other packages, those
// packages' API hashes are folded in through depAPI, so a change to the
// dependency's type reaches importers of this package even though this
// package's own declarations did not change.
func APIHash(pkg *types.Package, depAPI func(path string) string) string {
	w := &apiWalker{
		h:       sha256.New(),
		pkg:     pkg,
		seen:    map[types.Type]bool{},
		queued:  map[*types.TypeName]bool{},
		exposed: map[string]bool{},
	}
	w.str("api")
	w.str(pkg.Path())
	w.str(pkg.Name())

	scope := pkg.Scope()
	for _, name := range scope.Names() { // sorted
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		w.object(obj)
	}
	for len(w.queue) > 0 { // named types discovered while walking
		tn := w.queue[0]
		w.queue = w.queue[1:]
		w.named(tn)
	}

	paths := make([]string, 0, len(w.exposed))
	for p := range w.exposed {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	w.str("exposed")
	for _, p := range paths {
		w.str(p)
		w.str(depAPI(p))
	}
	return hex.EncodeToString(w.h.Sum(nil))
}

type apiWalker struct {
	h       hash.Hash
	pkg     *types.Package
	seen    map[types.Type]bool
	queued  map[*types.TypeName]bool
	queue   []*types.TypeName
	exposed map[string]bool
}

func (w *apiWalker) str(s string) {
	fmt.Fprintf(w.h, "%d:%s;", len(s), s)
}

func qualifier(p *types.Package) string { return p.Path() }

func (w *apiWalker) typ(t types.Type) string {
	w.collect(t)
	return types.TypeString(t, qualifier)
}

func (w *apiWalker) object(obj types.Object) {
	switch o := obj.(type) {
	case *types.Const:
		w.str("const")
		w.str(o.Name())
		w.str(w.typ(o.Type()))
	case *types.Var:
		w.str("var")
		w.str(o.Name())
		w.str(w.typ(o.Type()))
	case *types.Func:
		w.str("func")
		w.str(o.Name())
		w.str(w.typ(o.Type()))
	case *types.TypeName:
		w.str("type")
		w.str(o.Name())
		if o.IsAlias() {
			w.str("alias")
			w.str(w.typ(o.Type()))
			return
		}
		w.enqueue(o)
	}
}

func (w *apiWalker) enqueue(tn *types.TypeName) {
	if !w.queued[tn] {
		w.queued[tn] = true
		w.queue = append(w.queue, tn)
	}
}

// named writes the full description of a named type declared in this package.
func (w *apiWalker) named(tn *types.TypeName) {
	w.str("named")
	w.str(tn.Name())
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return
	}
	if tps := named.TypeParams(); tps != nil {
		for i := range tps.Len() {
			tp := tps.At(i)
			w.str("tparam")
			w.str(tp.Obj().Name())
			w.str(w.typ(tp.Constraint()))
		}
	}
	w.str("underlying")
	w.str(w.typ(named.Underlying()))

	methods := make([]*types.Func, 0, named.NumMethods())
	for i := range named.NumMethods() {
		methods = append(methods, named.Method(i))
	}
	sort.Slice(methods, func(i, j int) bool { return methods[i].Name() < methods[j].Name() })
	for _, m := range methods {
		sig := m.Signature()
		_, ptr := sig.Recv().Type().(*types.Pointer)
		w.str("method")
		w.str(m.Name())
		w.str(fmt.Sprint(ptr))
		w.str(w.typ(sig))
	}
}

// collect finds the named types inside t: those declared in this package are
// queued for a full description, those declared elsewhere mark the package as
// exposed.
func (w *apiWalker) collect(t types.Type) {
	if t == nil || w.seen[t] {
		return
	}
	w.seen[t] = true
	switch t := t.(type) {
	case *types.Named:
		w.note(t.Obj())
		if args := t.TypeArgs(); args != nil {
			for i := range args.Len() {
				w.collect(args.At(i))
			}
		}
	case *types.Alias:
		w.note(t.Obj())
		w.collect(t.Rhs())
		if args := t.TypeArgs(); args != nil {
			for i := range args.Len() {
				w.collect(args.At(i))
			}
		}
	case *types.Pointer:
		w.collect(t.Elem())
	case *types.Slice:
		w.collect(t.Elem())
	case *types.Array:
		w.collect(t.Elem())
	case *types.Chan:
		w.collect(t.Elem())
	case *types.Map:
		w.collect(t.Key())
		w.collect(t.Elem())
	case *types.Signature:
		w.collectTuple(t.Params())
		w.collectTuple(t.Results())
		for i := range t.TypeParams().Len() {
			w.collect(t.TypeParams().At(i).Constraint())
		}
	case *types.Struct:
		for i := range t.NumFields() {
			w.collect(t.Field(i).Type())
		}
	case *types.Interface:
		for i := range t.NumEmbeddeds() {
			w.collect(t.EmbeddedType(i))
		}
		for i := range t.NumExplicitMethods() {
			w.collect(t.ExplicitMethod(i).Type())
		}
	case *types.Union:
		for i := range t.Len() {
			w.collect(t.Term(i).Type())
		}
	case *types.TypeParam:
		w.collect(t.Constraint())
	}
}

func (w *apiWalker) collectTuple(t *types.Tuple) {
	for i := range t.Len() {
		w.collect(t.At(i).Type())
	}
}

func (w *apiWalker) note(tn *types.TypeName) {
	switch {
	case tn.Pkg() == nil:
	case tn.Pkg() == w.pkg:
		if tn.Parent() == w.pkg.Scope() {
			w.enqueue(tn)
		}
	default:
		w.exposed[tn.Pkg().Path()] = true
	}
}
