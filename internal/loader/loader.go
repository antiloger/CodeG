// Package loader wraps go/packages for the indexer. It provides two loads: a
// cheap metadata-only load that gives the current package graph (used to
// decide what changed), and a full load that type-checks only the packages
// that need re-indexing.
package loader

import (
	"bytes"
	"context"
	"fmt"
	"go/build/constraint"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"codeg/internal/index"
)

// Config selects what to load and under which build configuration. An index
// is made for exactly one configuration.
type Config struct {
	Dir    string   // module or workspace root
	GOOS   string   // empty: host
	GOARCH string   // empty: host
	Tags   []string // extra build tags
	CGO    string   // "", "0" or "1"
}

func (c Config) env() []string {
	env := os.Environ()
	if c.GOOS != "" {
		env = append(env, "GOOS="+c.GOOS)
	}
	if c.GOARCH != "" {
		env = append(env, "GOARCH="+c.GOARCH)
	}
	if c.CGO != "" {
		env = append(env, "CGO_ENABLED="+c.CGO)
	}
	return env
}

func (c Config) buildFlags() []string {
	if len(c.Tags) == 0 {
		return nil
	}
	return []string{"-tags=" + strings.Join(c.Tags, ",")}
}

// Module identifies the module that provides a package.
type Module struct{ Path, Version string }

// Excluded is a Go file that build constraints exclude from the package.
type Excluded struct {
	Path   string
	Reason string
}

// Package is the metadata of one indexed package: the canonical variant only.
type Package struct {
	Path     string // import path; external test packages end in _test
	ID       string
	Name     string
	Dir      string
	Files    []string // compiled Go source files
	Other    []string // non-Go sources and embedded files
	Excluded []Excluded
	Imports  []string // direct import paths, sorted
	Module   *Module  // nil for the standard library
	Errors   []string // list errors (missing package, ...)
}

// Graph is the metadata of the whole project.
type Graph struct {
	Build    index.BuildConfig
	Root     string
	GoMod    string
	GoWork   string
	ModDirs  []string            // directories of the main modules: one, or every module of a workspace
	Packages map[string]*Package // project packages by import path
	// Deps maps every package in the dependency graph, project or not, to its
	// module. Standard-library packages are absent.
	Deps map[string]Module
}

// SortedPaths returns the project package paths in order.
func (g *Graph) SortedPaths() []string {
	out := make([]string, 0, len(g.Packages))
	for p := range g.Packages {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (c Config) goEnv(ctx context.Context, keys ...string) (map[string]string, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"env"}, keys...)...)
	cmd.Dir, cmd.Env = c.Dir, c.env()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if _, lp := exec.LookPath("go"); lp != nil {
			return nil, fmt.Errorf("the go command is not on PATH: codeg needs it to load packages")
		}
		return nil, fmt.Errorf("go env: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	res := map[string]string{}
	for i, k := range keys {
		if i < len(lines) {
			res[k] = lines[i]
		}
	}
	return res, nil
}

// LoadGraph loads package metadata for ./... without type-checking.
func (c Config) LoadGraph(ctx context.Context) (*Graph, error) {
	env, err := c.goEnv(ctx, "GOVERSION", "GOOS", "GOARCH", "CGO_ENABLED", "GOMOD", "GOWORK")
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(c.Dir)
	if err != nil {
		return nil, err
	}
	g := &Graph{
		Root: root, GoMod: env["GOMOD"], GoWork: env["GOWORK"],
		Build: index.BuildConfig{
			GoVersion: env["GOVERSION"], GOOS: env["GOOS"], GOARCH: env["GOARCH"],
			CGO: env["CGO_ENABLED"], Tags: sortedCopy(c.Tags),
		},
		Packages: map[string]*Package{}, Deps: map[string]Module{},
	}
	inWorkspace := g.GoWork != "" && g.GoWork != "off" && g.GoWork != os.DevNull
	if (g.GoMod == "" || g.GoMod == os.DevNull) && !inWorkspace {
		return nil, fmt.Errorf("%s is not inside a Go module (no go.mod found)", root)
	}

	// `./...` at a workspace root matches nothing, so name each module.
	patterns := []string{"./..."}
	if dirs, err := c.mainModuleDirs(ctx); err == nil {
		g.ModDirs = dirs
		if inWorkspace {
			patterns = patterns[:0]
			for _, d := range dirs {
				if rel, err := filepath.Rel(root, d); err == nil && !strings.HasPrefix(rel, "..") {
					d = "./" + filepath.ToSlash(rel)
				}
				patterns = append(patterns, strings.TrimSuffix(d, "/")+"/...")
			}
		}
	} else if inWorkspace {
		return nil, err
	}

	pkgs, err := packages.Load(&packages.Config{
		Context: ctx, Dir: c.Dir, Env: c.env(), BuildFlags: c.buildFlags(), Tests: true,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedModule | packages.NeedEmbedFiles,
	}, patterns...)
	if err != nil {
		return nil, err
	}

	packages.Visit(pkgs, func(p *packages.Package) bool { return true }, func(p *packages.Package) {
		if m := moduleOf(p); m != nil {
			g.Deps[p.PkgPath] = *m
		}
	})

	// Choose the canonical variant of each package: `p [p.test]` (which has
	// the in-package test files) over `p`, and `p_test [p.test]` as its own
	// package. The synthesized `p.test` main is skipped.
	byPath := map[string]*packages.Package{}
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test") || inNodeModules(p) {
			continue
		}
		prev := byPath[p.PkgPath]
		if prev == nil || (strings.Contains(p.ID, " [") && !strings.Contains(prev.ID, " [")) {
			byPath[p.PkgPath] = p
		}
	}
	for path, p := range byPath {
		md := c.metadata(p)
		// A directory holding only an external test file also produces an
		// empty `p`; it has nothing to index. A package whose files are all
		// excluded by build constraints is kept: the excluded list is useful.
		if len(md.Files) == 0 && len(md.Other) == 0 && len(md.Excluded) == 0 {
			continue
		}
		g.Packages[path] = md
	}
	return g, nil
}

// inNodeModules reports whether a package lives under a node_modules
// directory. `./...` matches those (Go only skips testdata and dot or
// underscore directories), but they are JavaScript tooling's files, not part
// of the project.
func inNodeModules(p *packages.Package) bool {
	files := p.GoFiles
	if len(files) == 0 {
		files = p.CompiledGoFiles
	}
	if len(files) == 0 {
		files = p.IgnoredFiles
	}
	for _, f := range files {
		for _, part := range strings.Split(filepath.ToSlash(filepath.Dir(f)), "/") {
			if part == "node_modules" {
				return true
			}
		}
		return false
	}
	return false
}

// mainModuleDirs lists the directories of the main modules: the module itself,
// or every module of a workspace.
func (c Config) mainModuleDirs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}")
	cmd.Dir, cmd.Env = c.Dir, c.env()
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list -m: %v: %s", err, strings.TrimSpace(errb.String()))
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			dirs = append(dirs, line)
		}
	}
	sort.Strings(dirs)
	return dirs, nil
}

func moduleOf(p *packages.Package) *Module {
	m := p.Module
	if m == nil {
		return nil
	}
	if m.Replace != nil {
		// The module keeps its identity; the replacement goes in the version so
		// that changing it changes the input hash. A directory replacement has
		// no version: edits to its files are not tracked by the importers.
		v := m.Replace.Version
		if v == "" {
			v = "=> " + m.Replace.Path
		}
		return &Module{Path: m.Path, Version: v}
	}
	return &Module{Path: m.Path, Version: m.Version}
}

func (c Config) metadata(p *packages.Package) *Package {
	pkg := &Package{
		Path: p.PkgPath, ID: p.ID, Name: p.Name, Module: moduleOf(p),
		Files: sortedCopy(p.GoFiles),
		Other: sortedCopy(append(append([]string(nil), p.OtherFiles...), p.EmbedFiles...)),
	}
	if len(p.GoFiles) > 0 {
		pkg.Dir = filepath.Dir(p.GoFiles[0])
	} else if len(p.CompiledGoFiles) > 0 {
		pkg.Dir = filepath.Dir(p.CompiledGoFiles[0])
	}
	for _, f := range p.IgnoredFiles {
		if strings.HasSuffix(f, ".go") {
			pkg.Excluded = append(pkg.Excluded, Excluded{Path: f, Reason: ExcludeReason(f, c.tagSet())})
		}
	}
	sort.Slice(pkg.Excluded, func(i, j int) bool { return pkg.Excluded[i].Path < pkg.Excluded[j].Path })
	for path := range p.Imports {
		pkg.Imports = append(pkg.Imports, path)
	}
	sort.Strings(pkg.Imports)
	for _, e := range p.Errors {
		pkg.Errors = append(pkg.Errors, e.Error())
	}
	return pkg
}

func (c Config) tagSet() map[string]bool {
	set := map[string]bool{}
	for _, t := range c.Tags {
		set[t] = true
	}
	return set
}

func sortedCopy(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}

// ExcludeReason explains why a Go file is excluded from the build: its
// //go:build line, or a GOOS/GOARCH file-name suffix.
func ExcludeReason(path string, tags map[string]bool) string {
	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "package ") {
				break
			}
			if constraint.IsGoBuild(line) {
				if expr, err := constraint.Parse(line); err == nil {
					return "build constraint: " + expr.String()
				}
			}
		}
	}
	name := strings.TrimSuffix(filepath.Base(path), ".go")
	name = strings.TrimSuffix(name, "_test")
	parts := strings.Split(name, "_")
	if n := len(parts); n > 1 {
		return "file name suffix: _" + strings.Join(parts[n-1:], "_")
	}
	return "excluded by the build configuration"
}

// LoadFull type-checks the named packages from source. Their dependencies
// come from export data, so only these packages are parsed. Patterns are
// import paths; the results include every test variant of each.
func (c Config) LoadFull(ctx context.Context, patterns []string) ([]*packages.Package, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	return packages.Load(&packages.Config{
		Context: ctx, Dir: c.Dir, Env: c.env(), BuildFlags: c.buildFlags(), Tests: true,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedModule | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedExportFile,
	}, patterns...)
}
