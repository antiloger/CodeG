// Package query answers questions about an index: where a symbol is defined,
// who references or calls it, what implements an interface. It loads package
// files lazily and never guesses: anything static analysis cannot decide is
// reported as unknown or as a labelled candidate.
//
// Results are plain data structs so the CLI and the HTTP server share one
// implementation.
package query

import (
	"container/list"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"codeg/internal/index"
	"codeg/internal/model"
)

// Index is an open index. It is safe for concurrent use.
type Index struct {
	Root     string
	Manifest *index.Manifest
	Summary  *index.Summary

	store *index.Store

	mu      sync.Mutex
	readers map[string]*list.Element // package path -> element holding *cached
	lru     *list.List
	lines   map[string][]string // source files by relative path, for snippets
	ext     map[model.Key]*SymbolRef
	calls   map[string]callData

	refBy      map[string][]string // package -> packages whose code refers to it
	methodPkgs map[string][]string // method name -> packages with a type that has it
	byFile     map[string]string   // relative file path -> package path
}

type cached struct {
	pkg string
	r   *index.Reader
}

const readerCacheSize = 128

// Open opens the index in storeDir.
func Open(storeDir string) (*Index, error) {
	s := index.OpenStore(storeDir)
	m, err := s.ReadManifest()
	if err != nil {
		return nil, err
	}
	sum, err := s.ReadSummary(m)
	if err != nil {
		return nil, fmt.Errorf("reading summary: %w", err)
	}
	ix := &Index{
		Root: m.Root, Manifest: m, Summary: sum, store: s,
		readers: map[string]*list.Element{}, lru: list.New(),
		lines: map[string][]string{}, ext: map[model.Key]*SymbolRef{}, calls: map[string]callData{},
		refBy: map[string][]string{}, methodPkgs: map[string][]string{}, byFile: map[string]string{},
	}
	for _, p := range sum.Packages {
		for _, ref := range p.Refs {
			ix.refBy[ref] = append(ix.refBy[ref], p.Path)
		}
		for _, name := range p.Methods {
			ix.methodPkgs[name] = append(ix.methodPkgs[name], p.Path)
		}
	}
	for i := range m.Packages {
		for _, f := range m.Packages[i].Files {
			if f.Kind == index.FileGo {
				ix.byFile[f.Path] = m.Packages[i].Path
			}
		}
	}
	return ix, nil
}

// Packages returns the indexed package paths.
func (ix *Index) Packages() []string {
	out := make([]string, len(ix.Manifest.Packages))
	for i := range ix.Manifest.Packages {
		out[i] = ix.Manifest.Packages[i].Path
	}
	return out
}

// Reader returns the reader for a package, opening its file if needed.
func (ix *Index) Reader(pkg string) (*index.Reader, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if el, ok := ix.readers[pkg]; ok {
		ix.lru.MoveToFront(el)
		return el.Value.(*cached).r, nil
	}
	e := ix.Manifest.Package(pkg)
	if e == nil {
		return nil, fmt.Errorf("package %s is not indexed", pkg)
	}
	r, err := ix.store.OpenPackage(e)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", pkg, err)
	}
	ix.readers[pkg] = ix.lru.PushFront(&cached{pkg, r})
	for ix.lru.Len() > readerCacheSize {
		last := ix.lru.Back()
		ix.lru.Remove(last)
		delete(ix.readers, last.Value.(*cached).pkg)
	}
	return r, nil
}

// referrers returns the packages that can contain an occurrence of a symbol
// owned by pkg: the package itself and every package that refers to it. That
// is wider than "importers": a method promoted through embedding reaches
// callers that never import the declaring package.
func (ix *Index) referrers(pkg string) []string {
	if pkg == "" { // universe symbol: anywhere
		return ix.Packages()
	}
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if !seen[p] && ix.Manifest.Package(p) != nil {
			seen[p] = true
			out = append(out, p)
		}
	}
	add(pkg)
	for _, p := range ix.refBy[pkg] {
		add(p)
	}
	sort.Strings(out)
	return out
}

// Location is a source range in an indexed file.
type Location struct {
	Package   string `json:"package"`
	File      string `json:"file"` // relative to the module root
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	Test      bool   `json:"test,omitempty"`
	Generated bool   `json:"generated,omitempty"`
}

func (l Location) String() string { return fmt.Sprintf("%s:%d:%d", l.File, l.Line, l.Col) }

func (ix *Index) location(pkg string, files []model.FileFact, r model.Range) Location {
	l := Location{Package: pkg, Line: int(r.Line), Col: int(r.Col), EndLine: int(r.EndLine), EndCol: int(r.EndCol)}
	if int(r.File) < len(files) {
		f := files[r.File]
		l.File, l.Test, l.Generated = f.Path, f.Test, f.Generated
	}
	return l
}

// Snippet returns the source line at loc, trimmed, or "" if the file cannot be
// read. It reads the working tree, so it can be stale; callers that must know
// compare against the manifest's file hash.
func (ix *Index) Snippet(loc Location) string {
	if loc.File == "" || loc.Line <= 0 {
		return ""
	}
	ix.mu.Lock()
	lines, ok := ix.lines[loc.File]
	ix.mu.Unlock()
	if !ok {
		path := loc.File
		if !filepath.IsAbs(path) {
			path = filepath.Join(ix.Root, filepath.FromSlash(path))
		}
		if data, err := os.ReadFile(path); err == nil {
			lines = strings.Split(string(data), "\n")
		}
		ix.mu.Lock()
		ix.lines[loc.File] = lines
		ix.mu.Unlock()
	}
	if loc.Line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[loc.Line-1])
}
