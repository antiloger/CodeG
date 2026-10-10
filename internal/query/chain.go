package query

import (
	"fmt"

	"codeg/internal/model"
)

// ChainStep is one call on a path.
type ChainStep struct {
	From      SymbolRef `json:"from"`
	To        SymbolRef `json:"to"`
	Dispatch  string    `json:"dispatch"`
	Certainty Certainty `json:"certainty"`
	Site      Occ       `json:"site"` // the first call site of this edge
}

// Chain is the shortest call path found between two functions.
type Chain struct {
	Found bool        `json:"found"`
	Steps []ChainStep `json:"steps,omitempty"`
	// Candidate is set when the path is only possible through an interface
	// call whose concrete target the index cannot determine. A path made of
	// certain calls alone is always preferred.
	Candidate bool   `json:"candidate,omitempty"`
	Note      string `json:"note,omitempty"`
}

type rawEdge struct {
	callee   model.Key
	dispatch model.Dispatch
	site     model.Occurrence
}

// callMap returns, for a package, the direct calls of each top-level function
// (closures folded into the function that contains them).
func (ix *Index) callMap(pkg string) (map[model.Key][]rawEdge, []model.FileFact, error) {
	ix.mu.Lock()
	if m, ok := ix.calls[pkg]; ok {
		ix.mu.Unlock()
		return m.edges, m.files, nil
	}
	ix.mu.Unlock()
	r, err := ix.Reader(pkg)
	if err != nil {
		return nil, nil, err
	}
	occs, err := r.Occurrences()
	if err != nil {
		return nil, nil, err
	}
	files, err := r.Files()
	if err != nil {
		return nil, nil, err
	}
	m := map[model.Key][]rawEdge{}
	for _, o := range occs {
		if o.Role == model.RoleCall && o.Symbol != (model.Key{}) {
			caller := topLevel(o.Enclosing)
			m[caller] = append(m[caller], rawEdge{callee: topLevel(o.Symbol), dispatch: o.Dispatch, site: o})
		}
	}
	ix.mu.Lock()
	ix.calls[pkg] = callData{m, files}
	ix.mu.Unlock()
	return m, files, nil
}

type callData struct {
	edges map[model.Key][]rawEdge
	files []model.FileFact
}

// CallChain finds the shortest path of calls from one function to another,
// up to maxDepth calls. It first tries certain edges only (static calls); if
// there is no such path it also follows interface calls to their possible
// implementations and marks the result as a candidate path.
func (ix *Index) CallChain(from, to model.Key, maxDepth int) (*Chain, error) {
	if maxDepth <= 0 {
		maxDepth = 15
	}
	from, to = topLevel(from), topLevel(to)
	for _, withCandidates := range []bool{false, true} {
		steps, err := ix.bfs(from, to, maxDepth, withCandidates)
		if err != nil {
			return nil, err
		}
		if steps != nil {
			c := &Chain{Found: true, Steps: steps, Candidate: withCandidates}
			if withCandidates {
				c.Note = "at least one step is an interface call: the concrete target is one of several candidates"
			}
			return c, nil
		}
	}
	return &Chain{Note: fmt.Sprintf("no call path of at most %d calls; calls through function values and reflection are not followed", maxDepth)}, nil
}

type parent struct {
	from model.Key
	edge rawEdge
	cand bool
}

func (ix *Index) bfs(from, to model.Key, maxDepth int, withCandidates bool) ([]ChainStep, error) {
	if from == to {
		return []ChainStep{}, nil
	}
	prev := map[model.Key]parent{}
	seen := map[model.Key]bool{from: true}
	frontier := []model.Key{from}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []model.Key
		for _, n := range frontier {
			if ix.Manifest.Package(n.Pkg) == nil {
				continue
			}
			m, _, err := ix.callMap(n.Pkg)
			if err != nil {
				return nil, err
			}
			for _, e := range m[n] {
				targets := []model.Key{e.callee}
				cand := false
				switch e.dispatch {
				case model.DispatchInterface:
					if !withCandidates {
						continue
					}
					cs, err := ix.candidateMethods(e.callee)
					if err != nil {
						return nil, err
					}
					targets, cand = nil, true
					for _, c := range cs {
						if c.Kind == "method" {
							targets = append(targets, c.k)
						}
					}
				case model.DispatchDynamic:
					continue
				}
				for _, t := range targets {
					t = topLevel(t)
					if seen[t] {
						continue
					}
					seen[t] = true
					prev[t] = parent{from: n, edge: e, cand: cand}
					if t == to {
						return ix.buildSteps(prev, from, to), nil
					}
					next = append(next, t)
				}
			}
		}
		frontier = next
	}
	return nil, nil
}

func (ix *Index) buildSteps(prev map[model.Key]parent, from, to model.Key) []ChainStep {
	var rev []ChainStep
	for at := to; at != from; {
		p := prev[at]
		_, files, _ := ix.callMap(p.from.Pkg)
		d := certaintyOf(p.edge.dispatch, at)
		if p.cand {
			d = Candidate
		}
		rev = append(rev, ChainStep{
			From: ix.symbolRef(p.from), To: ix.symbolRef(at),
			Dispatch: p.edge.dispatch.String(), Certainty: d,
			Site: ix.occFrom(p.from.Pkg, files, p.edge.site),
		})
		at = p.from
	}
	steps := make([]ChainStep, len(rev))
	for i, s := range rev {
		steps[len(rev)-1-i] = s
	}
	return steps
}
