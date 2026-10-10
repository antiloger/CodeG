package query

import (
	"sort"
	"strings"

	"codeg/internal/model"
)

// Filter restricts occurrence results.
type Filter struct {
	NoTests           bool
	NoGenerated       bool
	IncludeDefinition bool         // definitions are left out unless asked for
	Roles             []model.Role // nil: any role
}

func (f Filter) keep(o Occ) bool {
	if f.NoTests && o.Test || f.NoGenerated && o.Generated {
		return false
	}
	if o.role == model.RoleDefinition && !f.IncludeDefinition && len(f.Roles) == 0 {
		return false
	}
	if len(f.Roles) == 0 {
		return true
	}
	for _, r := range f.Roles {
		if o.role == r {
			return true
		}
	}
	return false
}

func (ix *Index) occFrom(pkg string, files []model.FileFact, o model.Occurrence) Occ {
	oc := Occ{
		Location: ix.location(pkg, files, o.Range),
		Role:     o.Role.String(), Dispatch: o.Dispatch.String(), Flags: o.Flags.Names(),
		Call: int(o.Call), ArgOf: int(o.ArgOf), ArgIndex: int(o.ArgIndex), TypeArgs: o.TypeArgs,
		role: o.Role, sym: o.Symbol, encl: o.Enclosing,
	}
	if o.Symbol != (model.Key{}) {
		oc.Symbol = o.Symbol.String()
	}
	if o.Enclosing != (model.Key{}) {
		oc.Enclosing = o.Enclosing.String()
	}
	if o.FlowTo != (model.Key{}) {
		oc.FlowTo = o.FlowTo.String()
	}
	return oc
}

func sortOccs(o []Occ) {
	sort.SliceStable(o, func(i, j int) bool {
		a, b := o[i], o[j]
		if a.Package != b.Package {
			return a.Package < b.Package
		}
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})
}

// Occurrences returns the occurrences of a symbol in every package that can
// contain one, in source order.
func (ix *Index) Occurrences(key model.Key, f Filter) ([]Occ, error) {
	var out []Occ
	for _, pkg := range ix.referrers(key.Pkg) {
		r, err := ix.Reader(pkg)
		if err != nil {
			return nil, err
		}
		occs, err := r.OccurrencesOf(key)
		if err != nil {
			return nil, err
		}
		if len(occs) == 0 {
			continue
		}
		files, err := r.Files()
		if err != nil {
			return nil, err
		}
		for _, o := range occs {
			if oc := ix.occFrom(pkg, files, o); f.keep(oc) {
				out = append(out, oc)
			}
		}
	}
	sortOccs(out)
	return out, nil
}

// References returns every use of a symbol except its definition (unless the
// filter asks for it).
func (ix *Index) References(key model.Key, f Filter) ([]Occ, error) {
	return ix.Occurrences(key, f)
}

// AccessKind selects which occurrences Access returns.
type AccessKind int

const (
	Reads  AccessKind = iota // read, read_write
	Writes                   // write, read_write, write_partial, address_taken
)

// Access returns where a variable or field is read or written.
//
// address_taken and write_partial are included in Writes because the variable
// may be modified through them, but the index cannot tell whether it is: a
// pointer-receiver call or a store through &x is only recorded, not
// analysed. Callers should present those separately from definite writes.
func (ix *Index) Access(key model.Key, kind AccessKind, f Filter) ([]Occ, error) {
	f.Roles = []model.Role{model.RoleRead, model.RoleReadWrite}
	if kind == Writes {
		f.Roles = []model.Role{model.RoleWrite, model.RoleReadWrite, model.RoleWritePartial, model.RoleAddressTaken}
	}
	return ix.Occurrences(key, f)
}

// TypeUse is a type a function refers to.
type TypeUse struct {
	Type        SymbolRef `json:"type"`
	Uses        int       `json:"uses"`
	Constructed int       `json:"constructed"` // T{}, &T{}, new(T), make(T)
	Sites       []Occ     `json:"sites"`
}

// enclosedBy reports whether an occurrence's enclosing symbol is fn or one of
// its closures (fn$0, fn$0$1, ...).
func enclosedBy(encl, fn model.Key) bool {
	return encl.Pkg == fn.Pkg && (encl.Name == fn.Name || strings.HasPrefix(encl.Name, fn.Name+"$"))
}

// TypesUsed lists the types a function refers to, including in its signature
// and in its closures.
func (ix *Index) TypesUsed(fn model.Key, f Filter) ([]TypeUse, error) {
	r, err := ix.Reader(fn.Pkg)
	if err != nil {
		return nil, err
	}
	files, err := r.Files()
	if err != nil {
		return nil, err
	}
	occs, err := r.Occurrences()
	if err != nil {
		return nil, err
	}
	uses := map[model.Key]*TypeUse{}
	var order []model.Key
	for _, o := range occs {
		if (o.Role != model.RoleTypeUse && o.Role != model.RoleConstruct) || !enclosedBy(o.Enclosing, fn) {
			continue
		}
		oc := ix.occFrom(fn.Pkg, files, o)
		if f.NoTests && oc.Test || f.NoGenerated && oc.Generated {
			continue
		}
		tu := uses[o.Symbol]
		if tu == nil {
			tu = &TypeUse{Type: ix.symbolRef(o.Symbol)}
			uses[o.Symbol] = tu
			order = append(order, o.Symbol)
		}
		tu.Uses++
		if o.Role == model.RoleConstruct {
			tu.Constructed++
		}
		tu.Sites = append(tu.Sites, oc)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].Compare(order[j]) < 0 })
	out := make([]TypeUse, 0, len(order))
	for _, k := range order {
		out = append(out, *uses[k])
	}
	return out, nil
}

// Event is one thing that happens to a variable, in source order.
type Event struct {
	Occ
	// For an argument: the call it is passed to and the parameter it becomes,
	// so `x -> f(x)` is one hop away.
	PassedTo *SymbolRef `json:"passedTo,omitempty"`
	Param    string     `json:"param,omitempty"`
}

// Events lists every occurrence of a variable in source order, with the
// one-hop links the index records: which call an argument goes to, and what a
// value flows into. Order is source order, not execution order.
func (ix *Index) Events(key model.Key, f Filter) ([]Event, error) {
	f.IncludeDefinition = true
	occs, err := ix.Occurrences(key, f)
	if err != nil {
		return nil, err
	}
	calls := map[string]map[int32]model.Occurrence{} // package -> call id -> callee occurrence
	callsOf := func(pkg string) map[int32]model.Occurrence {
		if m, ok := calls[pkg]; ok {
			return m
		}
		m := map[int32]model.Occurrence{}
		if r, err := ix.Reader(pkg); err == nil {
			all, _ := r.Occurrences()
			for _, o := range all {
				if o.Role == model.RoleCall && o.Call != 0 {
					m[o.Call] = o
				}
			}
		}
		calls[pkg] = m
		return m
	}
	out := make([]Event, 0, len(occs))
	for _, oc := range occs {
		ev := Event{Occ: oc}
		if oc.ArgOf != 0 {
			if callee, ok := callsOf(oc.Package)[int32(oc.ArgOf)]; ok && callee.Symbol != (model.Key{}) {
				ref := ix.symbolRef(callee.Symbol)
				ev.PassedTo = &ref
				ev.Param = model.Key{Pkg: callee.Symbol.Pkg, Name: callee.Symbol.Name + "#param" + itoa(oc.ArgIndex)}.String()
			}
		}
		out = append(out, ev)
	}
	return out, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// MaxTextLines bounds how many occurrences of one response get their source
// line attached, so a popular symbol cannot make a response read hundreds of
// files.
const MaxTextLines = 300

// AttachText fills in the source line of each occurrence, up to MaxTextLines.
// It reads the working tree, so the text can be stale; use Source for a
// checked view.
func (ix *Index) AttachText(occs []Occ) {
	for i := range occs {
		if i >= MaxTextLines {
			return
		}
		occs[i].Text = ix.Snippet(occs[i].Location)
	}
}
