package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"codeg/internal/engine"
)

// TestAgainstScipGo indexes a small module with both codeg and scip-go and
// requires that they agree, apart from the documented differences. It runs
// when SCIP_GO names a scip-go binary:
//
//	go install github.com/scip-code/scip-go/cmd/scip-go@latest
//	SCIP_GO=$(go env GOPATH)/bin/scip-go go test ./cmd/scipcheck
func TestAgainstScipGo(t *testing.T) {
	bin := os.Getenv("SCIP_GO")
	if bin == "" {
		t.Skip("SCIP_GO is not set")
	}
	t.Setenv("GOPROXY", "off")
	root := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/x\n\ngo 1.24\n",
		"a/a.go": `package a

import (
	"fmt"
	"strings"
)

type Shape interface{ Area() float64 }

type Base struct{ ID int }

func (b *Base) SetID(id int) { b.ID = id }

type Circle struct {
	Base
	R    float64
	Meta struct{ Tag string }
}

func (c Circle) Area() float64 { return c.R * c.R }

type Box[T any] struct{ Val T }

func (b *Box[T]) Get() T { return b.Val }

func Map[A, B any](xs []A, f func(A) B) []B {
	var out []B
	for _, x := range xs {
		out = append(out, f(x))
	}
	return out
}

var counter int

func init() { counter = 1 }
func init() { counter = 2 }

func Describe(s Shape, v any) string {
	c := Circle{R: 2}
	c.SetID(3)
	c.Meta.Tag = "t"
	counter++
	switch x := v.(type) {
	case int:
		return fmt.Sprint(x)
	case string:
		return strings.ToUpper(x)
	}
	f := func(n int) int { return n + counter }
	_ = f(1)
outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 2 {
				continue outer
			}
			if i == 2 {
				goto done
			}
		}
	}
done:
	b := &Box[int]{Val: 1}
	_ = b.Get()
	_ = Map([]int{1}, func(i int) string { return fmt.Sprint(i) })
	return fmt.Sprintf("%v", s.Area())
}
`,
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc TestDescribe(t *testing.T) { _ = Describe(Circle{}, 1) }\n",
	} {
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
	out := filepath.Join(root, "index.scip")
	cmd := exec.Command(bin, "index", "--skip-implementations", "-q", "-o", out)
	cmd.Dir = root
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scip-go: %v\n%s", err, b)
	}

	theirs, err := loadSCIP(out)
	if err != nil {
		t.Fatal(err)
	}
	mine, err := loadMine(store)
	if err != nil {
		t.Fatal(err)
	}
	r := compare(theirs, mine)
	if n := r.differences(); n != 0 {
		r.print(os.Stderr, 20, true)
		t.Fatalf("%d unexpected difference(s) between codeg and scip-go", n)
	}
	if r.sharedPositions < 100 {
		t.Errorf("only %d positions in common; the comparison is not exercising much", r.sharedPositions)
	}
}
