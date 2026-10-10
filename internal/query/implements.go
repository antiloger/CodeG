package query

import (
	"fmt"
	"sort"
	"strings"

	"codeg/internal/model"
)

// Implementer is a type that satisfies an interface.
type Implementer struct {
	Type SymbolRef `json:"type"`
	// ViaPointer: only *T satisfies the interface (some method has a pointer
	// receiver), not T.
	ViaPointer bool `json:"viaPointer,omitempty"`
	// ViaEmbedding: at least one required method is promoted from an embedded
	// field.
	ViaEmbedding bool `json:"viaEmbedding,omitempty"`
	// Interface: the type is itself an interface with a superset of the methods.
	Interface bool `json:"interface,omitempty"`
}

// Limits of v1 "implements", reported to users rather than hidden:
//   - generic interfaces and generic types are not considered;
//   - only interfaces and types of indexed packages are compared, so
//     satisfying an external interface (io.Reader) is not reported;
//   - interfaces that are constraints with type sets are skipped.
const ImplementsLimits = "generic types, external interfaces and type-set constraints are not considered"

var errNoMethodSet = fmt.Errorf("not a non-generic interface of an indexed package")

func (ix *Index) methodSetOf(key model.Key) (*model.MethodSet, error) {
	if ix.Manifest.Package(key.Pkg) == nil {
		return nil, errNoMethodSet
	}
	r, err := ix.Reader(key.Pkg)
	if err != nil {
		return nil, err
	}
	sets, err := r.MethodSets()
	if err != nil {
		return nil, err
	}
	i := sort.Search(len(sets), func(i int) bool { return sets[i].Type.Compare(key) >= 0 })
	if i < len(sets) && sets[i].Type == key {
		return &sets[i], nil
	}
	return nil, nil
}

// satisfies reports whether have provides every method of need with an
// identical signature. It is a set-inclusion check on canonical strings.
func satisfies(need []model.MethodEntry, have *model.MethodSet) (ok, viaPtr, viaEmb bool) {
	byName := make(map[string]model.MethodEntry, len(have.Methods))
	for _, m := range have.Methods {
		byName[m.Name] = m
	}
	for _, n := range need {
		h, found := byName[n.Name]
		if !found || h.Sig != n.Sig {
			return false, false, false
		}
		viaPtr = viaPtr || h.PtrRecv
		viaEmb = viaEmb || h.Promoted
	}
	return true, viaPtr, viaEmb
}

// Implementers lists the types (and interfaces) of the indexed packages that
// satisfy an interface. Packages without all of the interface's method names
// are skipped without being opened.
func (ix *Index) Implementers(iface model.Key) ([]Implementer, error) {
	ms, err := ix.methodSetOf(iface)
	if err != nil {
		return nil, err
	}
	if ms == nil {
		// Empty interfaces, generic types and type-set constraints have no
		// recorded method set. Say which case is likely.
		if ref := ix.symbolRef(iface); ref.Kind == "type" && ref.Detail == "interface" {
			return nil, fmt.Errorf("%s has no methods (or is generic, or is a type-set constraint): %s", iface, ImplementsLimits)
		}
		return nil, fmt.Errorf("%s is %w", iface, errNoMethodSet)
	}
	if !ms.Interface {
		return nil, fmt.Errorf("%s is %w", iface, errNoMethodSet)
	}
	if len(ms.Methods) == 0 {
		return nil, fmt.Errorf("%s has no methods: every type satisfies it", iface)
	}

	var out []Implementer
	for _, pkg := range ix.packagesWithMethods(ms.Methods) {
		r, err := ix.Reader(pkg)
		if err != nil {
			return nil, err
		}
		sets, err := r.MethodSets()
		if err != nil {
			return nil, err
		}
		for i := range sets {
			have := &sets[i]
			if have.Type == iface {
				continue
			}
			ok, viaPtr, viaEmb := satisfies(ms.Methods, have)
			if !ok {
				continue
			}
			out = append(out, Implementer{
				Type: ix.symbolRef(have.Type), ViaPointer: viaPtr, ViaEmbedding: viaEmb, Interface: have.Interface,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type.k.Compare(out[j].Type.k) < 0 })
	return out, nil
}

// packagesWithMethods returns the packages that have a method set containing
// every one of the named methods: the intersection of the summary's
// per-method package lists, smallest list first.
func (ix *Index) packagesWithMethods(methods []model.MethodEntry) []string {
	lists := make([][]string, 0, len(methods))
	for _, m := range methods {
		l := ix.methodPkgs[m.Name]
		if len(l) == 0 {
			return nil
		}
		lists = append(lists, l)
	}
	sort.Slice(lists, func(i, j int) bool { return len(lists[i]) < len(lists[j]) })
	set := map[string]int{}
	for _, p := range lists[0] {
		set[p] = 1
	}
	for _, l := range lists[1:] {
		for _, p := range l {
			if set[p] > 0 {
				set[p]++
			}
		}
	}
	var out []string
	for p, n := range set {
		if n == len(lists) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Implemented is an interface that a type satisfies.
type Implemented struct {
	Interface    SymbolRef `json:"interface"`
	ViaPointer   bool      `json:"viaPointer,omitempty"`
	ViaEmbedding bool      `json:"viaEmbedding,omitempty"`
}

// InterfacesOf lists the interfaces of the indexed packages that a type
// satisfies.
func (ix *Index) InterfacesOf(typ model.Key) ([]Implemented, error) {
	have, err := ix.methodSetOf(typ)
	if err != nil {
		return nil, err
	}
	if have == nil {
		// A concrete type with no methods has no recorded method set: it
		// satisfies no non-empty interface. Anything else is a refusal.
		if ref := ix.symbolRef(typ); ref.Kind != "type" || ref.Detail == "interface" || ix.Manifest.Package(typ.Pkg) == nil {
			return nil, fmt.Errorf("%s is not a non-generic named type of an indexed package", typ)
		}
		have = &model.MethodSet{Type: typ}
	}
	var out []Implemented
	for _, pkg := range ix.Packages() {
		r, err := ix.Reader(pkg)
		if err != nil {
			return nil, err
		}
		sets, err := r.MethodSets()
		if err != nil {
			return nil, err
		}
		for i := range sets {
			ifc := &sets[i]
			if !ifc.Interface || len(ifc.Methods) == 0 || ifc.Type == typ {
				continue
			}
			if ok, viaPtr, viaEmb := satisfies(ifc.Methods, have); ok {
				out = append(out, Implemented{Interface: ix.symbolRef(ifc.Type), ViaPointer: viaPtr, ViaEmbedding: viaEmb})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Interface.k.Compare(out[j].Interface.k) < 0 })
	return out, nil
}

// methodOf splits a method key ("pkg", "T.M") into its type key and method
// name, or reports false for a key that is not a method of a named type.
func methodOf(key model.Key) (typ model.Key, name string, ok bool) {
	i := strings.LastIndexByte(key.Name, '.')
	if i <= 0 || strings.ContainsAny(key.Name, "$:#@~") {
		return model.Key{}, "", false
	}
	return model.Key{Pkg: key.Pkg, Name: key.Name[:i]}, key.Name[i+1:], true
}

// qualifiedMethodName is a method's name as it appears in method sets:
// unexported names carry their package path.
func qualifiedMethodName(pkg, name string) string {
	if name != "" && (name[0] < 'A' || name[0] > 'Z') {
		return pkg + "." + name
	}
	return name
}

// candidateMethods lists the concrete methods an interface method call may
// reach. Promoted methods are declared elsewhere and are not listed by key:
// they come back with only the implementing type.
func (ix *Index) candidateMethods(ifaceMethod model.Key) ([]SymbolRef, error) {
	iface, name, ok := methodOf(ifaceMethod)
	if !ok {
		return nil, nil
	}
	ms, err := ix.methodSetOf(iface)
	if err != nil || ms == nil || !ms.Interface {
		return nil, nil // an external or generic interface: no candidates
	}
	impls, err := ix.Implementers(iface)
	if err != nil {
		return nil, err
	}
	var out []SymbolRef
	for _, im := range impls {
		if im.Interface {
			continue
		}
		mk := model.Key{Pkg: im.Type.Package, Name: im.Type.k.Name + "." + name}
		ref := ix.symbolRef(mk)
		if ref.Kind == "unknown" { // promoted from an embedded type
			ref = im.Type
		}
		out = append(out, ref)
	}
	return out, nil
}

// ImplementsResult answers "implements" in whichever direction applies.
type ImplementsResult struct {
	Symbol       SymbolRef     `json:"symbol"`
	IsInterface  bool          `json:"isInterface"`
	Implementers []Implementer `json:"implementers,omitempty"` // for an interface: the types that satisfy it
	Implemented  []Implemented `json:"implemented,omitempty"`  // for a type: the interfaces it satisfies
	Limits       string        `json:"limits"`
}

// Implements takes an interface and returns its implementers, or a named type
// and returns the interfaces it satisfies.
func (ix *Index) Implements(key model.Key) (*ImplementsResult, error) {
	res := &ImplementsResult{Symbol: ix.symbolRef(key), Limits: ImplementsLimits}
	ms, err := ix.methodSetOf(key)
	if err != nil {
		return nil, fmt.Errorf("%s is %w", key, errNoMethodSet)
	}
	if ms != nil && ms.Interface || ms == nil && res.Symbol.Detail == "interface" && res.Symbol.Kind == "type" {
		res.IsInterface = true
		res.Implementers, err = ix.Implementers(key)
		return res, err
	}
	res.Implemented, err = ix.InterfacesOf(key)
	return res, err
}
