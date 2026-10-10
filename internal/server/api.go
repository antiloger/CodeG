package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"codeg/internal/index"
	"codeg/internal/model"
	"codeg/internal/query"
)

func (s *Server) status(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	m := ix.Manifest
	var files, excluded, withErrors int
	for i := range m.Packages {
		if m.Packages[i].HasErrors {
			withErrors++
		}
		for _, f := range m.Packages[i].Files {
			if f.Kind == index.FileExcluded {
				excluded++
			} else {
				files++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root":       m.Root,
		"config":     m.Config,
		"packages":   len(m.Packages),
		"files":      files,
		"excluded":   excluded,
		"withErrors": withErrors,
		"indexedAt":  time.Unix(0, m.WrittenAt).UTC().Format(time.RFC3339),
		// generation changes whenever a new index is committed; the UI polls it.
		"generation": strconv.FormatInt(m.WrittenAt, 10),
	})
}

func graphOptions(r *http.Request) query.GraphOptions {
	return query.GraphOptions{
		Tests: boolParam(r, "tests"), Generated: boolParam(r, "generated"),
		Methods: boolParam(r, "methods"), Max: intParam(r, "max", 0),
	}
}

func (s *Server) graphPackages(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ix.PackagesGraph(query.PackagesOptions{
		Tests: boolParam(r, "tests"), Externals: boolParam(r, "externals"),
	}))
}

func (s *Server) graphPackage(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	pkg := r.URL.Query().Get("key")
	g, err := ix.PackageGraph(pkg, graphOptions(r))
	if err != nil {
		fail(w, fmt.Errorf("%w: package %q", query.ErrNotFound, pkg))
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) keyParam(ix *query.Index, w http.ResponseWriter, r *http.Request) (model.Key, bool) {
	k, err := ix.ParseKey(r.URL.Query().Get("key"))
	if err != nil {
		fail(w, err)
		return model.Key{}, false
	}
	return k, true
}

func (s *Server) graphFunction(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	k, ok := s.keyParam(ix, w, r)
	if !ok {
		return
	}
	g, err := ix.FunctionGraph(k, graphOptions(r))
	if err != nil {
		if strings.Contains(err.Error(), "not a function") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) symbol(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	k, ok := s.keyParam(ix, w, r)
	if !ok {
		return
	}
	d, err := ix.Detail(k)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// source serves a snippet. start and end are line numbers; hl is an optional
// highlight range line:col-endline:endcol.
func (s *Server) source(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	req := query.SourceRequest{
		File:      q.Get("file"),
		StartLine: intParam(r, "start", 1),
		EndLine:   intParam(r, "end", 0),
		Context:   min(intParam(r, "context", 3), 50),
	}
	if hl := q.Get("hl"); hl != "" {
		var l, c, el, ec int
		if _, err := fmt.Sscanf(hl, "%d:%d-%d:%d", &l, &c, &el, &ec); err != nil {
			writeError(w, http.StatusBadRequest, "hl must be line:col-endline:endcol")
			return
		}
		req.Highlight = &query.Location{Line: l, Col: c, EndLine: el, EndCol: ec}
	}
	v, err := ix.Source(req)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// resolve is the fuzzy lookup for humans: it returns every match of a spec.
func (s *Server) resolve(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	found, err := ix.Resolve(r.URL.Query().Get("spec"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matches": found})
}

func (s *Server) filter(r *http.Request) query.Filter {
	return query.Filter{NoTests: !boolParam(r, "tests"), NoGenerated: !boolParam(r, "generated")}
}

func (s *Server) references(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	k, ok := s.keyParam(ix, w, r)
	if !ok {
		return
	}
	occs, err := ix.References(k, s.filter(r))
	if err != nil {
		fail(w, err)
		return
	}
	ix.AttachText(occs)
	writeJSON(w, http.StatusOK, map[string]any{"occurrences": occs})
}

func (s *Server) calls(ix *query.Index, w http.ResponseWriter, r *http.Request) {
	k, ok := s.keyParam(ix, w, r)
	if !ok {
		return
	}
	var (
		edges []query.CallEdge
		err   error
	)
	if strings.HasSuffix(r.URL.Path, "/callers") {
		edges, err = ix.Callers(k, s.filter(r))
	} else {
		edges, err = ix.Callees(k, s.filter(r))
	}
	if err != nil {
		fail(w, err)
		return
	}
	budget := query.MaxTextLines
	for i := range edges {
		for j := range edges[i].Sites {
			if budget == 0 {
				break
			}
			edges[i].Sites[j].Text = ix.Snippet(edges[i].Sites[j].Location)
			budget--
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"edges": edges})
}
