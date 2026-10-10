package model

import (
	"go/ast"
	"go/token"
	"go/types"
)

// Key grammar (Name part; the package is carried separately):
//
//	Func, Type, Var, Const   Name
//	Method                   Recv.Method              (pointer-ness and type arguments ignored)
//	Field                    Type.Field               (Type.~objectpath for anonymous structs)
//	init, blank              init#N, _#N              (N: source order in the package)
//	Type parameter           Owner[T]
//	Closure                  Parent$N                 (N: order among the parent's closures)
//	Param, result, receiver  Func#param0, Func#result0, Func#recv
//	Local variable           Func::name@N             (N: nth declaration of name in that function)
//	Local type or const      Func.Name@N
//	Label                    Func#label:name          (label names are unique within a function)
//
// Everything declared inside a function body is only unique within one
// indexing generation: nothing outside its function can refer to it.

// Namer computes keys for the objects of one type-checked package.
//
// Package-level objects, methods and fields need only go/types, so the Namer
// names those for any package (dependencies included) using a lazily built
// per-package index. Objects declared inside function bodies, plus init and
// blank declarations, need the owning package's syntax; the Namer records
// them when built from the package's files.
type Namer struct {
	fset *token.FileSet
	pkg  *types.Package
	info *types.Info

	local    map[types.Object]Ref
	closures map[*ast.FuncLit]Key
	idx      map[*types.Package]*pkgIndex
}

// NewNamer returns a Namer for pkg. files and info may be nil, in which case
// only types-only keys are available.
func NewNamer(fset *token.FileSet, files []*ast.File, pkg *types.Package, info *types.Info) *Namer {
	n := &Namer{
		fset:     fset,
		pkg:      pkg,
		info:     info,
		local:    map[types.Object]Ref{},
		closures: map[*ast.FuncLit]Key{},
		idx:      map[*types.Package]*pkgIndex{},
	}
	if info != nil && len(files) > 0 {
		n.walkFiles(files)
	}
	return n
}

// Ref returns the key and kind for obj. It reports false for objects that
// have no symbol: labels, and objects that are not reachable by name (for
// example parameters of a function type expression).
func (n *Namer) Ref(obj types.Object) (Ref, bool) {
	if obj == nil {
		return Ref{}, false
	}
	if r, ok := n.local[obj]; ok {
		return r, true
	}
	return n.typesRef(obj)
}

// Key is Ref without the kind.
func (n *Namer) Key(obj types.Object) (Key, bool) {
	r, ok := n.Ref(obj)
	return r.Key, ok
}

// Closure returns the key of a function literal of the package the Namer
// was built from.
func (n *Namer) Closure(lit *ast.FuncLit) (Key, bool) {
	k, ok := n.closures[lit]
	return k, ok
}

// isPkgLevel reports whether o is a named package-level declaration. init
// functions and blank declarations are excluded: nothing can refer to them by
// name, and several can exist, so only the syntax walk (which numbers them)
// can key them.
func isPkgLevel(o types.Object) bool {
	if o.Pkg() == nil || o.Parent() != o.Pkg().Scope() {
		return false
	}
	if o.Name() == "_" {
		return false
	}
	_, isFunc := o.(*types.Func)
	return !(isFunc && o.Name() == "init")
}

func isUniverse(o types.Object) bool {
	return o.Pkg() == nil && o.Parent() == types.Universe
}

// typesRef names obj using go/types information only.
func (n *Namer) typesRef(obj types.Object) (Ref, bool) {
	switch o := obj.(type) {
	case *types.PkgName:
		return Ref{Key{Pkg: o.Imported().Path()}, KindPackage}, true
	case *types.Builtin:
		return Ref{Key{Name: o.Name()}, KindFunc}, true
	case *types.Nil:
		return Ref{Key{Name: "nil"}, KindVar}, true
	case *types.Func:
		o = o.Origin()
		sig := o.Signature()
		if sig.Recv() != nil {
			return n.methodRef(o, sig)
		}
		if isPkgLevel(o) {
			return Ref{Key{o.Pkg().Path(), o.Name()}, KindFunc}, true
		}
	case *types.Var:
		o = o.Origin()
		if o.IsField() {
			return n.fieldRef(o)
		}
		if isPkgLevel(o) {
			return Ref{Key{o.Pkg().Path(), o.Name()}, KindVar}, true
		}
	case *types.Const:
		switch {
		case isUniverse(o):
			return Ref{Key{Name: o.Name()}, KindConst}, true
		case isPkgLevel(o):
			return Ref{Key{o.Pkg().Path(), o.Name()}, KindConst}, true
		}
	case *types.TypeName:
		kind := KindType
		if o.IsAlias() {
			kind = KindAlias
		}
		switch {
		case isUniverse(o):
			return Ref{Key{Name: o.Name()}, kind}, true
		case isPkgLevel(o):
			return Ref{Key{o.Pkg().Path(), o.Name()}, kind}, true
		}
		if _, ok := o.Type().(*types.TypeParam); ok {
			return n.typeParamRef(o)
		}
	}
	return Ref{}, false
}

func (n *Namer) methodRef(m *types.Func, sig *types.Signature) (Ref, bool) {
	t := types.Unalias(sig.Recv().Type())
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	if named, ok := t.(*types.Named); ok {
		base := named.Origin().Obj()
		name := base.Name() + "." + m.Name()
		switch {
		case isUniverse(base): // error.Error
			return Ref{Key{Name: name}, KindMethod}, true
		case isPkgLevel(base):
			return Ref{Key{base.Pkg().Path(), name}, KindMethod}, true
		}
	}
	// Method of an anonymous interface, e.g. a type parameter constraint.
	return n.objectPathRef(m, KindMethod)
}

func (n *Namer) fieldRef(f *types.Var) (Ref, bool) {
	if f.Pkg() == nil {
		return Ref{}, false
	}
	if name, ok := n.index(f.Pkg()).fieldNames[f]; ok {
		return Ref{Key{f.Pkg().Path(), name}, KindField}, true
	}
	return n.objectPathRef(f, KindField)
}

func (n *Namer) typeParamRef(tp *types.TypeName) (Ref, bool) {
	if tp.Pkg() == nil {
		return Ref{}, false
	}
	owner, ok := n.index(tp.Pkg()).tparamOwner[tp]
	if !ok {
		return Ref{}, false
	}
	or, ok := n.Ref(owner.obj)
	if !ok {
		return Ref{}, false
	}
	return Ref{Key{or.Key.Pkg, or.Key.Name + "[" + owner.name + "]"}, KindTypeParam}, true
}

// objectPathRef names an object that has no name-based key by its objectpath.
// The path is stable for anything reachable from a package-level declaration.
func (n *Namer) objectPathRef(obj types.Object, kind Kind) (Ref, bool) {
	if obj.Pkg() == nil {
		return Ref{}, false
	}
	p, err := n.index(obj.Pkg()).enc.For(obj)
	if err != nil {
		return Ref{}, false
	}
	return Ref{Key{obj.Pkg().Path(), "~" + string(p)}, kind}, true
}
