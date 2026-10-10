package extract

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"codeg/internal/model"
)

type enclosing struct {
	key model.Key
	sig *types.Signature
}

type argInfo struct {
	call  int32
	index int32
}

type calleeInfo struct {
	call  int32
	flags model.Flags // FlagDefer / FlagGo
}

// walker assigns occurrences while traversing a file. It is an ast.Visitor
// that keeps a parent stack and a stack of undo functions, so per-node state
// (loop depth, enclosing function, ...) is restored when the node is left.
type walker struct {
	x     *extractor
	info  *types.Info
	stack []ast.Node
	undo  []func()

	encl    []enclosing
	depth   int // closure nesting depth
	loop    int
	branch  int
	inDefer bool
	inGo    bool

	declDepth map[types.Object]int // closure depth at which a local was declared
	callee    map[*ast.Ident]calleeInfo
	argOf     map[*ast.Ident]argInfo
	flowTo    map[*ast.Ident]model.Key
	construct map[*ast.Ident]bool
	declKey   map[*ast.FuncDecl]enclosing
}

func (x *extractor) walkFiles() {
	w := &walker{
		x:         x,
		info:      x.in.Info,
		encl:      []enclosing{{key: x.pkgKey}},
		declDepth: map[types.Object]int{},
		callee:    map[*ast.Ident]calleeInfo{},
		argOf:     map[*ast.Ident]argInfo{},
		flowTo:    map[*ast.Ident]model.Key{},
		construct: map[*ast.Ident]bool{},
		declKey:   map[*ast.FuncDecl]enclosing{},
	}
	for _, f := range x.walked {
		ast.Walk(w, f)
	}
}

func (w *walker) Visit(n ast.Node) ast.Visitor {
	if n == nil {
		last := len(w.stack) - 1
		w.undo[last]()
		w.stack, w.undo = w.stack[:last], w.undo[:last]
		return nil
	}
	var parent, grand ast.Node
	if k := len(w.stack); k > 0 {
		parent = w.stack[k-1]
		if k > 1 {
			grand = w.stack[k-2]
		}
	}
	w.stack = append(w.stack, n)
	w.undo = append(w.undo, w.enter(n, parent, grand))
	if id, ok := n.(*ast.Ident); ok {
		w.ident(id)
	}
	return w
}

func (w *walker) cur() enclosing { return w.encl[len(w.encl)-1] }

func nop() {}

// enter updates the traversal state for node n and returns the function that
// restores it. It also emits the occurrences that belong to n itself rather
// than to an identifier: imports, unknown calls and closures.
func (w *walker) enter(n, parent, grand ast.Node) func() {
	var undos []func()
	push := func(e enclosing) {
		w.encl = append(w.encl, e)
		undos = append(undos, func() { w.encl = w.encl[:len(w.encl)-1] })
	}
	bump := func(p *int) {
		*p++
		undos = append(undos, func() { *p-- })
	}

	// State implied by the parent/child relationship.
	switch p := parent.(type) {
	case *ast.ForStmt:
		if n == ast.Node(p.Cond) || n == ast.Node(p.Post) || n == ast.Node(p.Body) {
			bump(&w.loop)
		}
	case *ast.RangeStmt:
		if n == ast.Node(p.Body) {
			bump(&w.loop)
		}
	case *ast.IfStmt:
		if n == ast.Node(p.Body) || n == ast.Node(p.Else) {
			bump(&w.branch)
		}
	case *ast.CaseClause, *ast.CommClause:
		bump(&w.branch)
	case *ast.BinaryExpr:
		if (p.Op == token.LAND || p.Op == token.LOR) && n == ast.Node(p.Y) {
			bump(&w.branch)
		}
	case *ast.FuncDecl:
		if e, ok := w.declKey[p]; ok && n != ast.Node(p.Name) && n != ast.Node(p.Doc) {
			push(e)
		}
	case *ast.ValueSpec:
		if len(w.encl) == 1 && n != nameNode(p, n) {
			if e, ok := w.valueOwner(p, n); ok {
				push(e)
			}
		}
	}

	switch n := n.(type) {
	case *ast.FuncDecl:
		w.enterFuncDecl(n)

	case *ast.FuncLit:
		key, ok := w.x.namer.Closure(n)
		if !ok {
			break
		}
		sig, _ := w.info.TypeOf(n).(*types.Signature)
		w.declareClosure(n, key, sig)
		w.emit(n.Type.Func, n.Type.Func+token.Pos(len("func")), key, model.RoleDefinition, emitOpts{})
		if call, ok := parent.(*ast.CallExpr); ok && call.Fun == ast.Expr(n) {
			switch grand.(type) {
			case *ast.DeferStmt:
				w.setBool(&w.inDefer, true, &undos)
			case *ast.GoStmt:
				w.setBool(&w.inGo, true, &undos)
			}
		}
		push(enclosing{key, sig})
		bump(&w.depth)
		saveLoop, saveBranch := w.loop, w.branch
		w.loop, w.branch = 0, 0
		undos = append(undos, func() { w.loop, w.branch = saveLoop, saveBranch })
		w.registerSig(key, sig)

	case *ast.CallExpr:
		w.enterCall(n, parent)

	case *ast.AssignStmt:
		if len(n.Lhs) == len(n.Rhs) {
			for i := range n.Lhs {
				w.flow(n.Lhs[i], n.Rhs[i])
			}
		}

	case *ast.ValueSpec:
		if len(n.Names) == len(n.Values) {
			for i := range n.Names {
				w.flow(n.Names[i], n.Values[i])
			}
		}

	case *ast.ReturnStmt:
		if sig := w.cur().sig; sig != nil && sig.Results().Len() == len(n.Results) {
			for i, r := range n.Results {
				if id := head(r); id != nil {
					if k, ok := w.x.namer.Key(sig.Results().At(i)); ok {
						w.flowTo[id] = k
					}
				}
			}
		}

	case *ast.CompositeLit:
		for _, e := range n.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if id, ok := kv.Key.(*ast.Ident); ok {
				if f, ok := w.info.Uses[id].(*types.Var); ok && f.IsField() {
					if r := head(kv.Value); r != nil {
						if k, ok := w.x.namer.Key(f); ok {
							w.flowTo[r] = k
						}
					}
				}
			}
		}

	case *ast.TypeSwitchStmt:
		w.typeSwitchDefs(n)

	case *ast.ImportSpec:
		w.importSpec(n)
	}

	if len(undos) == 0 {
		return nop
	}
	return func() {
		for i := len(undos) - 1; i >= 0; i-- {
			undos[i]()
		}
	}
}

func (w *walker) setBool(p *bool, v bool, undos *[]func()) {
	old := *p
	*p = v
	*undos = append(*undos, func() { *p = old })
}

// nameNode returns the name of spec that n is, or nil, so a ValueSpec's
// names are not treated as part of its initializer.
func nameNode(spec *ast.ValueSpec, n ast.Node) ast.Node {
	for _, name := range spec.Names {
		if n == ast.Node(name) {
			return n
		}
	}
	return nil
}

// valueOwner finds the package-level variable that owns the initializer or
// type expression n of spec: closures inside belong to it.
func (w *walker) valueOwner(spec *ast.ValueSpec, n ast.Node) (enclosing, bool) {
	idx := 0
	if len(spec.Values) == len(spec.Names) {
		for i, v := range spec.Values {
			if n == ast.Node(v) {
				idx = i
			}
		}
	}
	if idx >= len(spec.Names) {
		return enclosing{}, false
	}
	obj := w.info.Defs[spec.Names[idx]]
	if obj == nil {
		return enclosing{}, false
	}
	k, ok := w.x.namer.Key(obj)
	return enclosing{key: k}, ok
}

func (w *walker) enterFuncDecl(d *ast.FuncDecl) {
	obj, _ := w.info.Defs[d.Name].(*types.Func)
	if obj == nil {
		return
	}
	key, ok := w.x.namer.Key(obj)
	if !ok {
		return
	}
	sig := obj.Signature()
	w.declKey[d] = enclosing{key, sig}
	w.registerSig(key, sig)
}

// registerSig creates symbols for the receiver, parameters and results, so
// unnamed ones exist for the has_param and has_result edges too.
func (w *walker) registerSig(owner model.Key, sig *types.Signature) {
	if sig == nil {
		return
	}
	add := func(v *types.Var, kind model.Kind, edge model.EdgeKind) {
		k, ok := w.x.namer.Key(v)
		if !ok {
			return
		}
		w.x.addSymbol(model.Symbol{Key: k, Kind: kind, Name: v.Name(), Detail: w.x.typeString(v.Type())})
		w.x.addEdge(edge, owner, k)
	}
	if r := sig.Recv(); r != nil {
		add(r, model.KindParam, model.EdgeHasParam)
	}
	for i := range sig.Params().Len() {
		add(sig.Params().At(i), model.KindParam, model.EdgeHasParam)
	}
	for i := range sig.Results().Len() {
		add(sig.Results().At(i), model.KindResult, model.EdgeHasResult)
	}
}

func (w *walker) declareClosure(lit *ast.FuncLit, key model.Key, sig *types.Signature) {
	pos, _ := w.x.rng(lit.Type.Func, lit.Type.Func+token.Pos(len("func")))
	decl, _ := w.x.rng(lit.Pos(), lit.End())
	w.x.addSymbol(model.Symbol{Key: key, Kind: model.KindFuncLit, Detail: w.x.typeString(sig), Pos: pos, Decl: decl})
	w.x.addEdge(model.EdgeContains, w.cur().key, key)
}

// flow records `lhs = rhs` for the occurrences of both heads.
func (w *walker) flow(lhs, rhs ast.Expr) {
	if _, deref := unparen(lhs).(*ast.StarExpr); deref {
		return // *p = v writes to something unknown
	}
	l, r := head(lhs), head(rhs)
	if l == nil || r == nil || l.Name == "_" {
		return
	}
	obj := w.info.Defs[l]
	if obj == nil {
		obj = w.info.Uses[l]
	}
	if obj == nil {
		return
	}
	if k, ok := w.x.namer.Key(obj); ok {
		w.flowTo[r] = k
	}
}

// head returns the identifier whose occurrence stands for the value of e:
// the field for x.f, the variable for &x, *p, x[i] and x[i:j].
func head(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			return x.Sel
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return nil
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// typeHead returns the identifier naming the type in a type expression that is
// a plain (possibly qualified or instantiated) type name.
func typeHead(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.ParenExpr:
			e = x.X
		case *ast.SelectorExpr:
			return x.Sel
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		default:
			return nil
		}
	}
}

func unparen(e ast.Expr) ast.Expr {
	for {
		p, ok := e.(*ast.ParenExpr)
		if !ok {
			return e
		}
		e = p.X
	}
}

// calleeIdent returns the identifier a call's function expression resolves
// to, or nil if it is not a plain name.
func (w *walker) calleeIdent(fun ast.Expr) *ast.Ident {
	switch f := unparen(fun).(type) {
	case *ast.Ident:
		return f
	case *ast.SelectorExpr:
		return f.Sel
	case *ast.IndexExpr:
		if _, ok := w.info.TypeOf(f.X).(*types.Signature); ok {
			return w.calleeIdent(f.X) // explicit instantiation F[T](...)
		}
	case *ast.IndexListExpr:
		return w.calleeIdent(f.X)
	}
	return nil
}

func (w *walker) enterCall(call *ast.CallExpr, parent ast.Node) {
	fun := unparen(call.Fun)
	tv := w.info.Types[fun]
	switch {
	case tv.IsType():
		return // conversion
	case tv.IsBuiltin():
		w.builtin(call, fun)
		return
	}

	w.x.callSeq++
	id := w.x.callSeq
	var flags model.Flags
	switch parent.(type) {
	case *ast.DeferStmt:
		flags = model.FlagDefer
	case *ast.GoStmt:
		flags = model.FlagGo
	}

	for i, a := range call.Args {
		if h := head(a); h != nil {
			w.argOf[h] = argInfo{call: id, index: int32(i)}
		}
	}

	if lit, ok := fun.(*ast.FuncLit); ok {
		if key, ok := w.x.namer.Closure(lit); ok {
			w.emit(lit.Pos(), lit.Type.End(), key, model.RoleCall, emitOpts{dispatch: model.DispatchStatic, call: id, extra: flags})
		}
		return
	}
	if target := w.calleeIdent(fun); target != nil {
		w.callee[target] = calleeInfo{call: id, flags: flags}
		return
	}
	// Called value is computed: f()(), fns[i](), m["k"](). The target is
	// unknown, and recorded as such.
	w.emit(fun.Pos(), fun.End(), model.Key{}, model.RoleCall, emitOpts{dispatch: model.DispatchDynamic, call: id, extra: flags})
}

// builtin handles new(T) and make(T, ...), which construct a named type.
func (w *walker) builtin(call *ast.CallExpr, fun ast.Expr) {
	id, ok := fun.(*ast.Ident)
	if !ok || (id.Name != "new" && id.Name != "make") || len(call.Args) == 0 {
		return
	}
	if h := typeHead(call.Args[0]); h != nil {
		w.construct[h] = true
	}
}

func (w *walker) typeSwitchDefs(ts *ast.TypeSwitchStmt) {
	as, ok := ts.Assign.(*ast.AssignStmt)
	if !ok || len(as.Lhs) != 1 {
		return
	}
	id, ok := as.Lhs[0].(*ast.Ident)
	if !ok {
		return
	}
	// One symbol for the whole switch: define it once, and register every
	// clause's variable as declared here (for capture detection).
	first := true
	for _, c := range ts.Body.List {
		v, ok := w.info.Implicits[c].(*types.Var)
		if !ok {
			continue
		}
		w.declDepth[v] = w.depth
		if !first {
			continue
		}
		if ref, ok := w.x.namer.Ref(v); ok {
			w.define(id, v, ref)
			w.x.syms[ref.Key].Detail = "type varies per case"
			first = false
		}
	}
}

func (w *walker) importSpec(spec *ast.ImportSpec) {
	obj := w.info.Implicits[spec]
	if obj == nil && spec.Name != nil {
		obj = w.info.Defs[spec.Name]
	}
	pn, ok := obj.(*types.PkgName)
	if !ok {
		return
	}
	ref, ok := w.x.namer.Ref(pn)
	if !ok {
		return
	}
	w.x.noteExternal(pn, ref)
	w.emit(spec.Path.Pos(), spec.Path.End(), ref.Key, model.RoleImport, emitOpts{})
}

type emitOpts struct {
	dispatch model.Dispatch
	call     int32
	argOf    argInfo
	flow     model.Key
	targs    string
	extra    model.Flags
}

// emit records an occurrence with the current traversal context.
func (w *walker) emit(pos, end token.Pos, sym model.Key, role model.Role, o emitOpts) {
	r, ok := w.x.rng(pos, end)
	if !ok {
		return
	}
	var fl model.Flags
	if w.loop > 0 {
		fl |= model.FlagInLoop
	}
	if w.branch > 0 {
		fl |= model.FlagInBranch
	}
	if w.depth > 0 {
		fl |= model.FlagInClosure
	}
	if w.inDefer {
		fl |= model.FlagInDefer
	}
	if w.inGo {
		fl |= model.FlagInGo
	}
	w.x.occs = append(w.x.occs, model.Occurrence{
		Range:     r,
		Symbol:    sym,
		Role:      role,
		Dispatch:  o.dispatch,
		Flags:     fl | o.extra,
		Enclosing: w.cur().key,
		Call:      o.call,
		ArgOf:     o.argOf.call,
		ArgIndex:  o.argOf.index,
		FlowTo:    o.flow,
		TypeArgs:  o.targs,
	})
}

func (w *walker) ident(id *ast.Ident) {
	defObj := w.info.Defs[id]
	if defObj != nil {
		switch defObj.(type) {
		case *types.PkgName:
		default:
			if ref, ok := w.x.namer.Ref(defObj); ok {
				w.define(id, defObj, ref)
			}
		}
	}
	use := w.info.Uses[id]
	if use == nil || use == defObj {
		// Nothing more, or the definition itself (receiver type parameters
		// are recorded in both maps). An embedded field differs: Defs holds
		// the field and Uses the type it names.
		return
	}
	switch use.(type) {
	case *types.PkgName, *types.Nil, *types.Builtin:
		return
	}
	// Universe types and constants are not symbols of interest; universe
	// methods (error.Error) are.
	if use.Pkg() == nil {
		if _, ok := use.(*types.Func); !ok {
			return
		}
	}
	ref, ok := w.x.namer.Ref(use)
	if !ok {
		return
	}
	w.x.noteExternal(use, ref)
	w.noteSynthetic(use, ref)
	role, o := w.useRole(id, use, ref)
	if ci, ok := w.callee[id]; ok {
		role, o.call, o.extra = model.RoleCall, ci.call, o.extra|ci.flags
		o.dispatch = dispatchOf(use)
	}
	if ai, ok := w.argOf[id]; ok {
		o.argOf = ai
	}
	if k, ok := w.flowTo[id]; ok {
		o.flow = k
	}
	if inst, ok := w.info.Instances[id]; ok && inst.TypeArgs.Len() > 0 {
		var parts []string
		for i := range inst.TypeArgs.Len() {
			parts = append(parts, w.x.typeString(inst.TypeArgs.At(i)))
		}
		o.targs = strings.Join(parts, ", ")
	}
	if w.captured(use, ref) {
		o.extra |= model.FlagCaptured
	}
	w.emit(id.Pos(), id.End(), ref.Key, role, o)
}

func dispatchOf(obj types.Object) model.Dispatch {
	fn, ok := obj.(*types.Func)
	if !ok {
		return model.DispatchDynamic // variable, field or parameter of function type
	}
	if recv := fn.Signature().Recv(); recv != nil && types.IsInterface(recv.Type()) {
		return model.DispatchInterface
	}
	return model.DispatchStatic
}

func (w *walker) captured(obj types.Object, ref model.Ref) bool {
	switch ref.Kind {
	case model.KindLocal, model.KindParam, model.KindResult:
	default:
		return false
	}
	d, ok := w.declDepth[obj]
	return ok && d < w.depth
}

// define records the definition of obj at id.
func (w *walker) define(id *ast.Ident, obj types.Object, ref model.Ref) {
	// A blank identifier declares nothing anyone can refer to. A blank local
	// gets no symbol at all; blank package-level declarations and parameters
	// keep theirs (they own initializer closures, or take part in has_param
	// edges) but get no definition occurrence.
	blank := id.Name == "_"
	if blank && ref.Kind == model.KindLocal {
		return
	}
	pos, _ := w.x.rng(id.Pos(), id.End())
	sym := model.Symbol{
		Key: ref.Key, Kind: ref.Kind, Name: obj.Name(), Detail: w.x.detail(obj),
		Exported: obj.Exported(), Pos: pos, Decl: w.declRange(id),
	}
	w.x.addSymbol(sym)
	w.x.defs = append(w.x.defs, symbolObj{obj, ref})

	switch ref.Kind {
	case model.KindLocal, model.KindParam, model.KindResult:
		w.declDepth[obj] = w.depth
	}
	// contains: the package holds top-level declarations, a function holds
	// what is declared in its body. Members, params and results have their
	// own edges, from types.
	switch ref.Kind {
	case model.KindFunc, model.KindVar, model.KindConst, model.KindType, model.KindAlias, model.KindLocal, model.KindLabel:
		w.x.addEdge(model.EdgeContains, w.cur().key, ref.Key)
	case model.KindField, model.KindMethod:
		// Members of named types get has_field/has_method from types. Members
		// of anonymous structs and interfaces have no named owner, so the
		// declaration they appear in holds them.
		if isAnonMember(ref.Key.Name) {
			w.x.addEdge(model.EdgeContains, w.cur().key, ref.Key)
		}
	}
	if !blank {
		w.emit(id.Pos(), id.End(), ref.Key, model.RoleDefinition, emitOpts{})
	}
}

// isAnonMember reports whether a field or method key names a member of an
// anonymous struct or interface (objectpath or function-relative key).
func isAnonMember(name string) bool {
	return strings.HasPrefix(name, "~") || strings.Contains(name, ".~struct@") || strings.Contains(name, ".~iface@")
}

// declRange is the extent of the declaration that id names, if id is the
// name of a function, type or variable/constant spec.
func (w *walker) declRange(id *ast.Ident) model.Range {
	if len(w.stack) < 2 {
		return model.Range{}
	}
	var n ast.Node
	switch p := w.stack[len(w.stack)-2].(type) {
	case *ast.FuncDecl:
		if p.Name == id {
			n = p
		}
	case *ast.TypeSpec:
		if p.Name == id {
			n = p
		}
	case *ast.ValueSpec:
		n = p
	}
	if n == nil {
		return model.Range{}
	}
	r, _ := w.x.rng(n.Pos(), n.End())
	return r
}

// useRole classifies a reference to obj by its syntactic context.
func (w *walker) useRole(id *ast.Ident, obj types.Object, ref model.Ref) (model.Role, emitOpts) {
	var o emitOpts
	n := len(w.stack)
	eIdx := n - 1 // index of the expression this identifier stands for
	if sel, ok := w.stack[n-2].(*ast.SelectorExpr); ok && sel.Sel == id {
		eIdx = n - 2
	}

	switch obj.(type) {
	case *types.TypeName:
		if w.construct[id] || w.isLiteralType(eIdx) {
			return model.RoleConstruct, o
		}
		return model.RoleTypeUse, o
	case *types.Func:
		return model.RoleValueRef, o // callee handled by the caller
	case *types.Const, *types.Label:
		return model.RoleRead, o
	}

	// A variable or field. Climb through selectors, indexing, slicing and
	// parentheses to the expression that has a context of its own.
	i := eIdx
	implicitAddr := false
	for i >= 1 {
		switch p := w.stack[i-1].(type) {
		case *ast.SelectorExpr:
			if p.X != w.stack[i] {
				goto climbed
			}
			if w.implicitAddress(p) {
				implicitAddr = true
			}
		case *ast.IndexExpr:
			if p.X != w.stack[i] {
				goto climbed
			}
		case *ast.IndexListExpr:
			if p.X != w.stack[i] {
				goto climbed
			}
		case *ast.SliceExpr:
			if p.X != w.stack[i] {
				goto climbed
			}
		case *ast.ParenExpr:
		default:
			goto climbed
		}
		i--
	}
climbed:
	isLast := i == eIdx
	top := w.stack[i]
	var parent ast.Node
	if i >= 1 {
		parent = w.stack[i-1]
	}

	base := model.RoleRead
	switch p := parent.(type) {
	case *ast.AssignStmt:
		if containsExpr(p.Lhs, top) {
			if p.Tok == token.ASSIGN || p.Tok == token.DEFINE {
				base = model.RoleWrite
			} else {
				base = model.RoleReadWrite
			}
		}
	case *ast.IncDecStmt:
		base = model.RoleReadWrite
	case *ast.RangeStmt:
		if (top == ast.Node(p.Key) || top == ast.Node(p.Value)) && p.Tok == token.ASSIGN {
			base = model.RoleWrite
		}
	case *ast.UnaryExpr:
		if p.Op == token.AND {
			base = model.RoleAddressTaken
		}
	case *ast.KeyValueExpr:
		if isLast && p.Key == top.(ast.Expr) && i >= 2 {
			if _, ok := w.stack[i-2].(*ast.CompositeLit); ok {
				if v, ok := obj.(*types.Var); ok && v.IsField() {
					o.extra |= model.FlagInitKey
					base = model.RoleWrite
				}
			}
		}
	}

	switch {
	case base == model.RoleAddressTaken, implicitAddr && base == model.RoleRead:
		return model.RoleAddressTaken, o
	case !isLast && (base == model.RoleWrite || base == model.RoleReadWrite):
		return model.RoleWritePartial, o
	}
	return base, o
}

func containsExpr(list []ast.Expr, n ast.Node) bool {
	for _, e := range list {
		if ast.Node(e) == n {
			return true
		}
	}
	return false
}

// isLiteralType reports whether the expression at stack index i is the type
// of a composite literal: T{}, pkg.T{}, Box[int]{}, &T{}.
func (w *walker) isLiteralType(i int) bool {
	for i >= 1 {
		switch p := w.stack[i-1].(type) {
		case *ast.ParenExpr:
			i--
			continue
		case *ast.IndexExpr:
			if p.X != w.stack[i] { // a type argument, not the generic type
				return false
			}
			i--
			continue
		case *ast.IndexListExpr:
			if p.X != w.stack[i] {
				return false
			}
			i--
			continue
		case *ast.CompositeLit:
			return p.Type == w.stack[i]
		}
		return false
	}
	return false
}

// implicitAddress reports whether sel is a method value whose pointer
// receiver makes the compiler take the address of its (non-pointer) operand.
func (w *walker) implicitAddress(sel *ast.SelectorExpr) bool {
	s := w.info.Selections[sel]
	if s == nil || s.Kind() != types.MethodVal {
		return false
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok {
		return false
	}
	recv := fn.Signature().Recv()
	if recv == nil {
		return false
	}
	if _, ptr := types.Unalias(recv.Type()).(*types.Pointer); !ptr {
		return false
	}
	// The receiver operand is sel.X, or the last embedded field on the path
	// to the method. Only a non-pointer operand needs its address taken.
	t := w.info.TypeOf(sel.X)
	path := s.Index()
	for _, idx := range path[:len(path)-1] {
		if p, ok := types.Unalias(t).(*types.Pointer); ok {
			t = p.Elem()
		}
		st, ok := types.Unalias(t).Underlying().(*types.Struct)
		if !ok {
			return false
		}
		t = st.Field(idx).Type()
	}
	switch u := types.Unalias(t).(type) {
	case *types.Pointer:
		return false
	default:
		return !types.IsInterface(u)
	}
}

// noteSynthetic creates a symbol for an object of this package that is
// declared in a file the facts leave out (cgo's _cgo_gotypes.go declares
// _Cfunc_add for C.add), so references to it have something to point at. It
// has no position: there is no source for it.
func (w *walker) noteSynthetic(obj types.Object, ref model.Ref) {
	x := w.x
	if x.in.Sources == nil || obj.Pkg() != x.in.Pkg || !obj.Pos().IsValid() {
		return
	}
	if _, ok := x.fileInfo[x.in.Fset.PositionFor(obj.Pos(), false).Filename]; ok {
		return
	}
	name := obj.Name()
	for _, prefix := range []string{"_Cfunc_", "_Ctype_", "_Cmacro_", "_Cvar_"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok {
			name = "C." + strings.TrimPrefix(rest, "struct_")
			break
		}
	}
	x.addSymbol(model.Symbol{Key: ref.Key, Kind: ref.Kind, Name: name, Detail: x.detail(obj)})
	x.addEdge(model.EdgeContains, x.pkgKey, ref.Key)
}
