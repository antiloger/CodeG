package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeg/internal/engine"
	"codeg/internal/query"
)

var files = map[string]string{
	"go.mod": "module example.com/c\n\ngo 1.24\n",
	"a/a.go": `package a

type Speaker interface{ Speak() string }

type Dog struct{}

func (*Dog) Speak() string { return "woof" }

var Volume int

func Talk(s Speaker) string {
	Volume++
	return s.Speak()
}

func Main() string {
	d := &Dog{}
	return Talk(d)
}

func Speak() {}
`,
}

func openFixture(t *testing.T) *query.Index {
	t.Helper()
	root := t.TempDir()
	for name, content := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	store := filepath.Join(root, ".codeg")
	if _, err := engine.Index(context.Background(), engine.Options{Root: root, StoreDir: store}); err != nil {
		t.Fatal(err)
	}
	ix, err := query.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func run(t *testing.T, ix *query.Index, kind string, args ...string) (string, error) {
	t.Helper()
	var b bytes.Buffer
	err := dispatch(ix, kind, args, queryFlags{depth: 15}, &b)
	return b.String(), err
}

func TestQueryText(t *testing.T) {
	ix := openFixture(t)
	tests := []struct {
		kind string
		args []string
		want []string
	}{
		{"def", []string{"Dog"}, []string{"type  example.com/c/a.Dog", "defined at a/a.go:5:6", "type Dog struct{}"}},
		{"refs", []string{"Volume"}, []string{"1 reference(s)", "a/a.go:12:2", "Volume++"}},
		{"writes", []string{"Volume"}, []string{"read_write", "address_taken = &x"}},
		{"reads", []string{"Volume"}, []string{"1 reads of"}},
		{"callers", []string{"Dog.Speak"}, []string{"candidate", "found through interface method a.Speaker.Speak", "Talk"}},
		{"callees", []string{"Talk"}, []string{"[interface, candidate]", "possible concrete targets (candidates, not certain)", "a.Dog.Speak"}},
		{"types", []string{"Main"}, []string{"example.com/c/a.Dog", "1 construction(s)"}},
		{"implements", []string{"Speaker"}, []string{"example.com/c/a.Dog", "only *T implements it", "note: generic types"}},
		{"implements", []string{"Dog"}, []string{"implements 1 interface(s)", "a.Speaker"}},
		{"chain", []string{"Main", "Talk"}, []string{"1 call(s), certain calls only", "a.Main -> a.Talk"}},
		{"chain", []string{"Main", "Dog.Speak"}, []string{"includes candidate steps", "candidate"}},
		{"events", []string{"Main::d"}, []string{"definition", "argument 0 of a.Talk", "source order, not execution order"}},
	}
	for _, tt := range tests {
		t.Run(tt.kind+" "+strings.Join(tt.args, " "), func(t *testing.T) {
			out, err := run(t, ix, tt.kind, tt.args...)
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tt.want {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
		})
	}
}

func TestQueryJSON(t *testing.T) {
	ix := openFixture(t)
	for _, c := range []struct {
		kind string
		args []string
	}{
		{"def", []string{"Dog"}}, {"refs", []string{"Volume"}}, {"reads", []string{"Volume"}}, {"writes", []string{"Volume"}},
		{"callers", []string{"Talk"}}, {"callees", []string{"Talk"}}, {"types", []string{"Main"}},
		{"implements", []string{"Speaker"}}, {"chain", []string{"Main", "Talk"}}, {"events", []string{"Main::d"}},
	} {
		var b bytes.Buffer
		if err := dispatch(ix, c.kind, c.args, queryFlags{json: true, depth: 15}, &b); err != nil {
			t.Errorf("%s: %v", c.kind, err)
			continue
		}
		var v any
		if err := json.Unmarshal(b.Bytes(), &v); err != nil {
			t.Errorf("%s: output is not JSON: %v\n%s", c.kind, err, b.String())
		}
	}
}

// def shows every match of an ambiguous name; the other kinds need exactly one.
func TestDefListsAllMatches(t *testing.T) {
	ix := openFixture(t)
	out, err := run(t, ix, "def", "Speak")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"example.com/c/a.Speak", "example.com/c/a.Dog.Speak", "example.com/c/a.Speaker.Speak"} {
		if !strings.Contains(out, want) {
			t.Errorf("def Speak lacks %s:\n%s", want, out)
		}
	}
}

func TestQueryErrors(t *testing.T) {
	ix := openFixture(t)
	for _, tt := range []struct {
		kind string
		args []string
		want string
	}{
		{"refs", []string{"Speak"}, "ambiguous"}, // Speaker.Speak, Dog.Speak, a.Speak
		{"def", []string{"Nope"}, "no such symbol"},
		{"refs", nil, "takes 1 symbol"},
		{"chain", []string{"Main"}, "takes 2 symbol"},
		{"frobnicate", []string{"x"}, "unknown query"},
		{"implements", []string{"Talk"}, "not a non-generic"},
	} {
		_, err := run(t, ix, tt.kind, tt.args...)
		if err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s %v: error %v, want one containing %q", tt.kind, tt.args, err, tt.want)
		}
	}
}
