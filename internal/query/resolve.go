package query

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"codeg/internal/model"
)

// ErrNotFound is returned when a spec matches no symbol.
var ErrNotFound = errors.New("no such symbol")

// AmbiguousError lists the candidates when a spec matches several symbols.
type AmbiguousError struct {
	Spec       string
	Candidates []SymbolRef
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q is ambiguous; it matches %d symbols:", e.Spec, len(e.Candidates))
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  %s  (%s)", c.Key, c.Kind)
	}
	return b.String()
}

var positionRE = regexp.MustCompile(`^(.+?):(\d+)(?::(\d+))?$`)

// Resolve finds the symbols a user-supplied spec names. Forms accepted:
//
//	pkg/path.Name, pkg/path.Type.Method      the exact key
//	pkg.Name, lib.Add                        last element(s) of the package path
//	Name, Type.Method, Func::x               a name (matched at a "." or ":" boundary)
//	file.go:LINE[:COL]                       the symbol at a source position
//
// It returns every match; use ResolveOne to insist on a single symbol.
func (ix *Index) Resolve(spec string) ([]SymbolRef, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("empty symbol")
	}
	if m := positionRE.FindStringSubmatch(spec); m != nil {
		if pkg, ok := ix.packageOfFile(m[1]); ok {
			line, _ := strconv.Atoi(m[2])
			col := 0
			if m[3] != "" {
				col, _ = strconv.Atoi(m[3])
			}
			return ix.atPosition(pkg, m[1], line, col)
		}
	}

	if found, err := ix.resolveInProject(spec); err != nil || len(found) > 0 {
		return found, err
	}
	if found := ix.resolveExternal(spec); len(found) > 0 {
		return found, nil
	}
	return nil, fmt.Errorf("%w: %s", ErrNotFound, spec)
}

// ResolveOne resolves spec to exactly one symbol.
func (ix *Index) ResolveOne(spec string) (SymbolRef, error) {
	found, err := ix.Resolve(spec)
	if err != nil {
		return SymbolRef{}, err
	}
	if len(found) > 1 {
		return SymbolRef{}, &AmbiguousError{Spec: spec, Candidates: found}
	}
	return found[0], nil
}

func (ix *Index) packageOfFile(file string) (string, bool) {
	file = strings.TrimPrefix(file, "./")
	if p, ok := ix.byFile[file]; ok {
		return p, true
	}
	var found string
	for f, p := range ix.byFile {
		if strings.HasSuffix(f, "/"+file) {
			if found != "" && found != p {
				return "", false
			}
			found = p
		}
	}
	return found, found != ""
}

func (ix *Index) atPosition(pkg, file string, line, col int) ([]SymbolRef, error) {
	r, err := ix.Reader(pkg)
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
	file = strings.TrimPrefix(file, "./")
	seen := map[model.Key]bool{}
	var keys []model.Key
	best := map[model.Key]int{}
	for _, o := range occs {
		if int(o.File) >= len(files) || o.Symbol == (model.Key{}) || o.Role == model.RoleImport {
			continue
		}
		f := files[o.File].Path
		if f != file && !strings.HasSuffix(f, "/"+file) {
			continue
		}
		if int(o.Line) > line || int(o.EndLine) < line {
			continue
		}
		if col > 0 {
			if (int(o.Line) == line && int(o.Col) > col) || (int(o.EndLine) == line && int(o.EndCol) <= col) {
				continue
			}
		}
		width := int(o.End - o.Start)
		if prev, has := best[o.Symbol]; !has || width < prev {
			best[o.Symbol] = width
		}
		if !seen[o.Symbol] {
			seen[o.Symbol] = true
			keys = append(keys, o.Symbol)
		}
	}
	if col > 0 && len(keys) > 1 {
		// Nested ranges (x.f: both x and f cover the column when col is on the
		// dot): prefer the narrowest.
		sort.SliceStable(keys, func(i, j int) bool { return best[keys[i]] < best[keys[j]] })
		keys = keys[:1]
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("%w at %s:%d", ErrNotFound, file, line)
	}
	var out []SymbolRef
	for _, k := range keys {
		out = append(out, ix.symbolRef(k))
	}
	sortRefs(out)
	return out, nil
}

// nameMatches reports whether a key name matches spec at a boundary: the
// whole name, or a suffix that starts after "." or ":". A spec without an
// "@N" ordinal also matches names that end in one (locals, closures' locals).
func nameMatches(keyName, spec string) bool {
	if keyName == spec {
		return true
	}
	if !strings.Contains(spec, "@") {
		if i := strings.LastIndexByte(keyName, '@'); i > 0 && allDigits(keyName[i+1:]) {
			keyName = keyName[:i]
			if keyName == spec {
				return true
			}
		}
	}
	if len(keyName) > len(spec) && strings.HasSuffix(keyName, spec) {
		switch keyName[len(keyName)-len(spec)-1] {
		case '.', ':':
			return true
		}
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// resolveInProject looks for spec among the indexed packages' symbols.
func (ix *Index) resolveInProject(spec string) ([]SymbolRef, error) {
	wantsLocal := strings.Contains(spec, "::") || strings.Contains(spec, "#") || strings.Contains(spec, "$")

	// Candidate (package, name) splits.
	type split struct{ pkg, name string }
	var exact []split
	pkgs := ix.Packages()
	for _, p := range pkgs {
		if spec == p {
			exact = append(exact, split{p, ""})
		} else if strings.HasPrefix(spec, p+".") {
			exact = append(exact, split{p, spec[len(p)+1:]})
		}
	}
	// Prefer the longest package prefix: gopkg.in/yaml.v3.Node.
	sort.Slice(exact, func(i, j int) bool { return len(exact[i].pkg) > len(exact[j].pkg) })
	for _, sp := range exact {
		if found, err := ix.lookupExact(sp.pkg, sp.name); err != nil || len(found) > 0 {
			return found, err
		}
	}

	// pkg-suffix.Name: try every dot as the package/name boundary.
	var out []SymbolRef
	seen := map[model.Key]bool{}
	for i := 0; i < len(spec); i++ {
		if spec[i] != '.' {
			continue
		}
		pkgPart, name := spec[:i], spec[i+1:]
		for _, p := range pkgs {
			if p == pkgPart || strings.HasSuffix(p, "/"+pkgPart) {
				found, err := ix.lookupExact(p, name)
				if err != nil {
					return nil, err
				}
				for _, s := range found {
					if !seen[s.k] {
						seen[s.k] = true
						out = append(out, s)
					}
				}
			}
		}
	}
	if len(out) > 0 {
		sortRefs(out)
		return out, nil
	}

	// A bare name: scan every package's symbols.
	for _, p := range pkgs {
		r, err := ix.Reader(p)
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
		for i := range syms {
			s := &syms[i]
			switch s.Kind {
			case model.KindPackage, model.KindFile:
				continue
			case model.KindLocal, model.KindParam, model.KindResult, model.KindFuncLit:
				if !wantsLocal {
					continue
				}
			}
			if nameMatches(s.Key.Name, spec) && !seen[s.Key] {
				seen[s.Key] = true
				out = append(out, ix.refFromSymbol(p, files, s))
			}
		}
	}
	sortRefs(out)
	return out, nil
}

func (ix *Index) lookupExact(pkg, name string) ([]SymbolRef, error) {
	r, err := ix.Reader(pkg)
	if err != nil {
		return nil, err
	}
	files, err := r.Files()
	if err != nil {
		return nil, err
	}
	if name == "" {
		s, ok, err := r.Symbol(model.Key{Pkg: pkg})
		if err != nil || !ok {
			return nil, err
		}
		return []SymbolRef{ix.refFromSymbol(pkg, files, &s)}, nil
	}
	s, ok, err := r.Symbol(model.Key{Pkg: pkg, Name: name})
	if err != nil || ok {
		if err != nil {
			return nil, err
		}
		return []SymbolRef{ix.refFromSymbol(pkg, files, &s)}, nil
	}
	// A local given without its ordinal: F::x matches F::x@0, F::x@1.
	if !strings.Contains(name, "@") && strings.Contains(name, "::") {
		syms, err := r.Symbols()
		if err != nil {
			return nil, err
		}
		var out []SymbolRef
		for i := range syms {
			if nameMatches(syms[i].Key.Name, name) && strings.HasPrefix(syms[i].Key.Name, name+"@") {
				out = append(out, ix.refFromSymbol(pkg, files, &syms[i]))
			}
		}
		return out, nil
	}
	return nil, nil
}

// resolveExternal finds a symbol owned by a dependency, using the stubs
// recorded in packages that refer to it.
func (ix *Index) resolveExternal(spec string) []SymbolRef {
	var out []SymbolRef
	seen := map[model.Key]bool{}
	for i := 0; i < len(spec); i++ {
		if spec[i] != '.' {
			continue
		}
		pkgPart, name := spec[:i], spec[i+1:]
		for ref, from := range ix.refBy {
			if ref != pkgPart && !strings.HasSuffix(ref, "/"+pkgPart) {
				continue
			}
			if ix.Manifest.Package(ref) != nil || len(from) == 0 {
				continue
			}
			key := model.Key{Pkg: ref, Name: name}
			if seen[key] {
				continue
			}
			if s := ix.externalRef(key, from); s != nil {
				seen[key] = true
				out = append(out, *s)
			}
		}
	}
	sortRefs(out)
	return out
}

func sortRefs(r []SymbolRef) {
	sort.Slice(r, func(i, j int) bool { return r[i].k.Compare(r[j].k) < 0 })
}

// refFromSymbol builds a SymbolRef for a symbol of an indexed package.
func (ix *Index) refFromSymbol(pkg string, files []model.FileFact, s *model.Symbol) SymbolRef {
	ref := SymbolRef{
		Key: s.Key.String(), Package: pkg, Name: s.Name, Kind: s.Kind.String(), Detail: s.Detail, k: s.Key,
	}
	if s.Pos.Line > 0 {
		l := ix.location(pkg, files, s.Pos)
		ref.Location = &l
	}
	return ref
}

// symbolRef describes any symbol by key: an indexed one from its package, an
// external one from a stub, or a universe symbol.
func (ix *Index) symbolRef(key model.Key) SymbolRef {
	unknown := SymbolRef{Key: key.String(), Package: key.Pkg, Name: key.Name, Kind: "unknown", k: key}
	if key.Pkg == "" {
		unknown.Kind = "universe"
		return unknown
	}
	if ix.Manifest.Package(key.Pkg) != nil {
		r, err := ix.Reader(key.Pkg)
		if err != nil {
			return unknown
		}
		files, _ := r.Files()
		if s, ok, err := r.Symbol(key); err == nil && ok {
			return ix.refFromSymbol(key.Pkg, files, &s)
		}
		return unknown
	}
	if s := ix.externalRef(key, ix.refBy[key.Pkg]); s != nil {
		return *s
	}
	return unknown
}

func (ix *Index) externalRef(key model.Key, from []string) *SymbolRef {
	ix.mu.Lock()
	cached, ok := ix.ext[key]
	ix.mu.Unlock()
	if ok {
		return cached
	}
	var found *SymbolRef
	for _, p := range from {
		r, err := ix.Reader(p)
		if err != nil {
			continue
		}
		exts, err := r.Externals()
		if err != nil {
			continue
		}
		for _, e := range exts {
			if e.Key == key {
				found = &SymbolRef{
					Key: key.String(), Package: key.Pkg, Name: e.Name, Kind: e.Kind.String(), Detail: e.Detail,
					External: true, Module: e.Module, Version: e.Version, k: key,
				}
				break
			}
		}
		if found != nil {
			break
		}
	}
	ix.mu.Lock()
	ix.ext[key] = found
	ix.mu.Unlock()
	return found
}

// ParseKey parses an exact symbol key as it appears in results (pkgpath.Name).
// Unlike Resolve it never guesses: the package must be indexed (or be a known
// dependency) and the symbol must exist.
func (ix *Index) ParseKey(s string) (model.Key, error) {
	if s == "" {
		return model.Key{}, fmt.Errorf("%w: empty key", ErrNotFound)
	}
	// The longest package prefix wins: gopkg.in/yaml.v3.Node.
	pkgs := ix.Packages()
	sort.Slice(pkgs, func(i, j int) bool { return len(pkgs[i]) > len(pkgs[j]) })
	for _, p := range pkgs {
		var name string
		switch {
		case s == p:
		case strings.HasPrefix(s, p+"."):
			name = s[len(p)+1:]
		default:
			continue
		}
		r, err := ix.Reader(p)
		if err != nil {
			return model.Key{}, err
		}
		k := model.Key{Pkg: p, Name: name}
		if _, ok, err := r.Symbol(k); err != nil {
			return model.Key{}, err
		} else if ok {
			return k, nil
		}
		break // the longest matching package owns the key: it does not exist there
	}
	// A dependency's symbol: known from the stubs of packages that refer to it.
	var deps []string
	for p := range ix.refBy {
		if ix.Manifest.Package(p) == nil && strings.HasPrefix(s, p+".") {
			deps = append(deps, p)
		}
	}
	sort.Slice(deps, func(i, j int) bool { return len(deps[i]) > len(deps[j]) })
	for _, p := range deps {
		k := model.Key{Pkg: p, Name: s[len(p)+1:]}
		if ix.externalRef(k, ix.refBy[p]) != nil {
			return k, nil
		}
		break
	}
	// A universe symbol (error.Error, int).
	if !strings.ContainsAny(s, "/") && ix.universeKnown(s) {
		return model.Key{Name: s}, nil
	}
	return model.Key{}, fmt.Errorf("%w: %s", ErrNotFound, s)
}

// universeKnown reports whether a predeclared symbol appears in any result.
// Universe symbols carry no package, so they are only accepted in the forms
// the index produces: error.Error and the builtin type names.
func (ix *Index) universeKnown(s string) bool {
	switch s {
	case "error", "error.Error", "any", "comparable":
		return true
	}
	return false
}
