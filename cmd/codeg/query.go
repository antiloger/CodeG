package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codeg/internal/query"
)

const queryUsage = `usage: codeg query <kind> <symbol> [flags]

kinds:
  def <symbol>              where a symbol is defined
  refs <symbol>             every reference (add -with-def to include the definition)
  callers <symbol>          who calls a function or method
  callees <symbol>          what a function calls (interface calls list candidates)
  reads <symbol>            where a variable or field is read
  writes <symbol>           where it is written, or may be modified (address taken, partial writes)
  types <function>          the types a function uses
  implements <symbol>       for an interface, its implementers; for a type, its interfaces
  chain <from> <to>         the shortest call path between two functions
  events <variable>         everything that happens to a variable, in source order

A symbol can be a full key (example.com/m/lib.Add), a package-suffixed name
(lib.Add), a bare name (Add, Store.Put, Func::x), or a position (file.go:12:5).
An ambiguous name lists its candidates.

flags:
`

type queryFlags struct {
	dir, store     string
	json           bool
	noTests, noGen bool
	withDef        bool
	depth          int
}

func runQuery(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Print(queryUsage)
		return nil
	}
	kind, args := args[0], args[1:]

	var qf queryFlags
	fs := flag.NewFlagSet("query "+kind, flag.ExitOnError)
	fs.StringVar(&qf.dir, "dir", ".", "module or workspace root")
	fs.StringVar(&qf.store, "store", "", "index directory (default: <dir>/.codeg)")
	fs.BoolVar(&qf.json, "json", false, "machine-readable output")
	fs.BoolVar(&qf.noTests, "no-tests", false, "leave out test files")
	fs.BoolVar(&qf.noGen, "no-generated", false, "leave out generated files")
	fs.BoolVar(&qf.withDef, "with-def", false, "include the definition (refs)")
	fs.IntVar(&qf.depth, "depth", 15, "maximum number of calls (chain)")
	// Flags may follow the symbol arguments, so parse them out of the tail.
	var pos []string
	for len(args) > 0 {
		if strings.HasPrefix(args[0], "-") && len(args[0]) > 1 {
			fs.Parse(args)
			args = fs.Args()
			continue
		}
		pos = append(pos, args[0])
		args = args[1:]
	}

	sd := qf.store
	if sd == "" {
		sd = filepath.Join(qf.dir, ".codeg")
	}
	ix, err := query.Open(sd)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no index") {
			return fmt.Errorf("no index in %s; run \"codeg index\" first", sd)
		}
		return err
	}
	return dispatch(ix, kind, pos, qf, os.Stdout)
}

func dispatch(ix *query.Index, kind string, pos []string, qf queryFlags, w io.Writer) error {
	need := func(n int) error {
		if len(pos) != n {
			return fmt.Errorf("%s takes %d symbol argument(s), got %d\n\n%s", kind, n, len(pos), queryUsage)
		}
		return nil
	}
	filter := query.Filter{NoTests: qf.noTests, NoGenerated: qf.noGen, IncludeDefinition: qf.withDef}
	resolve := func(spec string) (query.SymbolRef, error) { return ix.ResolveOne(spec) }
	out := &printer{w: w, ix: ix, json: qf.json}

	switch kind {
	case "def":
		if err := need(1); err != nil {
			return err
		}
		found, err := ix.Resolve(pos[0])
		if err != nil {
			return err
		}
		return out.symbols(found)

	case "refs", "reads", "writes", "events":
		if err := need(1); err != nil {
			return err
		}
		sym, err := resolve(pos[0])
		if err != nil {
			return err
		}
		switch kind {
		case "refs":
			occs, err := ix.References(sym.ModelKey(), filter)
			if err != nil {
				return err
			}
			return out.occurrences(sym, occs)
		case "reads", "writes":
			ak := query.Reads
			if kind == "writes" {
				ak = query.Writes
			}
			occs, err := ix.Access(sym.ModelKey(), ak, filter)
			if err != nil {
				return err
			}
			return out.access(sym, kind, occs)
		default:
			evs, err := ix.Events(sym.ModelKey(), filter)
			if err != nil {
				return err
			}
			return out.events(sym, evs)
		}

	case "callers", "callees":
		if err := need(1); err != nil {
			return err
		}
		sym, err := resolve(pos[0])
		if err != nil {
			return err
		}
		var edges []query.CallEdge
		if kind == "callers" {
			edges, err = ix.Callers(sym.ModelKey(), filter)
		} else {
			edges, err = ix.Callees(sym.ModelKey(), filter)
		}
		if err != nil {
			return err
		}
		return out.calls(sym, kind, edges)

	case "types":
		if err := need(1); err != nil {
			return err
		}
		sym, err := resolve(pos[0])
		if err != nil {
			return err
		}
		uses, err := ix.TypesUsed(sym.ModelKey(), filter)
		if err != nil {
			return err
		}
		return out.types(sym, uses)

	case "implements":
		if err := need(1); err != nil {
			return err
		}
		sym, err := resolve(pos[0])
		if err != nil {
			return err
		}
		res, err := ix.Implements(sym.ModelKey())
		if err != nil {
			return err
		}
		return out.implements(res)

	case "chain":
		if err := need(2); err != nil {
			return err
		}
		from, err := resolve(pos[0])
		if err != nil {
			return err
		}
		to, err := resolve(pos[1])
		if err != nil {
			return err
		}
		c, err := ix.CallChain(from.ModelKey(), to.ModelKey(), qf.depth)
		if err != nil {
			return err
		}
		return out.chain(from, to, c)
	}
	return fmt.Errorf("unknown query %q\n\n%s", kind, queryUsage)
}

// printer renders results as text or JSON.
type printer struct {
	w    io.Writer
	ix   *query.Index
	json bool
}

func (p *printer) emit(v any) error {
	enc := json.NewEncoder(p.w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// short drops a key's import-path directories: example.com/m/svc.Run -> svc.Run.
func short(key string) string {
	if i := strings.LastIndexByte(key, '/'); i >= 0 {
		return key[i+1:]
	}
	return key
}

func (p *printer) line(l query.Location) string {
	s := l.String()
	var tags []string
	if l.Test {
		tags = append(tags, "test")
	}
	if l.Generated {
		tags = append(tags, "generated")
	}
	if len(tags) > 0 {
		s += " [" + strings.Join(tags, ",") + "]"
	}
	return s
}

func (p *printer) snippet(l query.Location) string {
	if s := p.ix.Snippet(l); s != "" {
		return "    " + s + "\n"
	}
	return ""
}

func (p *printer) symbols(found []query.SymbolRef) error {
	if p.json {
		return p.emit(found)
	}
	for _, s := range found {
		fmt.Fprintf(p.w, "%s  %s\n", s.Kind, s.Key)
		if s.Detail != "" {
			fmt.Fprintf(p.w, "  %s\n", s.Detail)
		}
		switch {
		case s.Location != nil:
			fmt.Fprintf(p.w, "  defined at %s\n", p.line(*s.Location))
			fmt.Fprint(p.w, p.snippet(*s.Location))
		case s.External:
			fmt.Fprintf(p.w, "  external: defined in %s@%s (dependency; not indexed)\n", s.Module, s.Version)
		}
	}
	return nil
}

func (p *printer) occurrences(sym query.SymbolRef, occs []query.Occ) error {
	if p.json {
		return p.emit(map[string]any{"symbol": sym, "occurrences": occs})
	}
	fmt.Fprintf(p.w, "%d reference(s) to %s\n", len(occs), sym.Key)
	p.occLines(occs)
	return nil
}

func (p *printer) occLines(occs []query.Occ) {
	for _, o := range occs {
		fmt.Fprintf(p.w, "%s  %s", p.line(o.Location), o.Role)
		if len(o.Flags) > 0 {
			fmt.Fprintf(p.w, " (%s)", strings.Join(o.Flags, ", "))
		}
		if o.Enclosing != "" {
			fmt.Fprintf(p.w, "  in %s", short(o.Enclosing))
		}
		fmt.Fprintln(p.w)
		fmt.Fprint(p.w, p.snippet(o.Location))
	}
}

func (p *printer) access(sym query.SymbolRef, kind string, occs []query.Occ) error {
	if p.json {
		return p.emit(map[string]any{"symbol": sym, "kind": kind, "occurrences": occs})
	}
	fmt.Fprintf(p.w, "%d %s of %s\n", len(occs), kind, sym.Key)
	if kind == "writes" {
		fmt.Fprintln(p.w, "  write = overwritten; read_write = x++ / x += y; write_partial = a field or element of it is written;")
		fmt.Fprintln(p.w, "  address_taken = &x or a pointer-receiver call: it MAY be modified, the index does not analyse what happens next")
	}
	p.occLines(occs)
	return nil
}

func (p *printer) events(sym query.SymbolRef, evs []query.Event) error {
	if p.json {
		return p.emit(map[string]any{"symbol": sym, "order": "source order, not execution order", "events": evs})
	}
	fmt.Fprintf(p.w, "%d event(s) for %s (source order, not execution order)\n", len(evs), sym.Key)
	for i, e := range evs {
		fmt.Fprintf(p.w, "%2d. %s  %s", i+1, p.line(e.Location), e.Role)
		if len(e.Flags) > 0 {
			fmt.Fprintf(p.w, " (%s)", strings.Join(e.Flags, ", "))
		}
		if e.PassedTo != nil {
			fmt.Fprintf(p.w, "  -> argument %d of %s", e.ArgIndex, short(e.PassedTo.Key))
		}
		if e.FlowTo != "" {
			fmt.Fprintf(p.w, "  -> flows into %s", short(e.FlowTo))
		}
		fmt.Fprintln(p.w)
		fmt.Fprint(p.w, p.snippet(e.Location))
	}
	return nil
}

func (p *printer) calls(sym query.SymbolRef, kind string, edges []query.CallEdge) error {
	if p.json {
		return p.emit(map[string]any{"symbol": sym, "kind": kind, "edges": edges})
	}
	if len(edges) == 0 {
		fmt.Fprintf(p.w, "no %s of %s\n", kind, sym.Key)
		return nil
	}
	fmt.Fprintf(p.w, "%s of %s\n", kind, sym.Key)
	for _, e := range edges {
		callee := "<unknown target>"
		if e.Callee != nil {
			callee = short(e.Callee.Key)
		}
		label := string(e.Certainty)
		if e.Dispatch != "" {
			label = e.Dispatch + ", " + label
		}
		fmt.Fprintf(p.w, "\n%s -> %s   [%s]  %d call site(s)\n", short(e.Caller.Key), callee, label, len(e.Sites))
		if e.Via != "" {
			fmt.Fprintf(p.w, "  found through interface method %s\n", short(e.Via))
		}
		if e.Note != "" {
			fmt.Fprintf(p.w, "  note: %s\n", e.Note)
		}
		for _, o := range e.Sites {
			fmt.Fprintf(p.w, "  %s", p.line(o.Location))
			if len(o.Flags) > 0 {
				fmt.Fprintf(p.w, " (%s)", strings.Join(o.Flags, ", "))
			}
			fmt.Fprintln(p.w)
			fmt.Fprint(p.w, p.snippet(o.Location))
		}
		if len(e.Candidates) > 0 {
			fmt.Fprintln(p.w, "  possible concrete targets (candidates, not certain):")
			for _, c := range e.Candidates {
				fmt.Fprintf(p.w, "    %s  (%s)\n", short(c.Key), c.Kind)
			}
		}
	}
	return nil
}

func (p *printer) types(sym query.SymbolRef, uses []query.TypeUse) error {
	if p.json {
		return p.emit(map[string]any{"symbol": sym, "types": uses})
	}
	fmt.Fprintf(p.w, "%d type(s) used by %s\n", len(uses), sym.Key)
	for _, u := range uses {
		fmt.Fprintf(p.w, "  %s  (%s)  %d use(s)", u.Type.Key, u.Type.Kind, u.Uses)
		if u.Constructed > 0 {
			fmt.Fprintf(p.w, ", %d construction(s)", u.Constructed)
		}
		if u.Type.External {
			fmt.Fprint(p.w, "  [external]")
		}
		fmt.Fprintln(p.w)
	}
	return nil
}

func (p *printer) implements(r *query.ImplementsResult) error {
	if p.json {
		return p.emit(r)
	}
	if r.IsInterface {
		fmt.Fprintf(p.w, "%d implementer(s) of %s\n", len(r.Implementers), r.Symbol.Key)
		for _, im := range r.Implementers {
			var how []string
			if im.Interface {
				how = append(how, "interface with more methods")
			}
			if im.ViaPointer {
				how = append(how, "only *T implements it (pointer receivers)")
			}
			if im.ViaEmbedding {
				how = append(how, "via embedding")
			}
			fmt.Fprintf(p.w, "  %s", im.Type.Key)
			if len(how) > 0 {
				fmt.Fprintf(p.w, "  (%s)", strings.Join(how, "; "))
			}
			fmt.Fprintln(p.w)
		}
	} else {
		fmt.Fprintf(p.w, "%s implements %d interface(s)\n", r.Symbol.Key, len(r.Implemented))
		for _, im := range r.Implemented {
			fmt.Fprintf(p.w, "  %s", im.Interface.Key)
			if im.ViaPointer {
				fmt.Fprint(p.w, "  (only *T)")
			}
			if im.ViaEmbedding {
				fmt.Fprint(p.w, "  (via embedding)")
			}
			fmt.Fprintln(p.w)
		}
	}
	fmt.Fprintf(p.w, "note: %s\n", r.Limits)
	return nil
}

func (p *printer) chain(from, to query.SymbolRef, c *query.Chain) error {
	if p.json {
		return p.emit(c)
	}
	if !c.Found {
		fmt.Fprintf(p.w, "no call chain from %s to %s\n  %s\n", from.Key, to.Key, c.Note)
		return nil
	}
	kind := "certain calls only"
	if c.Candidate {
		kind = "includes candidate steps"
	}
	fmt.Fprintf(p.w, "%s -> %s: %d call(s), %s\n", short(from.Key), short(to.Key), len(c.Steps), kind)
	for i, s := range c.Steps {
		fmt.Fprintf(p.w, "%2d. %s -> %s   [%s, %s]  at %s\n", i+1, short(s.From.Key), short(s.To.Key), s.Dispatch, s.Certainty, s.Site.Location.String())
	}
	if c.Note != "" {
		fmt.Fprintf(p.w, "note: %s\n", c.Note)
	}
	return nil
}
