package query_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"codeg/internal/query"
)

func nodeIDs(g *query.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func findNode(g *query.Graph, id string) *query.GraphNode {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return &g.Nodes[i]
		}
	}
	return nil
}

func findEdge(g *query.Graph, src, dst, kind string) *query.GraphEdge {
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.Source == src && e.Target == dst && e.Kind == kind {
			return e
		}
	}
	return nil
}

func TestPackagesGraph(t *testing.T) {
	ix := open(t)
	g := ix.PackagesGraph(query.PackagesOptions{})
	want := []string{"example.com/q/store", "example.com/q/svc", "example.com/q/use", "example.com/q/wrap"}
	if got := nodeIDs(g); !slices.Equal(got, want) {
		t.Fatalf("nodes = %v, want %v", got, want)
	}
	for _, e := range [][2]string{{"svc", "store"}, {"wrap", "store"}, {"use", "wrap"}} {
		if findEdge(g, "example.com/q/"+e[0], "example.com/q/"+e[1], "import") == nil {
			t.Errorf("missing import edge %s -> %s", e[0], e[1])
		}
	}
	if findEdge(g, "example.com/q/use", "example.com/q/store", "import") != nil {
		t.Error("use never imports store, yet has an edge to it")
	}
	if n := findNode(g, "example.com/q/svc"); n == nil || n.Label != "svc" || n.Count != 1 {
		t.Errorf("svc node = %+v", n)
	}
	// Dependencies are collapsed by default and appear per module on request.
	for _, n := range g.Nodes {
		if n.External {
			t.Errorf("external node %s in a collapsed graph", n.ID)
		}
	}
	g = ix.PackagesGraph(query.PackagesOptions{Externals: true})
	if n := findNode(g, "ext:std"); n == nil || !n.External || n.Group != "external" {
		t.Errorf("std module node = %+v", n)
	}
	if e := findEdge(g, "example.com/q/svc", "ext:std", "import"); e == nil || e.Count != 1 {
		t.Errorf("svc -> std edge = %+v (svc imports fmt only)", e)
	}
}

func TestPackageGraph(t *testing.T) {
	ix := open(t)
	g, err := ix.PackageGraph("example.com/q/svc", query.GraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const svc = "example.com/q/svc."
	for _, n := range []string{"CreateUser", "foo", "bar", "helper", "Run", "dynamic"} {
		if findNode(g, svc+n) == nil {
			t.Errorf("no node for %s", n)
		}
	}
	// Locals, parameters and closures are internal to a function.
	for _, n := range g.Nodes {
		if n.Group == "example.com/q/svc" && strings.ContainsAny(n.Label, "@$#") {
			t.Errorf("function-internal node in the package view: %s", n.ID)
		}
	}
	if e := findEdge(g, svc+"foo", svc+"bar", "call"); e == nil || e.Count != 1 || e.Certainty != query.Certain {
		t.Errorf("foo -> bar = %+v", e)
	}
	if findEdge(g, svc+"Run", svc+"foo", "call") == nil {
		t.Error("Run -> foo missing")
	}
	// Calls made inside a closure belong to the function that contains it.
	if e := findEdge(g, svc+"dynamic", svc+"bar", "call"); e == nil {
		t.Error("the call to bar inside dynamic's closure was lost")
	}
	// Uses of another package appear as imported nodes, grouped by package.
	save := "example.com/q/store.Store.Save"
	if n := findNode(g, save); n == nil || n.Group != "example.com/q/store" || n.Kind != "method" {
		t.Errorf("imported node = %+v", n)
	}
	if e := findEdge(g, svc+"CreateUser", save, "call"); e == nil || e.Dispatch != "interface" || e.Certainty != query.Candidate {
		t.Errorf("CreateUser -> Store.Save = %+v", e)
	}
	if e := findEdge(g, svc+"CreateUser", "example.com/q/store.User", "construct"); e == nil {
		t.Error("construct edge to store.User missing")
	}
	if e := findEdge(g, svc+"bar", "example.com/q/store.Counter", "write"); e == nil {
		t.Error("bar writes store.Counter (+=)")
	}
	// Only what is used is imported: store has NewMem, Base, ... but svc uses few.
	if findNode(g, "example.com/q/store.fileStore") != nil {
		t.Error("an unused symbol of another package is in the slice")
	}
	// A field of another package's type is shown as the type, not as a node
	// of its own: svc reads store.User.Name only through the composite literal.
	for _, n := range g.Nodes {
		if n.Group != "example.com/q/svc" && n.Kind == "field" {
			t.Errorf("imported field %s has its own node; want it folded into its type", n.ID)
		}
	}
	if findNode(g, "fmt.Sprint") == nil {
		t.Error("used standard library function missing")
	}
}

func TestPackageGraphMethods(t *testing.T) {
	ix := open(t)
	folded, _ := ix.PackageGraph("example.com/q/store", query.GraphOptions{})
	if findNode(folded, "example.com/q/store.memStore.Save") != nil {
		t.Error("methods are nodes although they should be folded into their type")
	}
	full, err := ix.PackageGraph("example.com/q/store", query.GraphOptions{Methods: true})
	if err != nil {
		t.Fatal(err)
	}
	m := findNode(full, "example.com/q/store.memStore.Save")
	if m == nil || m.Parent != "example.com/q/store.memStore" {
		t.Errorf("method node = %+v", m)
	}
	if findEdge(full, "example.com/q/store.memStore", "example.com/q/store.Base", "embeds") == nil {
		t.Error("memStore embeds Base")
	}
	if n := findNode(full, "example.com/q/store.memStore"); n == nil || n.Count < 3 {
		t.Errorf("memStore member count = %+v", n)
	}
}

func TestPackageGraphCapsLargePackages(t *testing.T) {
	ix := open(t)
	g, err := ix.PackageGraph("example.com/q/svc", query.GraphOptions{Max: 4})
	if err != nil {
		t.Fatal(err)
	}
	if !g.Truncated || len(g.Nodes) > 4+8 {
		t.Errorf("truncated=%v nodes=%d", g.Truncated, len(g.Nodes))
	}
	ids := map[string]bool{}
	for _, n := range g.Nodes {
		ids[n.ID] = true
	}
	for _, e := range g.Edges {
		if !ids[e.Source] || !ids[e.Target] {
			t.Errorf("edge %s -> %s points outside the truncated slice", e.Source, e.Target)
		}
	}
	if _, err := ix.PackageGraph("example.com/q/nope", query.GraphOptions{}); !errors.Is(err, query.ErrNotFound) {
		t.Errorf("unknown package: %v", err)
	}
}

func TestFunctionGraph(t *testing.T) {
	ix := open(t)
	fn := key(t, ix, "svc.CreateUser")
	g, err := ix.FunctionGraph(fn, query.GraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	const f = "example.com/q/svc.CreateUser"
	kinds := map[string][]string{}
	for _, n := range g.Nodes {
		kinds[n.Kind] = append(kinds[n.Kind], n.ID)
	}
	if !slices.Contains(kinds["function"], f) || len(kinds["param"]) != 2 || len(kinds["result"]) != 2 {
		t.Errorf("node kinds: %v", kinds)
	}
	if !slices.Contains(kinds["local"], f+"::u@0") || !slices.Contains(kinds["local"], f+"::err@0") {
		t.Errorf("locals: %v", kinds["local"])
	}
	if n := findNode(g, f+"::u@0"); n == nil || n.Detail != "*example.com/q/store.User" {
		t.Errorf("local u = %+v", n)
	}
	if findEdge(g, f, f+"#param0", "has_param") == nil || findEdge(g, f, f+"::u@0", "declares") == nil {
		t.Error("signature and local edges missing")
	}

	// The interface call, with candidates shown as such.
	save := "example.com/q/store.Store.Save"
	call := findEdge(g, f, save, "call")
	if call == nil || call.Dispatch != "interface" || call.Certainty != query.Candidate || len(call.Orders) != 1 {
		t.Fatalf("call edge = %+v", call)
	}
	if e := findEdge(g, save, "example.com/q/store.memStore.Save", "may_call"); e == nil || e.Certainty != query.Candidate {
		t.Errorf("candidate edge = %+v", e)
	}
	if findEdge(g, f, "example.com/q/store.User", "construct") == nil {
		t.Error("construct edge missing")
	}

	// Edge numbers follow source order: construct (line 9) precedes the call
	// (line 10), and a flow edge from u carries the number of its use.
	construct := findEdge(g, f, "example.com/q/store.User", "construct")
	if construct.Orders[0] >= call.Orders[0] {
		t.Errorf("construct #%d should come before the call #%d", construct.Orders[0], call.Orders[0])
	}
	if !slices.ContainsFunc(g.Notes, func(s string) bool { return strings.Contains(s, "source order") }) {
		t.Errorf("notes = %v", g.Notes)
	}
	// Flow: u flows into the result.
	if e := findEdge(g, f+"::u@0", f+"#result0", "flow"); e == nil {
		t.Errorf("flow edge from u to the result missing; edges: %d", len(g.Edges))
	}
}

func TestFunctionGraphUnknownsAndClosures(t *testing.T) {
	ix := open(t)
	g, err := ix.FunctionGraph(key(t, ix, "svc.dynamic"), query.GraphOptions{})
	if err != nil {
		t.Fatal(err)
	}
	unknown := 0
	for _, n := range g.Nodes {
		if n.Kind == "unknown" {
			unknown++
		}
	}
	if unknown != 2 {
		t.Errorf("unknown-call nodes = %d, want 2 (fns[0]() and get()())", unknown)
	}
	const d = "example.com/q/svc.dynamic"
	if n := findNode(g, d+"$0"); n == nil || n.Kind != "closure" || n.Parent != d {
		t.Errorf("closure node = %+v", n)
	}
	if findEdge(g, d, d+"$0", "contains") == nil {
		t.Error("contains edge to the closure missing")
	}
	// The closure, not the outer function, calls bar.
	if findEdge(g, d+"$0", "example.com/q/svc.bar", "call") == nil {
		t.Error("closure -> bar missing")
	}
	if _, err := ix.FunctionGraph(key(t, ix, "store.User"), query.GraphOptions{}); err == nil {
		t.Error("a type was accepted as a function")
	}
}

func TestSourceOnlyServesIndexedFiles(t *testing.T) {
	ix := open(t)
	v, err := ix.Source(query.SourceRequest{File: "svc/svc.go", StartLine: 17, EndLine: 19, Context: 1})
	if err != nil {
		t.Fatal(err)
	}
	if v.Stale || v.StartLine != 16 || v.EndLine != 20 || len(v.Lines) != 5 || !v.Lines[1].Highlight || v.Lines[0].Highlight {
		t.Errorf("view = %+v", v)
	}
	if !strings.Contains(v.Lines[2].Text, "b := a") {
		t.Errorf("line 18 = %q", v.Lines[2].Text)
	}
	for _, bad := range []string{"../go.mod", "/etc/passwd", "go.mod", "svc/../../x", "svc\\..\\..\\go.mod", "svc/nothere.go", ""} {
		if _, err := ix.Source(query.SourceRequest{File: bad, StartLine: 1}); !errors.Is(err, query.ErrNotFound) {
			t.Errorf("Source(%q): %v, want ErrNotFound", bad, err)
		}
	}
}

func TestDetail(t *testing.T) {
	ix := open(t)

	d, err := ix.Detail(key(t, ix, "svc.bar"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Callers == nil || *d.Callers != 2 || d.Callees == nil {
		t.Errorf("bar callers/callees = %v/%v", d.Callers, d.Callees)
	}
	if d.Source == nil || !slices.ContainsFunc(d.Source.Lines, func(l query.SourceLine) bool {
		return l.Highlight && strings.Contains(l.Text, "func bar")
	}) {
		t.Errorf("definition snippet = %+v", d.Source)
	}
	if d.Stats["definition"] != 1 || d.Stats["call"] != 2 {
		t.Errorf("stats = %v", d.Stats)
	}
	kinds := map[string]int{}
	for _, r := range d.Relations {
		kinds[r.Kind]++
	}
	if kinds["has_param"] != 2 {
		t.Errorf("relations = %v", kinds)
	}

	// A type: its members and its implementers.
	d, err = ix.Detail(key(t, ix, "store.Store"))
	if err != nil || d.Implements == nil || len(d.Implements.Implementers) != 2 {
		t.Errorf("Store detail: %+v, %v", d, err)
	}
	// A variable: its events in source order, with the limits stated.
	d, err = ix.Detail(key(t, ix, "foo::b"))
	if err != nil || len(d.Events) != 3 || len(d.Limits) == 0 {
		t.Errorf("b detail: events=%d limits=%v err=%v", len(d.Events), d.Limits, err)
	}
	if _, err := ix.Detail(key(t, ix, "svc.foo")); err != nil {
		t.Error(err)
	}
}
