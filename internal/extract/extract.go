// Package extract turns one type-checked package into PackageFacts.
//
// Extract is a pure function of its input: it never touches storage and it
// is deterministic, so its output can be golden-tested and compared between an
// incremental and a from-scratch index.
package extract

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strings"

	"codeg/internal/model"
)

// Input is a type-checked package. Info must have Types, Defs, Uses,
// Implicits, Selections and Instances populated.
type Input struct {
	Fset  *token.FileSet
	Files []*ast.File
	Pkg   *types.Package
	Info  *types.Info

	// Errors are the package's type or load errors. The package is still
	// extracted from whatever Info holds and flagged HasErrors.
	Errors []model.Diagnostic

	// Sources are the package's real source files (absolute paths), as
	// go/packages reports them in GoFiles. When set, only these files appear
	// in the facts: cgo's translated files (whose //line directives point back
	// at the original) are mapped to the original, and files with no source
	// origin (_cgo_gotypes.go) are left out. Nil means every file in Files is a
	// source file.
	Sources []string

	// FilePath maps an absolute file name to the path recorded in the facts,
	// typically relative to the module root. Nil keeps the name unchanged.
	FilePath func(abs string) string

	// ModuleOf reports the module that provides an imported package, for
	// external stubs. Nil leaves the module empty.
	ModuleOf func(pkgPath string) (module, version string)
}

// Extract computes the facts of the package in in.
func Extract(in Input) (*model.PackageFacts, error) {
	if in.Fset == nil || in.Pkg == nil || in.Info == nil {
		return nil, fmt.Errorf("extract: Fset, Pkg and Info are required")
	}
	x := newExtractor(in)
	x.walkFiles()
	x.structure()
	return x.finish(), nil
}

type symbolObj struct {
	obj types.Object
	ref model.Ref
}

type extractor struct {
	in     Input
	namer  *model.Namer
	pkgKey model.Key
	files  []*ast.File // every parsed file, for naming
	walked []*ast.File // the files that appear in the facts
	facts  *model.PackageFacts

	// fileInfo maps a parsed file's own name to its entry in facts.Files.
	// mapped: positions must be read through //line directives (cgo).
	fileInfo   map[string]fileEntry
	displayIdx map[string]int32 // facts file name (before FilePath) -> index

	syms      map[model.Key]*model.Symbol
	defs      []symbolObj // objects with a definition, in walk order
	edges     map[model.Edge]struct{}
	externals map[model.Key]model.External
	occs      []model.Occurrence
	callSeq   int32
}

func newExtractor(in Input) *extractor {
	x := &extractor{
		in:         in,
		pkgKey:     model.Key{Pkg: in.Pkg.Path()},
		fileInfo:   map[string]fileEntry{},
		displayIdx: map[string]int32{},
		syms:       map[model.Key]*model.Symbol{},
		edges:      map[model.Edge]struct{}{},
		externals:  map[model.Key]model.External{},
	}
	x.files = slices.Clone(in.Files)
	slices.SortStableFunc(x.files, func(a, b *ast.File) int {
		return cmp.Compare(x.fileName(a), x.fileName(b))
	})
	x.namer = model.NewNamer(in.Fset, x.files, in.Pkg, in.Info)

	x.facts = &model.PackageFacts{
		Path:      in.Pkg.Path(),
		Name:      in.Pkg.Name(),
		HasErrors: len(in.Errors) > 0,
	}
	x.facts.Diagnostics = slices.Clone(in.Errors)
	x.collectFiles()
	x.addSymbol(model.Symbol{Key: x.pkgKey, Kind: model.KindPackage, Name: in.Pkg.Name(), Exported: true})
	for i, ff := range x.facts.Files {
		fk := model.Key{Pkg: x.pkgKey.Pkg, Name: "@" + baseName(ff.Path)}
		x.addSymbol(model.Symbol{Key: fk, Kind: model.KindFile, Name: baseName(ff.Path), Pos: model.Range{File: int32(i)}})
		x.addEdge(model.EdgeFile, x.pkgKey, fk)
	}
	return x
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

type fileEntry struct {
	idx    int32
	mapped bool
}

// collectFiles decides which parsed files appear in the facts and under which
// name, and fills facts.Files (sorted by path).
func (x *extractor) collectFiles() {
	var sources map[string]bool
	if x.in.Sources != nil {
		sources = map[string]bool{}
		for _, s := range x.in.Sources {
			sources[s] = true
		}
	}
	type display struct {
		name      string
		generated bool
		files     []string // parsed-file names that map here
	}
	byName := map[string]*display{}
	var order []*display
	mapped := map[string]bool{}
	for _, f := range x.files {
		own := x.fileName(f)
		name, isMapped := own, false
		if sources != nil && !sources[own] {
			// cgo: the translated file's //line directives name the original.
			orig := x.in.Fset.PositionFor(f.Package, true).Filename
			if !sources[orig] {
				continue // no source origin
			}
			name, isMapped = orig, true
		}
		d := byName[name]
		if d == nil {
			d = &display{name: name, generated: !isMapped && ast.IsGenerated(f)}
			byName[name] = d
			order = append(order, d)
		}
		d.files = append(d.files, own)
		mapped[own] = isMapped
		x.walked = append(x.walked, f)
	}
	slices.SortFunc(order, func(a, b *display) int { return cmp.Compare(a.name, b.name) })
	for i, d := range order {
		path := d.name
		if x.in.FilePath != nil {
			path = x.in.FilePath(d.name)
		}
		x.facts.Files = append(x.facts.Files, model.FileFact{
			Path: path, Generated: d.generated, Test: strings.HasSuffix(d.name, "_test.go"),
		})
		x.displayIdx[d.name] = int32(i)
		for _, own := range d.files {
			x.fileInfo[own] = fileEntry{idx: int32(i), mapped: mapped[own]}
		}
	}
}

// fileName is the file's real name; //line directives are ignored.
func (x *extractor) fileName(f *ast.File) string {
	return x.in.Fset.PositionFor(f.Package, false).Filename
}

// rng converts a source range. Line and column come through //line directives
// for cgo files (so they name the original source); offsets are always those of
// the parsed file, and are only meaningful as an ordering for mapped files.
func (x *extractor) rng(pos, end token.Pos) (model.Range, bool) {
	if !pos.IsValid() {
		return model.Range{}, false
	}
	pu := x.in.Fset.PositionFor(pos, false)
	fe, ok := x.fileInfo[pu.Filename]
	if !ok {
		return model.Range{}, false
	}
	if !end.IsValid() {
		end = pos
	}
	eu := x.in.Fset.PositionFor(end, false)
	p, e := pu, eu
	if fe.mapped {
		p, e = x.in.Fset.PositionFor(pos, true), x.in.Fset.PositionFor(end, true)
		if idx, ok := x.displayIdx[p.Filename]; !ok || idx != fe.idx {
			return model.Range{}, false // directive points somewhere other than the origin
		}
	}
	return model.Range{
		File: fe.idx, Start: int32(pu.Offset), End: int32(eu.Offset),
		Line: int32(p.Line), Col: int32(p.Column), EndLine: int32(e.Line), EndCol: int32(e.Column),
	}, true
}

func (x *extractor) qualifier(p *types.Package) string { return p.Path() }

func (x *extractor) typeString(t types.Type) string {
	if t == nil {
		return ""
	}
	return canonical(types.TypeString(t, x.qualifier))
}

// syntheticName matches the placeholder names (#rv1, #p0, ...) that compiler
// export data gives to unnamed parameters and results. Whether they appear
// depends on how a dependency's types were loaded, so they must not reach the
// facts: `#` cannot occur in a Go identifier, so anything of this shape is
// synthetic.
var syntheticName = regexp.MustCompile(`#\w+ `)

func canonical(s string) string {
	if !strings.Contains(s, "#") {
		return s
	}
	return syntheticName.ReplaceAllString(s, "")
}

func (x *extractor) addEdge(kind model.EdgeKind, from, to model.Key) {
	if from == (model.Key{}) || to == (model.Key{}) {
		return
	}
	x.edges[model.Edge{Kind: kind, From: from, To: to}] = struct{}{}
}

// addSymbol records s, keeping the first one seen but filling in a position
// and declaration range that were unknown when it was first created.
func (x *extractor) addSymbol(s model.Symbol) *model.Symbol {
	if old, ok := x.syms[s.Key]; ok {
		if old.Pos.Line == 0 && s.Pos.Line != 0 {
			old.Pos = s.Pos
		}
		if old.Decl.Line == 0 && s.Decl.Line != 0 {
			old.Decl = s.Decl
		}
		return old
	}
	c := s
	x.syms[s.Key] = &c
	return &c
}

// detail describes obj for a symbol or an external stub.
func (x *extractor) detail(obj types.Object) string {
	switch o := obj.(type) {
	case *types.TypeName:
		if tp, ok := o.Type().(*types.TypeParam); ok {
			return x.typeString(tp.Constraint())
		}
		if a, ok := o.Type().(*types.Alias); ok {
			return "= " + x.typeString(a.Rhs())
		}
		switch u := o.Type().Underlying().(type) {
		case *types.Struct:
			return "struct"
		case *types.Interface:
			return "interface"
		default:
			return x.typeString(u)
		}
	case *types.PkgName, *types.Label:
		return "" // neither has a type
	}
	return x.typeString(obj.Type())
}

func (x *extractor) noteExternal(obj types.Object, ref model.Ref) {
	if ref.Key.Pkg == "" || ref.Key.Pkg == x.pkgKey.Pkg {
		return
	}
	if _, ok := x.externals[ref.Key]; ok {
		return
	}
	e := model.External{Key: ref.Key, Kind: ref.Kind, Name: obj.Name(), Detail: x.detail(obj)}
	if _, isPkg := obj.(*types.PkgName); isPkg {
		e.Name = ref.Key.Pkg
	}
	if x.in.ModuleOf != nil {
		e.Module, e.Version = x.in.ModuleOf(ref.Key.Pkg)
	}
	x.externals[ref.Key] = e
}

// structure derives the structural edges and method sets from go/types.
func (x *extractor) structure() {
	for _, d := range x.defs {
		switch d.ref.Kind {
		case model.KindType:
			if named, ok := d.obj.Type().(*types.Named); ok {
				x.namedStructure(d.ref.Key, named)
			}
		case model.KindAlias:
			if a, ok := d.obj.Type().(*types.Alias); ok {
				x.typeParams(d.ref.Key, a.TypeParams())
				if named, ok := types.Unalias(a.Rhs()).(*types.Named); ok {
					if k, ok := x.namer.Key(named.Origin().Obj()); ok {
						x.addEdge(model.EdgeAliasOf, d.ref.Key, k)
					}
				}
			}
		case model.KindFunc, model.KindMethod:
			if fn, ok := d.obj.(*types.Func); ok {
				sig := fn.Signature()
				x.typeParams(d.ref.Key, sig.TypeParams())
				x.typeParams(d.ref.Key, sig.RecvTypeParams())
			}
		}
	}
}

func (x *extractor) typeParams(owner model.Key, list *types.TypeParamList) {
	for i := range list.Len() {
		if k, ok := x.namer.Key(list.At(i).Obj()); ok {
			x.addEdge(model.EdgeTypeParam, owner, k)
		}
	}
}

func (x *extractor) namedStructure(key model.Key, named *types.Named) {
	x.typeParams(key, named.TypeParams())
	switch u := named.Underlying().(type) {
	case *types.Struct:
		x.structFields(key, key, u)
	case *types.Interface:
		for i := range u.NumExplicitMethods() {
			if k, ok := x.namer.Key(u.ExplicitMethod(i)); ok {
				x.addEdge(model.EdgeHasMethod, key, k)
			}
		}
		for i := range u.NumEmbeddeds() {
			if k, ok := x.typeKey(u.EmbeddedType(i)); ok {
				x.addEdge(model.EdgeEmbeds, key, k)
			}
		}
	}
	if named.Obj().Pkg() == x.in.Pkg {
		for i := range named.NumMethods() {
			if k, ok := x.namer.Key(named.Method(i)); ok {
				x.addEdge(model.EdgeHasMethod, key, k)
			}
		}
	}
}

// structFields adds has_field edges from parent to each field of st and
// recurses into anonymous struct types. Embeds edges hang off the named type.
func (x *extractor) structFields(typ, parent model.Key, st *types.Struct) {
	for i := range st.NumFields() {
		f := st.Field(i)
		fk, ok := x.namer.Key(f)
		if !ok {
			continue
		}
		x.addEdge(model.EdgeHasField, parent, fk)
		if f.Embedded() && parent == typ {
			if k, ok := x.typeKey(f.Type()); ok {
				x.addEdge(model.EdgeEmbeds, typ, k)
			}
		}
		if inner := anonStruct(f.Type()); inner != nil {
			x.structFields(typ, fk, inner)
		}
	}
}

// typeKey returns the key of the named type t denotes, looking through
// pointers and aliases and using the generic origin for instantiations.
func (x *extractor) typeKey(t types.Type) (model.Key, bool) {
	t = types.Unalias(t)
	if p, ok := t.(*types.Pointer); ok {
		t = types.Unalias(p.Elem())
	}
	named, ok := t.(*types.Named)
	if !ok {
		return model.Key{}, false
	}
	return x.namer.Key(named.Origin().Obj())
}

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

// methodSets builds the flattened method set of each package-level,
// non-generic named type. Generic types are not part of v1 "implements".
func (x *extractor) methodSets() []model.MethodSet {
	var out []model.MethodSet
	for _, d := range x.defs {
		if d.ref.Kind != model.KindType || d.obj.Parent() != x.in.Pkg.Scope() {
			continue
		}
		named, ok := d.obj.Type().(*types.Named)
		if !ok || named.TypeParams().Len() > 0 {
			continue
		}
		ms := model.MethodSet{Type: d.ref.Key}
		if iface, ok := named.Underlying().(*types.Interface); ok {
			if !iface.IsMethodSet() {
				continue // constraint interface with a type set
			}
			ms.Interface = true
			set := types.NewMethodSet(named)
			for i := range set.Len() {
				ms.Methods = append(ms.Methods, x.methodEntry(set.At(i), false))
			}
		} else {
			val := types.NewMethodSet(named)
			ptr := types.NewMethodSet(types.NewPointer(named))
			for i := range ptr.Len() {
				fn := ptr.At(i).Obj().(*types.Func)
				ms.Methods = append(ms.Methods, x.methodEntry(ptr.At(i), val.Lookup(fn.Pkg(), fn.Name()) == nil))
			}
		}
		if len(ms.Methods) == 0 {
			continue
		}
		slices.SortFunc(ms.Methods, func(a, b model.MethodEntry) int { return cmp.Compare(a.Name, b.Name) })
		out = append(out, ms)
	}
	slices.SortFunc(out, func(a, b model.MethodSet) int { return a.Type.Compare(b.Type) })
	return out
}

func (x *extractor) methodEntry(sel *types.Selection, ptrOnly bool) model.MethodEntry {
	fn := sel.Obj().(*types.Func)
	name := fn.Name()
	if !fn.Exported() && fn.Pkg() != nil {
		name = fn.Pkg().Path() + "." + name
	}
	return model.MethodEntry{
		Name: name, Sig: x.typeString(unnamedSignature(fn.Signature())), PtrRecv: ptrOnly,
		Promoted: len(sel.Index()) > 1,
	}
}

// unnamedSignature drops the receiver and every parameter and result name, so
// two methods with identical types have identical strings: interface
// satisfaction ignores names.
func unnamedSignature(sig *types.Signature) *types.Signature {
	strip := func(t *types.Tuple) *types.Tuple {
		if t == nil {
			return nil
		}
		vars := make([]*types.Var, t.Len())
		for i := range vars {
			vars[i] = types.NewVar(token.NoPos, nil, "", t.At(i).Type())
		}
		return types.NewTuple(vars...)
	}
	return types.NewSignatureType(nil, nil, nil, strip(sig.Params()), strip(sig.Results()), sig.Variadic())
}

func (x *extractor) finish() *model.PackageFacts {
	f := x.facts
	for _, s := range x.syms {
		f.Symbols = append(f.Symbols, *s)
	}
	slices.SortFunc(f.Symbols, func(a, b model.Symbol) int { return a.Key.Compare(b.Key) })

	f.Occurrences = x.occs
	slices.SortStableFunc(f.Occurrences, func(a, b model.Occurrence) int {
		// By line and column, not offset: for cgo files the offsets belong to
		// the translated file and only the lines name the original.
		return cmp.Or(
			cmp.Compare(a.File, b.File),
			cmp.Compare(a.Line, b.Line),
			cmp.Compare(a.Col, b.Col),
			cmp.Compare(a.EndLine, b.EndLine),
			cmp.Compare(a.EndCol, b.EndCol),
			cmp.Compare(a.Role, b.Role),
			a.Symbol.Compare(b.Symbol),
		)
	})

	for e := range x.edges {
		f.Edges = append(f.Edges, e)
	}
	slices.SortFunc(f.Edges, func(a, b model.Edge) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), a.From.Compare(b.From), a.To.Compare(b.To))
	})

	f.MethodSets = x.methodSets()

	for _, e := range x.externals {
		f.Externals = append(f.Externals, e)
	}
	slices.SortFunc(f.Externals, func(a, b model.External) int { return a.Key.Compare(b.Key) })

	seen := map[string]bool{}
	for _, imp := range x.in.Pkg.Imports() {
		if !seen[imp.Path()] {
			seen[imp.Path()] = true
			f.Imports = append(f.Imports, imp.Path())
		}
	}
	slices.Sort(f.Imports)
	return f
}
