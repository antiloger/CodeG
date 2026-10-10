// Command scipcheck cross-checks a codeg index against an SCIP index made by
// scip-go for the same source tree (milestone 7). It reports every position
// where the two disagree about what is there, and every symbol one of them
// splits or merges relative to the other, so each difference can be
// investigated.
//
//	scip-go index -o index.scip           # in the module root
//	codeg index
//	go run ./cmd/scipcheck -scip index.scip -store .codeg
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/scip-code/scip/bindings/go/scip"

	"codeg/internal/index"
	"codeg/internal/model"
)

type pos struct {
	file      string
	line, col int32 // 1-based
}

type entry struct {
	pos
	symbol string
	def    bool
	role   string
	kind   string // codeg's kind of the symbol, for codeg entries
}

func main() {
	scipPath := flag.String("scip", "index.scip", "SCIP index from scip-go")
	storeDir := flag.String("store", ".codeg", "codeg index directory")
	samples := flag.Int("samples", 6, "examples to print per category")
	verbose := flag.Bool("v", false, "print every difference")
	flag.Parse()

	theirs, err := loadSCIP(*scipPath)
	if err != nil {
		fatal(err)
	}
	mine, err := loadMine(*storeDir)
	if err != nil {
		fatal(err)
	}
	r := compare(theirs, mine)
	r.print(os.Stdout, *samples, *verbose)
	if r.differences() > 0 {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "scipcheck:", err)
	os.Exit(2)
}

func loadSCIP(path string) ([]entry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx scip.Index
	if err := proto.Unmarshal(data, &idx); err != nil {
		return nil, err
	}
	var out []entry
	skipped := 0
	for _, doc := range idx.Documents {
		// Documents outside the project are differences by design: the
		// synthesized `p.test` mains live in the Go build cache, and
		// node_modules is JavaScript tooling. codeg indexes neither.
		if strings.HasPrefix(doc.RelativePath, "..") || strings.Contains(doc.RelativePath, "node_modules/") {
			skipped++
			continue
		}
		for _, o := range doc.Occurrences {
			if len(o.Range) < 3 {
				continue
			}
			sym := o.Symbol
			if strings.HasPrefix(sym, "local ") {
				sym = doc.RelativePath + " " + sym // locals are numbered per document
			}
			out = append(out, entry{
				pos:    pos{doc.RelativePath, o.Range[0] + 1, o.Range[1] + 1},
				symbol: sym,
				def:    o.SymbolRoles&int32(scip.SymbolRole_Definition) != 0,
				role:   scipRole(o.SymbolRoles),
			})
		}
	}
	if skipped > 0 {
		fmt.Fprintf(os.Stderr, "scipcheck: ignoring %d SCIP document(s) outside the project (generated test mains, node_modules)\n", skipped)
	}
	return out, nil
}

func scipRole(r int32) string {
	var parts []string
	for _, x := range []struct {
		bit  scip.SymbolRole
		name string
	}{
		{scip.SymbolRole_Definition, "definition"}, {scip.SymbolRole_Import, "import"},
		{scip.SymbolRole_WriteAccess, "write"}, {scip.SymbolRole_ReadAccess, "read"},
		{scip.SymbolRole_ForwardDefinition, "forward"},
	} {
		if r&int32(x.bit) != 0 {
			parts = append(parts, x.name)
		}
	}
	if len(parts) == 0 {
		return "reference"
	}
	return strings.Join(parts, "+")
}

func loadMine(storeDir string) ([]entry, error) {
	s := index.OpenStore(storeDir)
	m, err := s.ReadManifest()
	if err != nil {
		return nil, err
	}
	var out []entry
	for i := range m.Packages {
		r, err := s.OpenPackage(&m.Packages[i])
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
		syms, err := r.Symbols()
		if err != nil {
			return nil, err
		}
		kinds := make(map[model.Key]model.Kind, len(syms))
		for _, sy := range syms {
			kinds[sy.Key] = sy.Kind
		}
		for _, o := range occs {
			if o.Symbol == (model.Key{}) || o.Symbol.Pkg == "" || int(o.File) >= len(files) {
				continue // unknown call targets and universe symbols
			}
			out = append(out, entry{
				pos:    pos{files[o.File].Path, o.Line, o.Col},
				symbol: o.Symbol.String(),
				def:    o.Role == model.RoleDefinition,
				role:   o.Role.String(),
				kind:   kinds[o.Symbol].String(),
			})
		}
	}
	return out, nil
}

type report struct {
	theirs, mine                int
	missing                     []entry        // in SCIP, not in codeg: unexpected
	extra                       []entry        // in codeg, not in SCIP: unexpected
	expected                    map[string]int // differences that are by design, by reason
	defMismatch                 []string
	split                       []string // one SCIP symbol, several codeg keys
	merged                      []string // one codeg key, several SCIP symbols
	sharedPositions, sharedSyms int
}

func (r *report) differences() int {
	return len(r.missing) + len(r.extra) + len(r.defMismatch) + len(r.split) + len(r.merged)
}

func compare(theirs, mine []entry) *report {
	r := &report{theirs: len(theirs), mine: len(mine), expected: map[string]int{}}
	scipCount := map[string]int{}
	for _, t := range theirs {
		scipCount[t.symbol]++
	}
	byPos := func(es []entry) map[pos][]entry {
		m := map[pos][]entry{}
		for _, e := range es {
			m[e.pos] = append(m[e.pos], e)
		}
		return m
	}
	tp, mp := byPos(theirs), byPos(mine)

	// scip symbol -> codeg keys seen at the same position, and the reverse.
	s2m := map[string]map[string]bool{}
	m2s := map[string]map[string]bool{}
	add := func(m map[string]map[string]bool, a, b string) {
		if m[a] == nil {
			m[a] = map[string]bool{}
		}
		m[a][b] = true
	}

	for p, ts := range tp {
		ms := mp[p]
		if len(ms) == 0 {
			for _, t := range ts {
				if why := expectedMissing(t, scipCount); why != "" {
					r.expected[why]++
				} else {
					r.missing = append(r.missing, t)
				}
			}
			continue
		}
		r.sharedPositions++
		var tdef, mdef bool
		for _, t := range ts {
			tdef = tdef || t.def
		}
		for _, m := range ms {
			mdef = mdef || m.def
		}
		if tdef != mdef {
			r.defMismatch = append(r.defMismatch, fmt.Sprintf("%s:%d:%d scip(%s %s) mine(%s %s)", p.file, p.line, p.col,
				ts[0].role, short(ts[0].symbol), ms[0].role, ms[0].symbol))
		}
		// Identity: pair up only when the position has one symbol on each
		// side, or the same number in the same role.
		for _, t := range ts {
			for _, m := range ms {
				if t.def == m.def {
					add(s2m, t.symbol, m.symbol)
					add(m2s, m.symbol, t.symbol)
				}
			}
		}
	}
	for p, ms := range mp {
		if len(tp[p]) == 0 {
			for _, m := range ms {
				if why := expectedExtra(m); why != "" {
					r.expected[why]++
				} else {
					r.extra = append(r.extra, m)
				}
			}
		}
	}
	for s, ks := range s2m {
		if len(ks) > 1 {
			var keys []string
			for k := range ks {
				keys = append(keys, k)
			}
			if why := splitReason(keys); why != "" {
				r.expected[why]++
			} else {
				r.split = append(r.split, fmt.Sprintf("%s  ->  %s", short(s), joinKeys(ks)))
			}
		}
	}
	for k, ss := range m2s {
		if len(ss) > 1 {
			if why := mergeReason(k); why != "" {
				r.expected[why]++
			} else {
				r.merged = append(r.merged, fmt.Sprintf("%s  <-  %s", k, joinKeys(ss)))
			}
		}
	}
	r.sharedSyms = len(s2m)
	sort.Strings(r.defMismatch)
	sort.Strings(r.split)
	sort.Strings(r.merged)
	sortEntries(r.missing)
	sortEntries(r.extra)
	return r
}

// expectedMissing explains an SCIP occurrence codeg deliberately lacks.
func expectedMissing(t entry, scipCount map[string]int) string {
	switch class(t.symbol) {
	case "package":
		return "SCIP records package qualifiers (fmt in fmt.Println) and package clauses; codeg records the member used and the import"
	case "local", "term (var/const/field)":
		// A lone local definition, or a parameter descriptor (name:param):
		// SCIP merges function-type parameters with same-named closure
		// parameters, so the count alone does not identify them.
		if t.def && (scipCount[t.symbol] == 1 && class(t.symbol) == "local" || strings.Contains(short(t.symbol), ":")) {
			return "SCIP names parameters of function types (func(x int) in a type); nothing can refer to them, so codeg has no symbol"
		}
	}
	return ""
}

// expectedExtra explains a codeg occurrence SCIP lacks.
func expectedExtra(m entry) string {
	switch {
	case m.role == "import":
		return "codeg records imports at the path literal; SCIP records them as package references"
	case m.kind == "func_lit":
		return "codeg has symbols for closures (needed for call chains); SCIP has none"
	case m.kind == "label" && m.role == "read":
		return "codeg records the target of goto statements; SCIP records labels only for break and continue"
	case strings.HasPrefix(m.symbol, "unsafe."):
		return "codeg records uses of package unsafe (unsafe.Pointer); SCIP skips it, having no source for it"
	case m.kind == "field" && (strings.Contains(m.symbol, ".~") || strings.Contains(m.symbol, ".~struct@")):
		return "codeg records fields of anonymous structs; SCIP has no symbol for them"
	}
	return ""
}

// splitReason explains why one SCIP symbol maps to several codeg keys, when it
// is because SCIP is coarser than codeg.
func splitReason(keys []string) string {
	all := func(f func(string) bool) bool {
		for _, k := range keys {
			if !f(k) {
				return false
			}
		}
		return true
	}
	switch {
	case all(func(k string) bool { return strings.Contains(k, ".init#") }):
		return "SCIP gives every init function the same symbol; codeg numbers them"
	case all(func(k string) bool { return strings.Contains(k, "$") }):
		return "SCIP names variables of sibling closures alike (one symbol per name); codeg keeps one per closure"
	case all(func(k string) bool { return strings.HasSuffix(k, "]") }):
		return "SCIP names type parameters by name alone, merging those of different functions; codeg keys them by owner"
	case all(func(k string) bool { return strings.Contains(k, "::_@") }):
		return "SCIP names blank variables alike"
	}
	return ""
}

// mergeReason explains why several SCIP symbols map to one codeg key.
func mergeReason(key string) string {
	if strings.HasSuffix(key, "]") {
		return "SCIP emits two symbols (a type and a local) for one type parameter"
	}
	return ""
}

func sortEntries(es []entry) {
	sort.Slice(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.file != b.file {
			return a.file < b.file
		}
		if a.line != b.line {
			return a.line < b.line
		}
		return a.col < b.col
	})
}

func joinKeys(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, short(k))
	}
	sort.Strings(ks)
	return strings.Join(ks, " | ")
}

// short trims an SCIP symbol to its descriptor part.
func short(s string) string {
	if strings.HasPrefix(s, "scip-go ") {
		if i := strings.Index(s, "`"); i >= 0 {
			return s[i:]
		}
	}
	return s
}

// class buckets an SCIP symbol by its descriptor suffix.
func class(sym string) string {
	switch {
	case strings.Contains(sym, " local "):
		return "local"
	case strings.HasSuffix(sym, "#"):
		return "type"
	case strings.HasSuffix(sym, ")."):
		return "func/method"
	case strings.HasSuffix(sym, "."):
		return "term (var/const/field)"
	case strings.HasSuffix(sym, "/"):
		return "package"
	case strings.HasSuffix(sym, "]"):
		return "type parameter"
	}
	return "other"
}

func (r *report) print(w *os.File, samples int, verbose bool) {
	fmt.Fprintf(w, "scip occurrences: %d   codeg occurrences (excluding universe/unknown): %d\n", r.theirs, r.mine)
	fmt.Fprintf(w, "positions in both: %d, symbols paired: %d\n\n", r.sharedPositions, r.sharedSyms)

	group := func(title string, es []entry, key func(entry) string) {
		fmt.Fprintf(w, "%s: %d\n", title, len(es))
		if len(es) == 0 {
			return
		}
		counts := map[string]int{}
		ex := map[string][]entry{}
		for _, e := range es {
			k := key(e)
			counts[k]++
			ex[k] = append(ex[k], e)
		}
		var keys []string
		for k := range counts {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
		for _, k := range keys {
			fmt.Fprintf(w, "  %-28s %d\n", k, counts[k])
			n := samples
			if verbose {
				n = len(ex[k])
			}
			for i := 0; i < n && i < len(ex[k]); i++ {
				e := ex[k][i]
				fmt.Fprintf(w, "      %s:%d:%d  %s  %s\n", e.file, e.line, e.col, e.role, short(e.symbol))
			}
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "expected differences (by design):")
	var reasons []string
	for why := range r.expected {
		reasons = append(reasons, why)
	}
	sort.Strings(reasons)
	for _, why := range reasons {
		fmt.Fprintf(w, "  %6d  %s\n", r.expected[why], why)
	}
	fmt.Fprintln(w)

	group("UNEXPECTED: in SCIP but not in codeg", r.missing, func(e entry) string { return class(e.symbol) + " / " + e.role })
	group("UNEXPECTED: in codeg but not in SCIP", r.extra, func(e entry) string { return e.role + " / " + e.kind })

	list := func(title string, l []string) {
		fmt.Fprintf(w, "%s: %d\n", title, len(l))
		n := samples
		if verbose {
			n = len(l)
		}
		for i := 0; i < n && i < len(l); i++ {
			fmt.Fprintf(w, "    %s\n", l[i])
		}
		fmt.Fprintln(w)
	}
	list("UNEXPECTED: definition disagreements", r.defMismatch)
	list("UNEXPECTED: symbols codeg splits (one SCIP symbol, several codeg keys)", r.split)
	list("UNEXPECTED: symbols codeg merges (one codeg key, several SCIP symbols)", r.merged)
}
