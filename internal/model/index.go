package model

import (
	"fmt"
	"go/types"

	"golang.org/x/tools/go/types/objectpath"
)

// pkgIndex holds the lookups that go/types cannot answer from an object
// alone: which named type declares a field, and which declaration owns a type
// parameter. It is built lazily from a package's scope, so it works for
// dependencies loaded from export data too.
type pkgIndex struct {
	fieldNames  map[*types.Var]string // "Type.Field"
	tparamOwner map[*types.TypeName]tparamOwner
	enc         objectpath.Encoder
}

type tparamOwner struct {
	obj  types.Object // the func, type or method that declares the parameter
	name string       // "T", or "_#i" for blank parameters
}

func (n *Namer) index(pkg *types.Package) *pkgIndex {
	if ix, ok := n.idx[pkg]; ok {
		return ix
	}
	ix := buildIndex(pkg)
	n.idx[pkg] = ix
	return ix
}

func buildIndex(pkg *types.Package) *pkgIndex {
	ix := &pkgIndex{
		fieldNames:  map[*types.Var]string{},
		tparamOwner: map[*types.TypeName]tparamOwner{},
	}

	type candidate struct {
		owner *types.TypeName
		name  string
	}
	cands := map[*types.Var][]candidate{}
	var order []*types.Var // first-seen order, so the pass below is deterministic

	scope := pkg.Scope()
	for _, name := range scope.Names() { // sorted
		switch obj := scope.Lookup(name).(type) {
		case *types.Func:
			ix.addTypeParams(obj, obj.Signature().TypeParams())
		case *types.TypeName:
			if a, ok := obj.Type().(*types.Alias); ok {
				ix.addTypeParams(obj, a.TypeParams())
				continue
			}
			named, ok := obj.Type().(*types.Named)
			if !ok {
				continue
			}
			ix.addTypeParams(obj, named.TypeParams())
			for i := range named.NumMethods() {
				m := named.Method(i)
				ix.addTypeParams(m, m.Signature().RecvTypeParams())
			}
			if st, ok := named.Underlying().(*types.Struct); ok {
				walkStruct(obj, obj.Name(), st, func(f *types.Var, name string) {
					if _, seen := cands[f]; !seen {
						order = append(order, f)
					}
					cands[f] = append(cands[f], candidate{obj, name})
				})
			}
		}
	}

	// `type B A` gives B the same underlying struct, and so the same field
	// objects, as A. The declaring type is the one whose name is closest
	// before the field in the source; fall back to the first by name.
	for _, f := range order {
		cs := cands[f]
		best := cs[0]
		if len(cs) > 1 {
			for _, c := range cs[1:] {
				p := c.owner.Pos()
				if p.IsValid() && p <= f.Pos() && (!best.owner.Pos().IsValid() || best.owner.Pos() > f.Pos() || p > best.owner.Pos()) {
					best = c
				}
			}
		}
		ix.fieldNames[f] = best.name
	}
	return ix
}

func (ix *pkgIndex) addTypeParams(owner types.Object, list *types.TypeParamList) {
	for i := range list.Len() {
		tn := list.At(i).Obj()
		name := tn.Name()
		if name == "_" {
			name = fmt.Sprintf("_#%d", i)
		}
		ix.tparamOwner[tn] = tparamOwner{owner, name}
	}
}

// walkStruct calls visit for every field of st, and recursively for the fields
// of anonymous struct types nested in them, naming each prefix.Field.Field.
// Fields of anonymous structs get name-based keys this way instead of
// objectpaths, whose choice of path can depend on unrelated declarations.
func walkStruct(owner *types.TypeName, prefix string, st *types.Struct, visit func(*types.Var, string)) {
	for i := range st.NumFields() {
		f := st.Field(i)
		fname := f.Name()
		if fname == "_" {
			fname = fmt.Sprintf("_#%d", i)
		}
		name := prefix + "." + fname
		visit(f, name)
		if inner := anonStruct(f.Type()); inner != nil {
			walkStruct(owner, name, inner, visit)
		}
	}
}

// anonStruct returns the unnamed struct type that t is, or is a pointer,
// slice, array, channel or map value of, if any.
func anonStruct(t types.Type) *types.Struct {
	for {
		switch u := t.(type) {
		case *types.Struct:
			return u
		case *types.Pointer:
			t = u.Elem()
		case *types.Slice:
			t = u.Elem()
		case *types.Array:
			t = u.Elem()
		case *types.Chan:
			t = u.Elem()
		case *types.Map:
			t = u.Elem()
		default:
			return nil
		}
	}
}
