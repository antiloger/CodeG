package model

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/types"
	"slices"
)

// walkState is the naming context of one function body: a declared function,
// a closure, or a package-level variable initializer.
type walkState struct {
	key      Key
	closures int
	anon     int
	decls    map[string]int // per name: how many locals/types/consts declared so far
}

func newWalkState(key Key) *walkState {
	return &walkState{key: key, decls: map[string]int{}}
}

func (st *walkState) child(sep, format string, args ...any) Key {
	return Key{st.key.Pkg, st.key.Name + sep + fmt.Sprintf(format, args...)}
}

// declare returns the key for the next declaration of name in this function.
func (st *walkState) declare(sep, name string) Key {
	c := st.decls[name]
	st.decls[name]++
	return st.child(sep, "%s@%d", name, c)
}

func (n *Namer) set(obj types.Object, key Key, kind Kind) {
	if obj == nil {
		return
	}
	if _, dup := n.local[obj]; !dup {
		n.local[obj] = Ref{key, kind}
	}
}

// walkFiles records keys for init/blank declarations and for everything
// declared inside function bodies. Files are visited in filename order and
// declarations in source order so ordinals are deterministic.
func (n *Namer) walkFiles(files []*ast.File) {
	files = slices.Clone(files)
	slices.SortStableFunc(files, func(a, b *ast.File) int {
		return cmp.Compare(n.fset.PositionFor(a.Package, false).Filename, n.fset.PositionFor(b.Package, false).Filename)
	})
	special := map[string]int{} // "init" and "_" ordinals, package-wide
	pkgPath := n.pkg.Path()
	special1 := func(name string) Key {
		k := Key{pkgPath, fmt.Sprintf("%s#%d", name, special[name])}
		special[name]++
		return k
	}

	for _, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				obj, _ := n.info.Defs[d.Name].(*types.Func)
				if obj == nil {
					continue
				}
				var key Key
				switch {
				case d.Recv == nil && (d.Name.Name == "init" || d.Name.Name == "_"):
					key = special1(d.Name.Name)
					n.set(obj, key, KindFunc)
				default:
					r, ok := n.typesRef(obj)
					if !ok {
						continue
					}
					key = r.Key
				}
				n.registerSig(key, obj.Signature())
				w := &walker{n, newWalkState(key), false}
				ast.Walk(w, d.Type) // anonymous types in the signature
				if d.Body != nil {
					ast.Walk(w, d.Body)
				}

			case *ast.GenDecl:
				for _, spec := range d.Specs {
					n.walkSpec(spec, special1)
				}
			}
		}
	}
}

func (n *Namer) walkSpec(spec ast.Spec, special1 func(string) Key) {
	switch s := spec.(type) {
	case *ast.TypeSpec:
		if s.Name.Name == "_" {
			if obj, ok := n.info.Defs[s.Name].(*types.TypeName); ok {
				kind := KindType
				if obj.IsAlias() {
					kind = KindAlias
				}
				n.set(obj, special1("_"), kind)
			}
		}
	case *ast.ValueSpec:
		keys := make([]Key, len(s.Names))
		states := make([]*walkState, len(s.Names))
		for i, name := range s.Names {
			obj := n.info.Defs[name]
			if obj == nil {
				continue
			}
			if name.Name == "_" {
				kind := KindVar
				if _, ok := obj.(*types.Const); ok {
					kind = KindConst
				}
				keys[i] = special1("_")
				n.set(obj, keys[i], kind)
			} else if r, ok := n.typesRef(obj); ok {
				keys[i] = r.Key
			}
		}
		for i, k := range keys {
			if k != (Key{}) {
				states[i] = newWalkState(k)
			}
		}
		if s.Type != nil && states[0] != nil {
			ast.Walk(&walker{n, states[0], false}, s.Type) // anonymous types in the declared type
		}
		// Closures in an initializer belong to the variable they initialize.
		for i, v := range s.Values {
			st := states[0]
			if len(s.Values) == len(s.Names) {
				st = states[i]
			}
			if st != nil {
				ast.Walk(&walker{n, st, false}, v)
			}
		}
	}
}

// registerSig names the receiver, parameters and results of a function.
// Signatures own their variables, so this covers unnamed and blank ones.
func (n *Namer) registerSig(owner Key, sig *types.Signature) {
	if sig == nil {
		return
	}
	if r := sig.Recv(); r != nil {
		n.set(r, Key{owner.Pkg, owner.Name + "#recv"}, KindParam)
	}
	for i := range sig.Params().Len() {
		n.set(sig.Params().At(i), Key{owner.Pkg, fmt.Sprintf("%s#param%d", owner.Name, i)}, KindParam)
	}
	for i := range sig.Results().Len() {
		n.set(sig.Results().At(i), Key{owner.Pkg, fmt.Sprintf("%s#result%d", owner.Name, i)}, KindResult)
	}
}

// walker is an ast.Visitor that assigns keys in source order.
type walker struct {
	n     *Namer
	st    *walkState
	inSig bool // inside a function type expression: its names are not symbols
}

func (w *walker) Visit(node ast.Node) ast.Visitor {
	n, st := w.n, w.st
	switch x := node.(type) {
	case *ast.FuncLit:
		key := st.child("$", "%d", st.closures)
		st.closures++
		n.closures[x] = key
		child := newWalkState(key)
		n.registerSig(key, n.sigOf(x))
		return &walker{n, child, false}

	case *ast.FuncType:
		return &walker{n, st, true}

	case *ast.TypeSpec:
		obj, ok := n.info.Defs[x.Name].(*types.TypeName)
		if !ok {
			return w
		}
		kind := KindType
		if obj.IsAlias() {
			kind = KindAlias
		}
		key := st.declare(".", x.Name.Name)
		n.set(obj, key, kind)
		w.nameMembers(x.Type, key, true)

	case *ast.StructType, *ast.InterfaceType:
		// Members of a local named type were handled at its TypeSpec.
		w.nameMembers(x.(ast.Expr), Key{}, false)

	case *ast.TypeSwitchStmt:
		// switch v := x.(type): go/types makes one implicit variable per
		// clause, but in the source it is one variable, so all clauses share
		// one key (as SCIP does). Its type differs per clause.
		if as, ok := x.Assign.(*ast.AssignStmt); ok && len(as.Lhs) == 1 {
			if id, ok := as.Lhs[0].(*ast.Ident); ok {
				var key Key
				have := false
				for _, c := range x.Body.List {
					if v, ok := n.info.Implicits[c].(*types.Var); ok {
						if !have {
							key, have = st.declare("::", id.Name), true
						}
						n.set(v, key, KindLocal)
					}
				}
			}
		}

	case *ast.Ident:
		obj := n.info.Defs[x]
		if obj == nil {
			return w
		}
		if _, done := n.local[obj]; done {
			return w
		}
		switch o := obj.(type) {
		case *types.Label:
			n.set(o, Key{st.key.Pkg, st.key.Name + "#label:" + o.Name()}, KindLabel)
		case *types.Var:
			if !o.IsField() && !w.inSig {
				n.set(o, st.declare("::", o.Name()), KindLocal)
			}
		case *types.Const:
			n.set(o, st.declare(".", o.Name()), KindConst)
		}
	}
	return w
}

func (n *Namer) sigOf(lit *ast.FuncLit) *types.Signature {
	sig, _ := n.info.TypeOf(lit).(*types.Signature)
	return sig
}

// nameMembers names the fields of a struct type expression or the explicit
// methods of an interface type expression. If named, parent is the key of the
// local type being declared. Otherwise the type is anonymous and its members
// use their objectpath key when they have one, and a function-relative key
// when they do not.
func (w *walker) nameMembers(expr ast.Expr, parent Key, named bool) {
	n, st := w.n, w.st
	switch expr.(type) {
	case *ast.StructType, *ast.InterfaceType:
	default:
		return
	}
	t := n.info.TypeOf(expr)
	if t == nil {
		return
	}
	// A TypeSpec names its members, then the Visit of the same expression
	// finds them already set.
	var anonKey Key
	anon := func() Key {
		if anonKey == (Key{}) {
			label := "~struct"
			if _, ok := t.(*types.Interface); ok {
				label = "~iface"
			}
			anonKey = st.child(".", "%s@%d", label, st.anon)
			st.anon++
		}
		return anonKey
	}
	owner := func() Key {
		if named {
			return parent
		}
		return anon()
	}
	switch t := t.(type) {
	case *types.Struct:
		for i := range t.NumFields() {
			f := t.Field(i)
			if _, done := n.local[f]; done {
				continue
			}
			if !named {
				if _, ok := n.typesRef(f); ok {
					continue
				}
			}
			name := f.Name()
			if name == "_" {
				name = fmt.Sprintf("_#%d", i)
			}
			o := owner()
			n.set(f, Key{o.Pkg, o.Name + "." + name}, KindField)
		}
	case *types.Interface:
		for i := range t.NumExplicitMethods() {
			m := t.ExplicitMethod(i)
			if _, done := n.local[m]; done {
				continue
			}
			if !named {
				if _, ok := n.typesRef(m); ok {
					continue
				}
			}
			o := owner()
			n.set(m, Key{o.Pkg, o.Name + "." + m.Name()}, KindMethod)
		}
	}
}
