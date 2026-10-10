package model_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"codeg/internal/model"
)

func TestKeyString(t *testing.T) {
	tests := []struct {
		key  model.Key
		want string
	}{
		{model.Key{Pkg: "gopkg.in/yaml.v3", Name: "Decoder.Decode"}, "gopkg.in/yaml.v3.Decoder.Decode"},
		{model.Key{Name: "error.Error"}, "error.Error"},
		{model.Key{Pkg: "fmt"}, "fmt"},
	}
	for _, tt := range tests {
		if got := tt.key.String(); got != tt.want {
			t.Errorf("%#v.String() = %q, want %q", tt.key, got, tt.want)
		}
	}
	// The owner is a field, so a dotted last path element is not ambiguous.
	if k := (model.Key{Pkg: "gopkg.in/yaml.v3", Name: "Node"}); k.Pkg != "gopkg.in/yaml.v3" {
		t.Errorf("owner = %q", k.Pkg)
	}
}

func TestUniverseAndPackageNames(t *testing.T) {
	const src = `package p

import str "strings"

func f(e error, s []int) int {
	_ = e.Error()
	_ = str.ToUpper
	var p *int = nil
	_ = p
	return len(s) + int(1) + cap(s)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	info := &types.Info{
		Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{},
		Types: map[ast.Expr]types.TypeAndValue{}, Implicits: map[ast.Node]types.Object{},
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("example.com/p", fset, []*ast.File{f}, info)
	if err != nil {
		t.Fatal(err)
	}
	n := model.NewNamer(fset, []*ast.File{f}, pkg, info)

	want := map[string]model.Ref{
		"Error":   {Key: model.Key{Name: "error.Error"}, Kind: model.KindMethod},
		"str":     {Key: model.Key{Pkg: "strings"}, Kind: model.KindPackage},
		"ToUpper": {Key: model.Key{Pkg: "strings", Name: "ToUpper"}, Kind: model.KindFunc},
		"nil":     {Key: model.Key{Name: "nil"}, Kind: model.KindVar},
		"len":     {Key: model.Key{Name: "len"}, Kind: model.KindFunc},
		"cap":     {Key: model.Key{Name: "cap"}, Kind: model.KindFunc},
		"int":     {Key: model.Key{Name: "int"}, Kind: model.KindType},
		"error":   {Key: model.Key{Name: "error"}, Kind: model.KindType},
	}
	seen := map[string]bool{}
	for id, obj := range info.Uses {
		w, ok := want[id.Name]
		if !ok {
			continue
		}
		got, gok := n.Ref(obj)
		if !gok || got != w {
			t.Errorf("%s: got %v (ok=%v), want %v", id.Name, got, gok, w)
		}
		seen[id.Name] = true
	}
	for name := range want {
		if !seen[name] {
			t.Errorf("%s never appeared in Uses", name)
		}
	}
	if !model.Key(want["int"].Key).IsUniverse() || want["ToUpper"].Key.IsUniverse() {
		t.Error("IsUniverse misclassifies keys")
	}
}
