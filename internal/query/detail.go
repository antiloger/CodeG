package query

import (
	"sort"

	"codeg/internal/model"
)

// Relation links a symbol to another one through a structural edge.
type Relation struct {
	Kind   string    `json:"kind"` // has_field, has_method, embeds, has_param, has_result, contains, alias_of, type_param
	Symbol SymbolRef `json:"symbol"`
}

// SymbolDetail is everything the sidebar shows about one symbol.
type SymbolDetail struct {
	Symbol     SymbolRef         `json:"symbol"`
	Source     *SourceView       `json:"source,omitempty"`    // the definition, with the line highlighted
	Stats      map[string]int    `json:"stats"`               // occurrences by role, across the index
	Relations  []Relation        `json:"relations,omitempty"` // members, parameters, embedded types
	Callers    *int              `json:"callers,omitempty"`   // functions only: distinct call sites
	Callees    *int              `json:"callees,omitempty"`
	Implements *ImplementsResult `json:"implements,omitempty"` // types only
	Events     []Event           `json:"events,omitempty"`     // variables only, in source order
	Limits     []string          `json:"limits,omitempty"`     // what static analysis cannot tell, for this kind of symbol
}

// Detail assembles the sidebar data for a symbol.
func (ix *Index) Detail(key model.Key) (*SymbolDetail, error) {
	ref := ix.symbolRef(key)
	if ref.Kind == "unknown" {
		return nil, ErrNotFound
	}
	d := &SymbolDetail{Symbol: ref, Stats: map[string]int{}}

	occs, err := ix.Occurrences(key, Filter{IncludeDefinition: true})
	if err != nil {
		return nil, err
	}
	for _, o := range occs {
		d.Stats[o.Role]++
	}

	if ref.Location != nil {
		start, end := ref.Location.Line, ref.Location.EndLine
		if decl := ix.declLines(key); decl != nil {
			start, end = decl[0], min(decl[1], decl[0]+39)
		}
		d.Source, _ = ix.Source(SourceRequest{File: ref.Location.File, StartLine: start, EndLine: end, Context: 2, Highlight: ref.Location})
	}

	if ix.Manifest.Package(key.Pkg) != nil {
		if r, err := ix.Reader(key.Pkg); err == nil {
			if edges, err := r.Edges(); err == nil {
				for _, e := range edges {
					if e.From == key && e.Kind != model.EdgeFile {
						d.Relations = append(d.Relations, Relation{Kind: e.Kind.String(), Symbol: ix.symbolRef(e.To)})
					}
				}
				sort.SliceStable(d.Relations, func(i, j int) bool {
					if d.Relations[i].Kind != d.Relations[j].Kind {
						return d.Relations[i].Kind < d.Relations[j].Kind
					}
					return d.Relations[i].Symbol.k.Compare(d.Relations[j].Symbol.k) < 0
				})
			}
		}
	}

	switch ref.Kind {
	case "func", "method", "func_lit":
		if in, err := ix.Callers(key, Filter{}); err == nil {
			n := 0
			for _, e := range in {
				n += len(e.Sites)
			}
			d.Callers = &n
		}
		if out, err := ix.Callees(key, Filter{}); err == nil {
			n := 0
			for _, e := range out {
				n += len(e.Sites)
			}
			d.Callees = &n
		}
		d.Limits = append(d.Limits,
			"interface calls list candidates, not the method that runs",
			"calls through function values are recorded as dynamic, with no target")
	case "type":
		if res, err := ix.Implements(key); err == nil {
			d.Implements = res
		}
	case "local", "param", "result", "var", "const", "field":
		if evs, err := ix.Events(key, Filter{}); err == nil {
			if len(evs) > 500 {
				evs = evs[:500]
			}
			for i := range evs {
				if i < MaxTextLines {
					evs[i].Text = ix.Snippet(evs[i].Location)
				}
			}
			d.Events = evs
		}
		d.Limits = append(d.Limits,
			"events are in source order, not execution order",
			"writes through pointers, slices and maps may alias other variables: address_taken and write_partial only record that a write may happen")
	}
	return d, nil
}

// declLines returns the first and last line of a symbol's declaration.
func (ix *Index) declLines(key model.Key) []int {
	r, err := ix.Reader(key.Pkg)
	if err != nil {
		return nil
	}
	s, ok, err := r.Symbol(key)
	if err != nil || !ok || s.Decl.Line == 0 {
		return nil
	}
	return []int{int(s.Decl.Line), int(s.Decl.EndLine)}
}
