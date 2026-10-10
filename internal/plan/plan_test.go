package plan_test

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"reflect"
	"testing"

	"codeg/internal/plan"
)

func check(t *testing.T, src string) *types.Package {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("example.com/p", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

func api(t *testing.T, src string) string {
	t.Helper()
	return plan.APIHash(check(t, src), func(path string) string { return "dep:" + path })
}

const base = `package p

import "io"

type Config struct {
	Name string
	opts []string
}

func (c *Config) Set(name string) { c.Name = name }
func (c Config) hidden() int       { return len(c.opts) }

type Reader interface{ Read(p []byte) (int, error) }

const Version = 1

var Default = Config{Name: "x"}

func New() *Config { return &Config{} }

func Copy(dst io.Writer, src io.Reader) error { return nil }

func unexported() {}
`

func TestAPIHashIgnoresWhatImportersCannotSee(t *testing.T) {
	want := api(t, base)
	same := map[string]string{
		"function body":          `func New() *Config { return &Config{Name: "other"} }`,
		"comment and line shift": "// New builds a Config.\n\n\nfunc New() *Config { return &Config{} }",
		"constant value":         "const Version = 2",
		"unexported function":    "func unexported() { println() }",
		"variable initializer":   `var Default = Config{Name: "y"}`,
	}
	old := map[string]string{
		"function body":          `func New() *Config { return &Config{} }`,
		"comment and line shift": `func New() *Config { return &Config{} }`,
		"constant value":         "const Version = 1",
		"unexported function":    "func unexported() {}",
		"variable initializer":   `var Default = Config{Name: "x"}`,
	}
	for name, repl := range same {
		src := replace(t, base, old[name], repl)
		if got := api(t, src); got != want {
			t.Errorf("%s changed the API hash", name)
		}
	}
}

func TestAPIHashSeesWhatImportersCanSee(t *testing.T) {
	want := api(t, base)
	changes := map[string][2]string{
		"new exported function":     {"func unexported() {}", "func unexported() {}\nfunc Extra() {}"},
		"changed signature":         {"func New() *Config {", "func New(n int) *Config {"},
		"changed result type":       {"func New() *Config { return &Config{} }", "func New() Config { return Config{} }"},
		"new exported field":        {"Name string", "Name string\n\tAge int"},
		"new unexported field":      {"opts []string", "opts []string\n\textra int"}, // embedding and anonymous-struct keys depend on layout
		"changed field type":        {"Name string", "Name any"},
		"new unexported method":     {"func (c Config) hidden() int", "func (c Config) another() int { return 0 }\nfunc (c Config) hidden() int"},
		"pointer receiver flip":     {"func (c *Config) Set(", "func (c Config) Set("},
		"struct tag":                {"Name string", "Name string `json:\"n\"`"},
		"interface method added":    {"Read(p []byte) (int, error) }", "Read(p []byte) (int, error); Close() error }"},
		"constant type":             {"const Version = 1", "const Version int8 = 1"},
		"variable type":             {"var Default = Config{Name: \"x\"}", "var Default = &Config{Name: \"x\"}"},
		"exported becomes alias":    {"type Reader interface{ Read(p []byte) (int, error) }", "type Reader = io.Reader"},
		"removed exported function": {"func Copy(dst io.Writer, src io.Reader) error { return nil }", "var _ io.Reader"},
	}
	for name, c := range changes {
		if got := api(t, replace(t, base, c[0], c[1])); got == want {
			t.Errorf("%s did not change the API hash", name)
		}
	}
}

// An unexported type that an exported function returns is part of the API:
// importers can call its exported methods.
func TestAPIHashCoversReachableUnexportedTypes(t *testing.T) {
	src := func(method string) string {
		return "package p\n\ntype hidden struct{}\n\n" + method + "\n\nfunc Make() hidden { return hidden{} }\n"
	}
	a := api(t, src("func (hidden) A() {}"))
	b := api(t, src("func (hidden) B() {}"))
	if a == b {
		t.Error("method on a reachable unexported type is invisible to the API hash")
	}
}

func TestAPIHashFoldsInExposedDependencies(t *testing.T) {
	// The exposed package is the standard library's io: a value of io.Reader
	// appears in Copy's signature.
	pkg := check(t, base)
	h1 := plan.APIHash(pkg, func(string) string { return "v1" })
	h2 := plan.APIHash(pkg, func(string) string { return "v2" })
	if h1 == h2 {
		t.Error("a change to an exposed dependency's API did not change this package's API hash")
	}

	// A package that exposes no foreign types ignores depAPI entirely.
	solo := check(t, "package p\n\nfunc F() int { return 1 }\n")
	if plan.APIHash(solo, func(string) string { return "v1" }) != plan.APIHash(solo, func(string) string { return "v2" }) {
		t.Error("API hash depends on a package that is not exposed")
	}
}

func TestAPIHashIsDeterministic(t *testing.T) {
	first := api(t, base)
	for range 5 {
		if got := api(t, base); got != first {
			t.Fatal("API hash varies between runs")
		}
	}
}

func replace(t *testing.T, src, old, new string) string {
	t.Helper()
	i := indexOf(src, old)
	if i < 0 {
		t.Fatalf("fixture text %q not found", old)
	}
	return src[:i] + new + src[i+len(old):]
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func names(waves [][]plan.Group) [][][]string {
	if len(waves) == 0 {
		return nil
	}
	out := make([][][]string, len(waves))
	for i, w := range waves {
		for _, g := range w {
			out[i] = append(out[i], g.Members)
		}
	}
	return out
}

func TestWaves(t *testing.T) {
	tests := []struct {
		name string
		in   map[string][]string
		want [][][]string
	}{
		{"empty", nil, nil},
		{"independent", map[string][]string{"b": nil, "a": nil}, [][][]string{{{"a"}, {"b"}}}},
		{"chain", map[string][]string{"c": {"b"}, "b": {"a"}, "a": nil},
			[][][]string{{{"a"}}, {{"b"}}, {{"c"}}}},
		{"diamond", map[string][]string{"top": {"l", "r"}, "l": {"base"}, "r": {"base"}, "base": nil},
			[][][]string{{{"base"}}, {{"l"}, {"r"}}, {{"top"}}}},
		{"imports outside the project are ignored", map[string][]string{"a": {"fmt", "github.com/x/y"}},
			[][][]string{{{"a"}}}},
		{"cycle is one group", map[string][]string{"a": {"b"}, "b": {"a"}, "c": {"a"}},
			[][][]string{{{"a", "b"}}, {{"c"}}}},
		{"deepest dependency decides the wave", map[string][]string{"x": {"a", "c"}, "c": {"b"}, "b": {"a"}, "a": nil},
			[][][]string{{{"a"}}, {{"b"}}, {{"c"}}, {{"x"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := names(plan.Waves(tt.in)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
