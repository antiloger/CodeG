package model

import (
	"fmt"
	"io"
	"strings"
)

// Dump writes a stable, human-readable rendering of the facts. It backs the
// golden tests and `codeg` debugging output.
func (f *PackageFacts) Dump(w io.Writer) {
	fmt.Fprintf(w, "package %s (%s)", f.Path, f.Name)
	if f.HasErrors {
		fmt.Fprint(w, " has_errors")
	}
	fmt.Fprintln(w)
	for _, d := range f.Diagnostics {
		fmt.Fprintf(w, "diag %s:%d:%d %s\n", d.File, d.Line, d.Col, d.Msg)
	}
	for _, imp := range f.Imports {
		fmt.Fprintf(w, "imports %s\n", imp)
	}
	for _, file := range f.Files {
		fmt.Fprintf(w, "file %s%s%s\n", file.Path, flag(file.Generated, " generated"), flag(file.Test, " test"))
	}
	fmt.Fprintln(w, "-- symbols")
	for _, s := range f.Symbols {
		fmt.Fprintf(w, "%s %s", s.Kind, s.Key)
		if s.Detail != "" {
			fmt.Fprintf(w, "  %s", s.Detail)
		}
		if s.Pos.Line != 0 {
			fmt.Fprintf(w, "  @%s", f.pos(s.Pos))
		}
		if s.Decl.Line != 0 {
			fmt.Fprintf(w, "  decl=%d-%d", s.Decl.Line, s.Decl.EndLine)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "-- edges")
	for _, e := range f.Edges {
		fmt.Fprintf(w, "%s %s -> %s\n", e.Kind, e.From, e.To)
	}
	fmt.Fprintln(w, "-- occurrences")
	for _, o := range f.Occurrences {
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s %s", f.pos(o.Range), o.Role, symbolText(o.Symbol))
		if d := o.Dispatch.String(); d != "" {
			fmt.Fprintf(&b, " dispatch=%s", d)
		}
		for _, fl := range flagNames {
			if o.Flags&fl.f != 0 {
				b.WriteString(" +" + fl.s)
			}
		}
		if o.Call != 0 {
			fmt.Fprintf(&b, " call=%d", o.Call)
		}
		if o.ArgOf != 0 {
			fmt.Fprintf(&b, " arg=%d#%d", o.ArgOf, o.ArgIndex)
		}
		if o.FlowTo != (Key{}) {
			fmt.Fprintf(&b, " flow=%s", o.FlowTo)
		}
		if o.TypeArgs != "" {
			fmt.Fprintf(&b, " targs=[%s]", o.TypeArgs)
		}
		fmt.Fprintf(&b, "  in %s", o.Enclosing)
		fmt.Fprintln(w, b.String())
	}
	fmt.Fprintln(w, "-- method sets")
	for _, ms := range f.MethodSets {
		fmt.Fprintf(w, "%s%s\n", ms.Type, flag(ms.Interface, " (interface)"))
		for _, m := range ms.Methods {
			fmt.Fprintf(w, "  %s%s %s%s\n", flagStr(m.PtrRecv, "*"), m.Name, m.Sig, flagStr(m.Promoted, " (promoted)"))
		}
	}
	fmt.Fprintln(w, "-- externals")
	for _, e := range f.Externals {
		fmt.Fprintf(w, "%s %s", e.Kind, e.Key)
		if e.Detail != "" {
			fmt.Fprintf(w, "  %s", e.Detail)
		}
		if e.Module != "" {
			fmt.Fprintf(w, "  [%s@%s]", e.Module, e.Version)
		}
		fmt.Fprintln(w)
	}
}

func symbolText(k Key) string {
	if k == (Key{}) {
		return "<unknown>"
	}
	return k.String()
}

func flag(b bool, s string) string { return flagStr(b, s) }

func flagStr(b bool, s string) string {
	if b {
		return s
	}
	return ""
}

func (f *PackageFacts) pos(r Range) string {
	name := "?"
	if int(r.File) < len(f.Files) && r.File >= 0 {
		name = f.Files[r.File].Path
	}
	return fmt.Sprintf("%s:%d:%d", name, r.Line, r.Col)
}
