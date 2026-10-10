package extract_test

import (
	"flag"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/txtar"

	"codeg/internal/extract"
	"codeg/internal/model"
)

var update = flag.Bool("update", false, "rewrite want.txt sections of the golden fixtures")

func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*.txtar")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	for _, file := range files {
		t.Run(strings.TrimSuffix(filepath.Base(file), ".txtar"), func(t *testing.T) {
			ar, err := txtar.ParseFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var want string
			dir := t.TempDir()
			for _, f := range ar.Files {
				if f.Name == "want.txt" {
					want = string(f.Data)
					continue
				}
				path := filepath.Join(dir, f.Name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, f.Data, 0o644); err != nil {
					t.Fatal(err)
				}
			}

			var got strings.Builder
			for _, p := range load(t, dir) {
				facts := run(t, p, dir)
				facts.Dump(&got)
				got.WriteString("\n")
				checkInvariants(t, facts)

				// Extraction is a pure function: a second run is identical.
				if again := run(t, p, dir); !reflect.DeepEqual(facts, again) {
					t.Errorf("%s: extraction is not deterministic", p.PkgPath)
				}
			}

			if *update {
				ar.Files = replaceWant(ar.Files, got.String())
				if err := os.WriteFile(file, txtar.Format(ar), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got.String() != want {
				t.Errorf("facts differ from want.txt (run with -update to accept)\n--- got ---\n%s--- want ---\n%s", got.String(), want)
			}
		})
	}
}

func replaceWant(files []txtar.File, data string) []txtar.File {
	for i := range files {
		if files[i].Name == "want.txt" {
			files[i].Data = []byte(data)
			return files
		}
	}
	return append(files, txtar.File{Name: "want.txt", Data: []byte(data)})
}

func load(t *testing.T, dir string) []*packages.Package {
	t.Helper()
	cfg := &packages.Config{
		Dir: dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Env:   append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod"),
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	// Index the test variant when there is one, and skip synthesized mains.
	var out []*packages.Package
	for _, p := range pkgs {
		if strings.HasSuffix(p.ID, ".test") {
			continue
		}
		out = append(out, p)
	}
	// Drop `p` when `p [p.test]` exists.
	have := map[string]bool{}
	for _, p := range out {
		if strings.Contains(p.ID, " [") {
			have[p.PkgPath] = true
		}
	}
	var final []*packages.Package
	for _, p := range out {
		if !strings.Contains(p.ID, " [") && have[p.PkgPath] {
			continue
		}
		final = append(final, p)
	}
	sort.Slice(final, func(i, j int) bool { return final[i].PkgPath < final[j].PkgPath })
	return final
}

func run(t *testing.T, p *packages.Package, dir string) *model.PackageFacts {
	t.Helper()
	var diags []model.Diagnostic
	for _, e := range p.Errors {
		d := model.Diagnostic{Msg: e.Msg}
		if parts := strings.Split(e.Pos, ":"); len(parts) >= 3 {
			d.File = filepath.Base(parts[0])
			fmt.Sscan(parts[1], &d.Line)
			fmt.Sscan(parts[2], &d.Col)
		}
		diags = append(diags, d)
	}
	facts, err := extract.Extract(extract.Input{
		Fset: p.Fset, Files: p.Syntax, Pkg: p.Types, Info: p.TypesInfo, Errors: diags,
		FilePath: func(abs string) string {
			rel, err := filepath.Rel(dir, abs)
			if err != nil {
				return abs
			}
			return filepath.ToSlash(rel)
		},
		ModuleOf: func(path string) (string, string) {
			if dep := findPkg(p, path); dep != nil && dep.Module != nil {
				return dep.Module.Path, dep.Module.Version
			}
			return "", ""
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return facts
}

func findPkg(root *packages.Package, path string) *packages.Package {
	seen := map[*packages.Package]bool{}
	var walk func(*packages.Package) *packages.Package
	walk = func(p *packages.Package) *packages.Package {
		if seen[p] {
			return nil
		}
		seen[p] = true
		if p.PkgPath == path {
			return p
		}
		for _, imp := range p.Imports {
			if r := walk(imp); r != nil {
				return r
			}
		}
		return nil
	}
	return walk(root)
}

// checkInvariants checks properties every PackageFacts must have.
func checkInvariants(t *testing.T, f *model.PackageFacts) {
	t.Helper()
	syms := map[model.Key]model.Symbol{}
	for i, s := range f.Symbols {
		if i > 0 && f.Symbols[i-1].Key.Compare(s.Key) >= 0 {
			t.Errorf("%s: symbols not strictly sorted at %s", f.Path, s.Key)
		}
		if s.Key.Pkg != f.Path {
			t.Errorf("%s: symbol %s owned by another package", f.Path, s.Key)
		}
		syms[s.Key] = s
	}
	ext := map[model.Key]bool{}
	for _, e := range f.Externals {
		ext[e.Key] = true
	}
	for i, o := range f.Occurrences {
		if i > 0 {
			p := f.Occurrences[i-1]
			if p.File > o.File || (p.File == o.File && (p.Line > o.Line || p.Line == o.Line && p.Col > o.Col)) {
				t.Errorf("%s: occurrences not in position order at %d:%d", f.Path, o.Line, o.Col)
			}
		}
		if o.Symbol == (model.Key{}) || o.Symbol.Pkg == "" {
			continue // unknown call, or universe symbol
		}
		if o.Symbol.Pkg == f.Path {
			if _, ok := syms[o.Symbol]; !ok && o.Role != model.RoleImport {
				t.Errorf("%s: occurrence %s at %d:%d names no symbol of the package", f.Path, o.Symbol, o.Line, o.Col)
			}
		} else if !ext[o.Symbol] {
			t.Errorf("%s: occurrence %s at %d:%d has no external stub", f.Path, o.Symbol, o.Line, o.Col)
		}
	}
	for _, e := range f.Edges {
		for _, k := range []model.Key{e.From, e.To} {
			if k.Pkg == f.Path {
				if _, ok := syms[k]; !ok {
					t.Errorf("%s: edge %s %s -> %s: %s is not a symbol", f.Path, e.Kind, e.From, e.To, k)
				}
			}
		}
	}
	// Every symbol other than the package has one owning edge. Fields are the
	// exception: `type B A` shares A's fields, so both types have them.
	owned := map[model.Key]int{}
	for _, e := range f.Edges {
		switch e.Kind {
		case model.EdgeContains, model.EdgeHasParam, model.EdgeHasResult, model.EdgeHasField,
			model.EdgeHasMethod, model.EdgeTypeParam, model.EdgeFile:
			if e.To.Pkg == f.Path {
				owned[e.To]++
			}
		}
	}
	for _, s := range f.Symbols {
		if s.Kind == model.KindPackage {
			continue
		}
		if n := owned[s.Key]; n != 1 && !(s.Kind == model.KindField && n > 1) {
			t.Errorf("%s: symbol %s (%s) has %d owning edges, want 1", f.Path, s.Key, s.Kind, n)
		}
	}
}

var (
	_ = ast.Ident{}
	_ = token.NoPos
	_ types.Object
)
