package model_test

import (
	"flag"
	"fmt"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/txtar"

	"codeg/internal/model"
)

var update = flag.Bool("update", false, "rewrite want.txt sections of the golden fixtures")

func TestKeys(t *testing.T) {
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
			pkgs := load(t, dir)

			namers := map[string]*model.Namer{}
			for _, p := range pkgs {
				namers[p.PkgPath] = model.NewNamer(p.Fset, p.Syntax, p.Types, p.TypesInfo)
			}
			var got strings.Builder
			for _, p := range pkgs {
				n := namers[p.PkgPath]
				got.WriteString(dump(p, n))
				checkCrossPackage(t, p, n, namers)
				checkUnique(t, p, n)
				checkTypesOnlyAgrees(t, p, n)
			}

			if *update {
				ar.Files = replaceWant(ar.Files, got.String())
				if err := os.WriteFile(file, txtar.Format(ar), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			if got.String() != want {
				t.Errorf("keys differ from want.txt (run with -update to accept)\n--- got ---\n%s--- want ---\n%s", got.String(), want)
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
			packages.NeedSyntax | packages.NeedTypesInfo,
		Env: append(os.Environ(), "GOWORK=off", "GOPROXY=off", "GOFLAGS=-mod=mod"),
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pkgs {
		for _, e := range p.Errors {
			t.Fatalf("fixture %s does not type-check: %v", p.ID, e)
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	return pkgs
}

type entry struct {
	off  int
	line string
}

func dump(p *packages.Package, n *model.Namer) string {
	var es []entry
	add := func(prefix string, pos token.Pos, name string, obj types.Object) {
		ps := p.Fset.Position(pos)
		res := "?"
		if r, ok := n.Ref(obj); ok {
			res = fmt.Sprintf("%s  %s", r.Kind, r.Key)
		}
		es = append(es, entry{ps.Offset + 1<<20*len(prefix), fmt.Sprintf("%s%s:%d:%d %s -> %s", prefix, filepath.Base(ps.Filename), ps.Line, ps.Column, name, res)})
	}
	info := p.TypesInfo
	for id, obj := range info.Defs {
		if obj != nil {
			add("", id.Pos(), id.Name, obj)
		}
	}
	for node, obj := range info.Implicits {
		if v, ok := obj.(*types.Var); ok {
			add("", node.Pos(), v.Name()+" (implicit)", v)
		}
	}
	for id, obj := range info.Uses {
		if _, isPkg := obj.(*types.PkgName); isPkg || obj.Pkg() == nil {
			continue
		}
		add("use ", id.Pos(), id.Name, obj)
	}
	sort.Slice(es, func(i, j int) bool {
		if es[i].off != es[j].off {
			return es[i].off < es[j].off
		}
		return es[i].line < es[j].line
	})
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", p.PkgPath)
	for _, e := range es {
		b.WriteString(e.line + "\n")
	}
	return b.String()
}

// checkUnique fails if two distinct objects of a package share a key.
func checkUnique(t *testing.T, p *packages.Package, n *model.Namer) {
	t.Helper()
	// The per-clause variables of a type switch deliberately share one key.
	clauseVar := map[types.Object]bool{}
	first := map[model.Key]bool{}
	for _, obj := range p.TypesInfo.Implicits {
		if v, ok := obj.(*types.Var); ok {
			if k, ok := n.Key(v); ok && first[k] {
				clauseVar[v] = true
			} else if ok {
				first[k] = true
			}
		}
	}
	seen := map[model.Key]types.Object{}
	for _, obj := range p.TypesInfo.Defs {
		if clauseVar[obj] {
			continue
		}
		if obj == nil {
			continue
		}
		k, ok := n.Key(obj)
		if !ok {
			continue
		}
		if prev, dup := seen[k]; dup && prev != obj {
			t.Errorf("%s: key %q shared by %v and %v", p.PkgPath, k, prev, obj)
		}
		seen[k] = obj
	}
}

// checkTypesOnlyAgrees checks that a Namer without syntax, which is what
// importers use for dependencies, produces the same key as the full Namer for
// every symbol it can name.
func checkTypesOnlyAgrees(t *testing.T, p *packages.Package, full *model.Namer) {
	t.Helper()
	bare := model.NewNamer(p.Fset, nil, p.Types, nil)
	for _, obj := range p.TypesInfo.Defs {
		if obj == nil {
			continue
		}
		got, ok := bare.Ref(obj)
		if !ok {
			continue
		}
		want, wok := full.Ref(obj)
		if !wok || got != want {
			t.Errorf("%s: types-only key for %v is %v, full namer says %v (ok=%v)", p.PkgPath, obj, got, want, wok)
		}
	}
}

// checkCrossPackage checks that the keys p's Namer computes for objects owned
// by other packages (types-only, no syntax) equal the keys the owning
// package's own Namer computes.
func checkCrossPackage(t *testing.T, p *packages.Package, n *model.Namer, namers map[string]*model.Namer) {
	t.Helper()
	for id, obj := range p.TypesInfo.Uses {
		if obj.Pkg() == nil || obj.Pkg() == p.Types {
			continue
		}
		owner, ok := namers[obj.Pkg().Path()]
		if !ok {
			continue // standard library: no full namer to compare with
		}
		got, gok := n.Ref(obj)
		want, wok := owner.Ref(obj)
		if gok != wok || got != want {
			t.Errorf("%s: %s: importer key %v (ok=%v), owner key %v (ok=%v)", p.PkgPath, p.Fset.Position(id.Pos()), got, gok, want, wok)
		}
	}
}
