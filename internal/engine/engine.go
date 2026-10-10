// Package engine keeps a .codeg index in sync with the source tree. Index
// re-indexes only what changed: it recomputes each package's input hash,
// skips packages whose hash matches the manifest, and cuts off propagation
// when a re-indexed package's API hash did not change.
//
// The central invariant, tested in engine_test.go: the result of any sequence
// of incremental runs equals a from-scratch index of the same tree.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"

	"codeg/internal/extract"
	"codeg/internal/index"
	"codeg/internal/loader"
	"codeg/internal/model"
	"codeg/internal/plan"
)

// ExtractorSchema is the version of what the extractor produces. Bump it when
// an extractor change alters facts for unchanged source: it is part of every
// input hash, so all packages are re-indexed.
const ExtractorSchema = 6

// Options configure one indexing run.
type Options struct {
	Root     string // module or workspace root
	StoreDir string // default: Root/.codeg
	GOOS     string
	GOARCH   string
	Tags     []string
	CGO      string
	Logf     func(format string, args ...any)
}

// Result reports what a run did.
type Result struct {
	Reindexed []string // packages that were loaded and extracted
	Reused    []string // packages whose stored facts were kept
	Removed   []string // packages that no longer exist
	Committed bool     // whether a new manifest was written
	Manifest  *index.Manifest
}

func (o *Options) logf(format string, args ...any) {
	if o.Logf != nil {
		o.Logf(format, args...)
	}
}

// Index brings the index up to date with the source tree.
func Index(ctx context.Context, o Options) (*Result, error) {
	root, err := filepath.Abs(o.Root)
	if err != nil {
		return nil, err
	}
	storeDir := o.StoreDir
	if storeDir == "" {
		storeDir = filepath.Join(root, ".codeg")
	}
	cfg := loader.Config{Dir: root, GOOS: o.GOOS, GOARCH: o.GOARCH, Tags: o.Tags, CGO: o.CGO}
	store := index.OpenStore(storeDir)

	unlock, err := store.Lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	old, err := store.ReadManifest()
	switch {
	case errors.Is(err, index.ErrNoIndex):
		old = nil
	case err != nil:
		o.logf("ignoring unreadable manifest (%v): indexing from scratch", err)
		old = nil
	}

	g, err := cfg.LoadGraph(ctx)
	if err != nil {
		return nil, err
	}
	run := &run{
		o: o, ctx: ctx, cfg: cfg, store: store, g: g, old: old,
		envHash: envHash(g),
		files:   newFileHasher(root, old),
		api:     map[string]string{},
		entries: map[string]*index.PackageEntry{},
		refs:    map[string]index.SummaryPackage{},
	}
	return run.execute()
}

type run struct {
	o       Options
	ctx     context.Context
	cfg     loader.Config
	store   *index.Store
	g       *loader.Graph
	old     *index.Manifest
	envHash string
	files   *fileHasher

	api     map[string]string // API hash of every processed project package
	entries map[string]*index.PackageEntry
	refs    map[string]index.SummaryPackage
	res     Result
}

func (r *run) execute() (*Result, error) {
	imports := map[string][]string{}
	for path, p := range r.g.Packages {
		imports[path] = p.Imports
	}
	for _, wave := range plan.Waves(imports) {
		if err := r.processWave(wave); err != nil {
			return nil, err
		}
	}

	if r.old != nil {
		for i := range r.old.Packages {
			if _, ok := r.g.Packages[r.old.Packages[i].Path]; !ok {
				r.res.Removed = append(r.res.Removed, r.old.Packages[i].Path)
			}
		}
	}
	sort.Strings(r.res.Reindexed)
	sort.Strings(r.res.Reused)
	sort.Strings(r.res.Removed)

	m := &index.Manifest{
		Schema: ExtractorSchema, Config: r.g.Build, EnvHash: r.envHash,
		Root: r.g.Root, WrittenAt: time.Now().UnixNano(),
	}
	for _, path := range r.g.SortedPaths() {
		m.Packages = append(m.Packages, *r.entries[path])
	}

	unchanged := r.old != nil && len(r.res.Reindexed) == 0 && len(r.res.Removed) == 0 &&
		r.old.Schema == m.Schema && r.old.EnvHash == m.EnvHash && r.old.Root == m.Root
	if unchanged {
		// A summary that cannot be read (older format, damaged, missing) is
		// rewritten from the package files rather than left in place.
		if _, err := r.store.ReadSummary(r.old); err == nil {
			r.res.Manifest = r.old
			return &r.res, nil
		}
	}

	sum, err := r.summary()
	if err != nil {
		return nil, err
	}
	data := sum.Encode()
	m.Summary = index.SummaryFileName(data)
	if err := r.store.WriteSummary(m.Summary, data); err != nil {
		return nil, err
	}
	// The commit point: everything the manifest names is already on disk.
	if err := r.store.Commit(m); err != nil {
		return nil, err
	}
	r.res.Committed, r.res.Manifest = true, m
	if _, err := r.store.GC(m, r.old); err != nil {
		r.o.logf("garbage collection: %v", err)
	}
	return &r.res, nil
}

// job is a package whose stored facts do not match its current inputs.
type job struct {
	pkg   *loader.Package
	input string
	files []index.FileEntry
	group string // hash tying together the members of an import cycle, or ""
}

func (r *run) processWave(wave []plan.Group) error {
	var dirty []job
	for _, grp := range wave {
		jobs, err := r.inspectGroup(grp)
		if err != nil {
			return err
		}
		dirty = append(dirty, jobs...)
	}
	if len(dirty) == 0 {
		return nil
	}

	seen := map[string]bool{}
	var patterns []string
	for _, j := range dirty {
		base := strings.TrimSuffix(j.pkg.Path, "_test")
		if !seen[base] {
			seen[base] = true
			patterns = append(patterns, base)
		}
	}
	sort.Strings(patterns)
	r.o.logf("loading %d package(s): %s", len(patterns), strings.Join(patterns, " "))
	loaded, err := r.cfg.LoadFull(r.ctx, patterns)
	if err != nil {
		return err
	}
	byID := map[string]*packages.Package{}
	for _, p := range loaded {
		byID[p.ID] = p
	}
	for _, j := range dirty {
		p := byID[j.pkg.ID]
		if p == nil {
			return fmt.Errorf("package %s (%s) was not loaded", j.pkg.Path, j.pkg.ID)
		}
		if err := r.indexPackage(j, p); err != nil {
			return err
		}
	}
	return nil
}

// inspectGroup computes the input hash of each package of a group and keeps
// the ones whose stored facts are still valid. It returns the rest.
func (r *run) inspectGroup(grp plan.Group) ([]job, error) {
	inGroup := map[string]bool{}
	for _, m := range grp.Members {
		inGroup[m] = true
	}
	locals := map[string]string{}
	fileEntries := map[string][]index.FileEntry{}
	for _, path := range grp.Members {
		files, err := r.fileEntries(r.g.Packages[path])
		if err != nil {
			return nil, err
		}
		fileEntries[path] = files
		locals[path] = r.localHash(r.g.Packages[path], files)
	}
	var groupHash string
	if len(grp.Members) > 1 {
		h := sha256.New()
		for _, m := range grp.Members {
			fmt.Fprintf(h, "%s=%s;", m, locals[m])
		}
		groupHash = hex.EncodeToString(h.Sum(nil))
	}

	var dirty []job
	for _, path := range grp.Members {
		pkg := r.g.Packages[path]
		input := r.inputHash(pkg, locals[path], groupHash, inGroup)
		if e := r.reusable(path, input); e != nil {
			ne := *e
			ne.Files = fileEntries[path]
			ne.Imports = pkg.Imports
			r.entries[path] = &ne
			r.api[path] = e.APIHash
			r.res.Reused = append(r.res.Reused, path)
			continue
		}
		dirty = append(dirty, job{pkg: pkg, input: input, files: fileEntries[path], group: groupHash})
	}
	return dirty, nil
}

// reusable returns the stored entry for path if it was made from exactly
// these inputs and its package file is still there.
func (r *run) reusable(path, input string) *index.PackageEntry {
	if r.old == nil {
		return nil
	}
	e := r.old.Package(path)
	if e == nil || e.InputHash != input {
		return nil
	}
	if _, err := os.Stat(filepath.Join(r.store.Dir, "pkg", e.File)); err != nil {
		return nil
	}
	return e
}

func (r *run) indexPackage(j job, p *packages.Package) error {
	var diags []model.Diagnostic
	for _, e := range p.Errors {
		// go list -export reports a package that fails to compile as one
		// unpositioned entry holding the compiler's whole output ("# pkg\n...").
		// The same errors arrive individually, with positions, from the type
		// checker, so the blob is a duplicate.
		if e.Kind == packages.ListError && strings.HasPrefix(e.Msg, "# ") && len(p.Errors) > 1 {
			continue
		}
		diags = append(diags, diagnostic(e, r.g.Root))
	}
	for _, msg := range j.pkg.Errors {
		if len(p.Errors) == 0 {
			diags = append(diags, model.Diagnostic{Msg: msg})
		}
	}
	facts, err := extract.Extract(extract.Input{
		Fset: p.Fset, Files: p.Syntax, Pkg: p.Types, Info: p.TypesInfo, Errors: diags,
		Sources:  j.pkg.Files,
		FilePath: func(abs string) string { return relPath(r.g.Root, abs) },
		ModuleOf: func(path string) (string, string) {
			m := r.g.Deps[path]
			return m.Path, m.Version
		},
	})
	if err != nil {
		return fmt.Errorf("%s: %w", j.pkg.Path, err)
	}
	data, err := index.Encode(facts)
	if err != nil {
		return fmt.Errorf("%s: %w", j.pkg.Path, err)
	}
	file := index.PackageFileName(j.pkg.Path, j.input)
	if err := r.store.WritePackage(file, data); err != nil {
		return err
	}

	api := plan.APIHash(p.Types, func(path string) string { return r.depAPI(path, j.pkg.Path, j.group) })
	if j.group != "" {
		// Siblings in an import cycle are not available yet: fold in the
		// hash of the whole cycle instead.
		api = hashStrings("cycle-api", api, j.group)
	}
	r.api[j.pkg.Path] = api

	entry := &index.PackageEntry{
		Path: j.pkg.Path, ID: j.pkg.ID, Name: j.pkg.Name, Dir: relPath(r.g.Root, j.pkg.Dir),
		InputHash: j.input, APIHash: api, File: file, HasErrors: facts.HasErrors,
		Imports: j.pkg.Imports, Files: j.files,
	}
	if m := j.pkg.Module; m != nil {
		entry.Module, entry.Version = m.Path, m.Version
	}
	r.entries[j.pkg.Path] = entry
	r.refs[j.pkg.Path] = summaryOf(facts)
	r.res.Reindexed = append(r.res.Reindexed, j.pkg.Path)
	return nil
}

func diagnostic(e packages.Error, root string) model.Diagnostic {
	d := model.Diagnostic{Msg: e.Msg}
	// Pos is "file:line:col".
	parts := strings.Split(e.Pos, ":")
	if n := len(parts); n >= 3 {
		fmt.Sscan(parts[n-2], &d.Line)
		fmt.Sscan(parts[n-1], &d.Col)
		d.File = relPath(root, strings.Join(parts[:n-2], ":"))
	}
	return d
}

func summaryOf(f *model.PackageFacts) index.SummaryPackage {
	refs := map[string]bool{}
	for i := range f.Occurrences {
		if p := f.Occurrences[i].Symbol.Pkg; p != "" && p != f.Path {
			refs[p] = true
		}
	}
	for _, e := range f.Externals {
		if e.Key.Pkg != f.Path {
			refs[e.Key.Pkg] = true
		}
	}
	sp := index.SummaryPackage{Path: f.Path, Imports: f.Imports}
	for p := range refs {
		sp.Refs = append(sp.Refs, p)
	}
	sort.Strings(sp.Refs)
	methods := map[string]bool{}
	for _, ms := range f.MethodSets {
		for _, m := range ms.Methods {
			methods[m.Name] = true
		}
	}
	for m := range methods {
		sp.Methods = append(sp.Methods, m)
	}
	sort.Strings(sp.Methods)
	return sp
}

// summary builds the summary: fresh for re-indexed packages, carried over
// for reused ones (their facts did not change).
func (r *run) summary() (*index.Summary, error) {
	var oldSum *index.Summary
	if r.old != nil {
		if s, err := r.store.ReadSummary(r.old); err == nil {
			oldSum = s
		}
	}
	s := &index.Summary{}
	for _, path := range r.g.SortedPaths() {
		if sp, ok := r.refs[path]; ok {
			s.Packages = append(s.Packages, sp)
			continue
		}
		if oldSum != nil {
			if sp := oldSum.Package(path); sp != nil {
				s.Packages = append(s.Packages, *sp)
				continue
			}
		}
		// No usable old summary: recompute from the package file.
		rd, err := r.store.OpenPackage(r.entries[path])
		if err != nil {
			return nil, err
		}
		facts, err := rd.Facts()
		if err != nil {
			return nil, err
		}
		s.Packages = append(s.Packages, summaryOf(facts))
	}
	// Which module provides each external package the project imports.
	seen := map[string]bool{}
	for _, sp := range s.Packages {
		for _, imp := range sp.Imports {
			if _, project := r.g.Packages[imp]; project || seen[imp] {
				continue
			}
			seen[imp] = true
			m := r.g.Deps[imp] // zero for the standard library
			s.Deps = append(s.Deps, index.SummaryDep{Path: imp, Module: m.Path, Version: m.Version})
		}
	}
	sort.Slice(s.Deps, func(i, j int) bool { return s.Deps[i].Path < s.Deps[j].Path })
	return s, nil
}

// depAPI is the API hash of a package that some type in the package being
// indexed exposes.
func (r *run) depAPI(path, self, group string) string {
	if h, ok := r.api[path]; ok {
		return h
	}
	if _, project := r.g.Packages[path]; project {
		return "cycle" // a sibling in the same import cycle: covered by the group hash
	}
	return r.externalToken(path)
}

func (r *run) externalToken(path string) string {
	if m, ok := r.g.Deps[path]; ok {
		return "m:" + m.Path + "@" + m.Version
	}
	return "s:" + r.g.Build.GoVersion
}

func (r *run) fileEntries(p *loader.Package) ([]index.FileEntry, error) {
	var out []index.FileEntry
	add := func(paths []string, kind index.FileKind, reason func(string) string) error {
		for _, f := range paths {
			e, err := r.files.entry(f, kind)
			if err != nil {
				return err
			}
			if reason != nil {
				e.Reason = reason(f)
			}
			out = append(out, e)
		}
		return nil
	}
	if err := add(p.Files, index.FileGo, nil); err != nil {
		return nil, err
	}
	if err := add(p.Other, index.FileOther, nil); err != nil {
		return nil, err
	}
	reasons := map[string]string{}
	var excluded []string
	for _, x := range p.Excluded {
		excluded = append(excluded, x.Path)
		reasons[x.Path] = x.Reason
	}
	if err := add(excluded, index.FileExcluded, func(f string) string { return reasons[f] }); err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

// localHash covers everything about a package except its imports' API hashes.
func (r *run) localHash(p *loader.Package, files []index.FileEntry) string {
	h := newHasher()
	h.str("codeg-local")
	h.str(fmt.Sprint(ExtractorSchema, index.Version))
	b := r.g.Build
	h.strs(b.GoVersion, b.GOOS, b.GOARCH, b.CGO)
	h.strs(b.Tags...)
	h.str(r.envHash)
	h.str(p.Path)
	h.str(p.Name)
	if p.Module != nil {
		h.strs(p.Module.Path, p.Module.Version)
	}
	for _, f := range files {
		h.strs(fmt.Sprint(f.Kind), f.Path, f.Hash)
	}
	return h.sum()
}

// inputHash adds what the package's imports contribute: the API hash of each
// project import, and the module version of each other one.
func (r *run) inputHash(p *loader.Package, local, group string, inGroup map[string]bool) string {
	h := newHasher()
	h.str("codeg-input")
	h.str(local)
	h.str(group)
	for _, imp := range p.Imports {
		switch {
		case inGroup[imp]:
			h.strs(imp, "group")
		case r.g.Packages[imp] != nil:
			h.strs(imp, "p:"+r.api[imp])
		default:
			h.strs(imp, r.externalToken(imp))
		}
	}
	return h.sum()
}

// envHash covers module resolution state: go.mod, go.sum, go.work,
// go.work.sum and the vendor manifest.
func envHash(g *loader.Graph) string {
	h := newHasher()
	h.str("codeg-env")
	var files []string
	if g.GoMod != "" && g.GoMod != os.DevNull {
		files = append(files, g.GoMod, filepath.Join(filepath.Dir(g.GoMod), "go.sum"))
	}
	for _, d := range g.ModDirs { // every module of a workspace
		files = append(files, filepath.Join(d, "go.mod"), filepath.Join(d, "go.sum"))
	}
	if g.GoWork != "" {
		files = append(files, g.GoWork, g.GoWork+".sum")
	}
	files = append(files, filepath.Join(g.Root, "vendor", "modules.txt"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			h.strs(relPath(g.Root, f), "absent")
			continue
		}
		sum := sha256.Sum256(data)
		h.strs(relPath(g.Root, f), hex.EncodeToString(sum[:]))
	}
	return h.sum()
}

func relPath(root, p string) string {
	if p == "" {
		return ""
	}
	if rel, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(p)
}

func hashStrings(parts ...string) string {
	h := newHasher()
	h.strs(parts...)
	return h.sum()
}

type hasher struct{ h hash.Hash }

func newHasher() *hasher { return &hasher{h: sha256.New()} }

func (h *hasher) str(s string) { fmt.Fprintf(h.h, "%d:%s;", len(s), s) }
func (h *hasher) strs(ss ...string) {
	for _, s := range ss {
		h.str(s)
	}
}
func (h *hasher) sum() string { return hex.EncodeToString(h.h.Sum(nil)) }
