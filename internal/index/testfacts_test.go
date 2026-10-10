package index_test

import (
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"

	"golang.org/x/tools/go/packages"

	"codeg/internal/extract"
	"codeg/internal/model"
)

// realFacts extracts real packages once: this repository's own packages and
// a few large standard-library ones, so tests and benchmarks see realistic
// key, string and occurrence distributions.
var realFacts = sync.OnceValues(func() ([]*model.PackageFacts, error) {
	cfg := &packages.Config{
		Dir: "../..",
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
			packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
			packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule,
		Env: os.Environ(),
	}
	pkgs, err := packages.Load(cfg, "./internal/...", "net/http", "go/types", "encoding/json", "regexp", "strings")
	if err != nil {
		return nil, err
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].PkgPath < pkgs[j].PkgPath })
	abs, _ := filepath.Abs("../..")
	var out []*model.PackageFacts
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			continue
		}
		f, err := extract.Extract(extract.Input{
			Fset: p.Fset, Files: p.Syntax, Pkg: p.Types, Info: p.TypesInfo,
			FilePath: func(f string) string { r, _ := filepath.Rel(abs, f); return r },
		})
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
})

func mustRealFacts(tb testing.TB) []*model.PackageFacts {
	tb.Helper()
	facts, err := realFacts()
	if err != nil {
		tb.Fatal(err)
	}
	if len(facts) == 0 {
		tb.Fatal("no facts extracted")
	}
	return facts
}

// handFacts covers shapes real code rarely produces: multiple files, an
// out-of-order occurrence list, every flag, unknown calls, unicode.
func handFacts() *model.PackageFacts {
	k := func(pkg, name string) model.Key { return model.Key{Pkg: pkg, Name: name} }
	pkg := k("example.com/héllo", "")
	fn := k("example.com/héllo", "Fn")
	return &model.PackageFacts{
		Path: "example.com/héllo", Name: "héllo", HasErrors: true,
		Diagnostics: []model.Diagnostic{{File: "a.go", Line: 3, Col: 9, Msg: "undefined: 名前"}},
		Files:       []model.FileFact{{Path: "a.go"}, {Path: "b_test.go", Test: true}, {Path: "c.go", Generated: true}},
		Imports:     []string{"fmt", "os"},
		Symbols: []model.Symbol{
			{Key: pkg, Kind: model.KindPackage, Name: "héllo", Exported: true},
			{Key: k("example.com/héllo", "@a.go"), Kind: model.KindFile, Name: "a.go", Pos: model.Range{File: 1}},
			{Key: fn, Kind: model.KindFunc, Name: "Fn", Detail: "func()", Exported: true,
				Pos:  model.Range{File: 0, Start: 10, End: 12, Line: 2, Col: 6, EndLine: 2, EndCol: 8},
				Decl: model.Range{File: 0, Start: 5, End: 900, Line: 2, Col: 1, EndLine: 40, EndCol: 2}},
		},
		Occurrences: []model.Occurrence{
			{Range: model.Range{File: 0, Start: 10, End: 12, Line: 2, Col: 6, EndLine: 2, EndCol: 8}, Symbol: fn, Role: model.RoleDefinition, Enclosing: pkg},
			{Range: model.Range{File: 0, Start: 30, End: 33, Line: 3, Col: 2, EndLine: 3, EndCol: 5}, Symbol: k("fmt", "Println"), Role: model.RoleCall,
				Dispatch: model.DispatchStatic, Flags: model.FlagInLoop | model.FlagInBranch | model.FlagInClosure | model.FlagInDefer | model.FlagInGo | model.FlagDefer | model.FlagGo | model.FlagCaptured | model.FlagInitKey,
				Enclosing: fn, Call: 1, ArgOf: 2, ArgIndex: 3, FlowTo: k("example.com/héllo", "Fn::x@0"), TypeArgs: "int, string"},
			{Range: model.Range{File: 0, Start: 20, End: 24, Line: 3, Col: 1, EndLine: 3, EndCol: 5}, Symbol: model.Key{}, Role: model.RoleCall, Dispatch: model.DispatchDynamic, Enclosing: fn, Call: 2},
			{Range: model.Range{File: 2, Start: 0, End: 1, Line: 1, Col: 1, EndLine: 1, EndCol: 2}, Symbol: fn, Role: model.RoleRead, Enclosing: fn},
			{Range: model.Range{File: 1, Start: 7, End: 9, Line: 1, Col: 8, EndLine: 1, EndCol: 10}, Symbol: fn, Role: model.RoleValueRef, Enclosing: fn},
		},
		Edges:      []model.Edge{{Kind: model.EdgeContains, From: pkg, To: fn}},
		MethodSets: []model.MethodSet{{Type: k("example.com/héllo", "T"), Interface: true, Methods: []model.MethodEntry{{Name: "M", Sig: "func()", PtrRecv: true, Promoted: true}, {Name: "N", Sig: "func(int)", Promoted: true}}}},
		Externals:  []model.External{{Key: k("fmt", "Println"), Kind: model.KindFunc, Name: "Println", Detail: "func(a ...any)", Module: "std", Version: "v1"}},
	}
}
