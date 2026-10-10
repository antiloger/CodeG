package extract_test

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/packages"

	"codeg/internal/extract"
	"codeg/internal/model"
)

// TestRealWorld extracts real packages (this repository and a few large
// standard-library ones) and checks the facts invariants. It has no golden
// output: it exists to find panics and inconsistencies that hand-written
// fixtures miss.
func TestRealWorld(t *testing.T) {
	if testing.Short() {
		t.Skip("loads large packages")
	}
	cfg := &packages.Config{
		Dir: "../..",
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Env: os.Environ(),
	}
	// Only the roots are extracted; their dependencies just provide types.
	roots, err := packages.Load(cfg, "./internal/...", "net/http", "go/types", "encoding/json", "golang.org/x/tools/go/packages")
	if err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("../..")
	for _, p := range roots {
		t.Run(p.PkgPath, func(t *testing.T) {
			if len(p.Errors) > 0 {
				t.Skipf("does not load cleanly: %v", p.Errors[0])
			}
			facts, err := extract.Extract(extract.Input{
				Fset: p.Fset, Files: p.Syntax, Pkg: p.Types, Info: p.TypesInfo,
				FilePath: func(f string) string { r, _ := filepath.Rel(abs, f); return r },
			})
			if err != nil {
				t.Fatal(err)
			}
			checkInvariants(t, facts)
			t.Logf("%d symbols, %d occurrences, %d edges, %d externals",
				len(facts.Symbols), len(facts.Occurrences), len(facts.Edges), len(facts.Externals))
			var _ model.Kind
		})
	}
}
