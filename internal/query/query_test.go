package query_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"codeg/internal/engine"
	"codeg/internal/model"
	"codeg/internal/query"
)

// The fixture module. The packages are arranged so that each query has a case
// that is easy to get wrong:
//
//	store   Store interface, memStore/fileStore implementations (one by embedding),
//	        Reader with named results implemented by a method with unnamed ones
//	wrap    W embeds store.Base
//	use     imports wrap only, yet calls store.Base's promoted method
//	svc     handlers: static, interface, dynamic and unknown calls; closures; the
//	        CreateUser and foo examples
var fixtureFiles = map[string]string{
	"go.mod": "module example.com/q\n\ngo 1.24\n",
	"store/store.go": `package store

type User struct {
	Name string
	Age  int
}

type Store interface {
	Save(u *User) error
	Load(id int) (*User, error)
}

type Base struct{ hits int }

func (b *Base) Touch() { b.hits++ }

type memStore struct {
	Base
	users []*User
}

func NewMem() *memStore { return &memStore{} }

func (s *memStore) Save(u *User) error        { s.users = append(s.users, u); return nil }
func (s *memStore) Load(id int) (*User, error) { return s.users[id], nil }

type fileStore struct{ *memStore }

// Reader has named results; buf's Read has unnamed ones. They are the same type.
type Reader interface {
	Read(p []byte) (n int, err error)
}

type buf struct{}

func (buf) Read(b []byte) (int, error) { return 0, nil }

type Empty interface{}

var Counter int
`,
	"wrap/wrap.go": `package wrap

import "example.com/q/store"

type W struct{ store.Base }
`,
	"use/use.go": `package use

import "example.com/q/wrap"

func Poke(w *wrap.W) { w.Touch() }
`,
	"svc/svc.go": `package svc

import (
	"fmt"

	"example.com/q/store"
)

func CreateUser(st store.Store, name string) (*store.User, error) {
	u := &store.User{Name: name}
	if err := st.Save(u); err != nil {
		return nil, err
	}
	return u, nil
}

func foo(a int) int {
	b := a
	bar(b, a)
	return b
}

func bar(x, y int) { store.Counter += x + y }

func helper() string { return fmt.Sprint("x") }

func Run() {
	st := store.NewMem()
	CreateUser(st, "n")
	foo(1)
	fmt.Println(helper())
}

func dynamic(fn func(int), fns []func(), get func() func()) {
	fn(1)
	fns[0]()
	get()()
	defer func() { bar(1, 2) }()
}

func Reader() {
	var c int
	_ = store.Counter
	store.Counter = 3
	c = store.Counter
	_ = c
}
`,
}

var (
	buildOnce sync.Once
	store     string
	buildErr  error
)

func open(t *testing.T) *query.Index {
	t.Helper()
	buildOnce.Do(func() {
		root, err := os.MkdirTemp("", "query-fixture")
		if err != nil {
			buildErr = err
			return
		}
		for name, content := range fixtureFiles {
			p := filepath.Join(root, filepath.FromSlash(name))
			os.MkdirAll(filepath.Dir(p), 0o755)
			if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
				buildErr = err
				return
			}
		}
		store = filepath.Join(root, ".codeg")
		_, buildErr = engine.Index(context.Background(), engine.Options{Root: root, StoreDir: store})
	})
	if buildErr != nil {
		t.Fatal(buildErr)
	}
	ix, err := query.Open(store)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func key(t *testing.T, ix *query.Index, spec string) model.Key {
	t.Helper()
	s, err := ix.ResolveOne(spec)
	if err != nil {
		t.Fatalf("resolve %q: %v", spec, err)
	}
	return s.ModelKey()
}

func TestResolve(t *testing.T) {
	ix := open(t)
	tests := []struct {
		spec string
		want string // key, or "" for an error
		err  string
	}{
		{"example.com/q/store.User", "example.com/q/store.User", ""},
		{"store.User", "example.com/q/store.User", ""},
		{"q/store.User", "example.com/q/store.User", ""},
		{"User", "example.com/q/store.User", ""},
		{"memStore.Save", "example.com/q/store.memStore.Save", ""},
		{"store.memStore.Save", "example.com/q/store.memStore.Save", ""},
		{"CreateUser", "example.com/q/svc.CreateUser", ""},
		{"svc.foo::b", "example.com/q/svc.foo::b@0", ""},
		{"foo::b@0", "example.com/q/svc.foo::b@0", ""},
		{"svc.dynamic$0", "example.com/q/svc.dynamic$0", ""},
		{"svc/svc.go:18:2", "example.com/q/svc.foo::b@0", ""}, // the `b` in `b := a`
		{"fmt.Sprint", "fmt.Sprint", ""},
		{"Save", "", "ambiguous"},   // Store.Save and memStore.Save
		{"Reader", "", "ambiguous"}, // store.Reader and svc.Reader
		{"NoSuchThing", "", "no such symbol"},
	}
	for _, tt := range tests {
		got, err := ix.ResolveOne(tt.spec)
		if tt.err != "" {
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Errorf("%q: error %v, want one containing %q", tt.spec, err, tt.err)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: %v", tt.spec, err)
			continue
		}
		if got.Key != tt.want {
			t.Errorf("%q resolved to %s, want %s", tt.spec, got.Key, tt.want)
		}
	}

	// The ambiguity error names every candidate.
	_, err := ix.ResolveOne("Save")
	var amb *query.AmbiguousError
	if !errors.As(err, &amb) || len(amb.Candidates) != 2 {
		t.Errorf("Save: want an AmbiguousError with 2 candidates, got %v", err)
	}

	// External symbols carry their stub.
	ext, err := ix.ResolveOne("fmt.Sprint")
	if err != nil || !ext.External || ext.Kind != "func" || ext.Location != nil {
		t.Errorf("fmt.Sprint: %+v, %v", ext, err)
	}
}

func locs(occs []query.Occ) []string {
	var out []string
	for _, o := range occs {
		out = append(out, o.File+":"+itoa(o.Line)+" "+o.Role)
	}
	return out
}

func itoa(n int) string { return string(rune('0'+n/10)) + string(rune('0'+n%10)) }

func TestReferences(t *testing.T) {
	ix := open(t)
	refs, err := ix.References(key(t, ix, "store.User"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]int{}
	for _, r := range refs {
		files[r.File]++
	}
	if files["svc/svc.go"] != 2 || files["store/store.go"] < 3 {
		t.Errorf("references to User by file: %v", files)
	}
	for _, r := range refs {
		if r.Role == "definition" {
			t.Errorf("definition returned without IncludeDefinition: %+v", r)
		}
	}
	withDef, _ := ix.References(key(t, ix, "store.User"), query.Filter{IncludeDefinition: true})
	if len(withDef) != len(refs)+1 {
		t.Errorf("IncludeDefinition added %d occurrences, want 1", len(withDef)-len(refs))
	}
}

// use.Poke calls store.Base.Touch through wrap.W without importing store: only
// the summary's referenced-packages set can find it.
func TestReferencesReachPackagesThatDoNotImport(t *testing.T) {
	ix := open(t)
	touch := key(t, ix, "Base.Touch")
	callers, err := ix.Callers(touch, query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range callers {
		names = append(names, e.Caller.Key)
		if e.Certainty != query.Certain || e.Dispatch != "static" {
			t.Errorf("promoted call should be certain and static: %+v", e)
		}
	}
	if !slices.Contains(names, "example.com/q/use.Poke") {
		t.Errorf("callers of Base.Touch = %v; missing use.Poke (a package that never imports store)", names)
	}
}

func TestCallees(t *testing.T) {
	ix := open(t)

	edges, err := ix.Callees(key(t, ix, "svc.CreateUser"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 {
		t.Fatalf("CreateUser callees: %d edges, want 1 (Store.Save)", len(edges))
	}
	e := edges[0]
	if e.Callee.Key != "example.com/q/store.Store.Save" || e.Dispatch != "interface" || e.Certainty != query.Candidate {
		t.Errorf("edge = %+v", e)
	}
	var cands []string
	for _, c := range e.Candidates {
		cands = append(cands, c.Key)
	}
	slices.Sort(cands)
	// memStore declares Save; fileStore gets it by embedding *memStore and is
	// listed by type, since its method is declared elsewhere.
	want := []string{"example.com/q/store.fileStore", "example.com/q/store.memStore.Save"}
	if !slices.Equal(cands, want) {
		t.Errorf("candidates = %v, want %v", cands, want)
	}

	// dynamic: function values and computed calls are unknown, never guessed.
	dyn, err := ix.Callees(key(t, ix, "svc.dynamic"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var unknown, viaParam, closure int
	for _, e := range dyn {
		switch {
		case e.Callee == nil:
			unknown += len(e.Sites) // fns[0]() and get()() share one edge
			if e.Certainty != query.Unknown {
				t.Errorf("unknown target with certainty %v", e.Certainty)
			}
		case e.Dispatch == "dynamic":
			viaParam++
		case e.Callee.Name == "bar":
			closure++
			if !slices.ContainsFunc(e.Sites, func(o query.Occ) bool { return slices.Contains(o.Flags, "in_closure") }) {
				t.Error("call in the deferred closure lost its in_closure flag")
			}
		}
	}
	// fn(1) and the inner get() call function-typed parameters.
	if unknown != 2 || viaParam != 2 || closure != 1 {
		t.Errorf("dynamic: %d unknown sites, %d via function parameter, %d closure calls (want 2, 2, 1)", unknown, viaParam, closure)
	}
}

func TestCallers(t *testing.T) {
	ix := open(t)

	edges, err := ix.Callers(key(t, ix, "svc.helper"), query.Filter{})
	if err != nil || len(edges) != 1 || edges[0].Caller.Key != "example.com/q/svc.Run" {
		t.Fatalf("callers of helper: %+v, %v", edges, err)
	}

	// bar is called from foo and from a closure inside dynamic.
	edges, err = ix.Callers(key(t, ix, "svc.bar"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	for _, e := range edges {
		callers = append(callers, e.Caller.Key)
	}
	if !slices.Equal(callers, []string{"example.com/q/svc.dynamic$0", "example.com/q/svc.foo"}) {
		t.Errorf("callers of bar = %v", callers)
	}

	// Nobody calls memStore.Save directly; CreateUser calls it through Store.Save.
	// That is reported as a candidate, never as a certain call.
	edges, err = ix.Callers(key(t, ix, "memStore.Save"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(edges) != 1 || edges[0].Certainty != query.Candidate || edges[0].Caller.Key != "example.com/q/svc.CreateUser" ||
		edges[0].Via != "example.com/q/store.Store.Save" {
		t.Errorf("callers of memStore.Save = %+v", edges)
	}
}

func TestAccess(t *testing.T) {
	ix := open(t)
	counter := key(t, ix, "store.Counter")

	writes, err := ix.Access(counter, query.Writes, query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{}
	for _, w := range writes {
		roles[w.Role]++
	}
	if roles["read_write"] != 1 || roles["write"] != 1 || len(writes) != 2 {
		t.Errorf("writes of Counter: %v", locs(writes))
	}
	reads, err := ix.Access(counter, query.Reads, query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	// `store.Counter += ...` is read_write, so it counts as a read too.
	if len(reads) != 3 {
		t.Errorf("reads of Counter: %v", locs(reads))
	}
}

func TestTypesUsed(t *testing.T) {
	ix := open(t)
	uses, err := ix.TypesUsed(key(t, ix, "svc.CreateUser"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][2]int{}
	for _, u := range uses {
		got[u.Type.Key] = [2]int{u.Uses, u.Constructed}
	}
	// User appears in the result type and in the literal; Store in the signature.
	if got["example.com/q/store.User"] != [2]int{2, 1} || got["example.com/q/store.Store"] != [2]int{1, 0} {
		t.Errorf("types used by CreateUser: %v", got)
	}
}

func TestImplements(t *testing.T) {
	ix := open(t)

	impls, err := ix.Implementers(key(t, ix, "store.Store"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]query.Implementer{}
	for _, im := range impls {
		got[im.Type.Key] = im
	}
	mem := got["example.com/q/store.memStore"]
	if mem.Type.Key == "" || !mem.ViaPointer || mem.ViaEmbedding {
		t.Errorf("memStore: %+v (want pointer receiver, no embedding)", mem)
	}
	// fileStore embeds *memStore, so its value type already has Save and Load.
	fs := got["example.com/q/store.fileStore"]
	if fs.Type.Key == "" || fs.ViaPointer || !fs.ViaEmbedding {
		t.Errorf("fileStore: %+v (want by value, via embedding)", fs)
	}
	if len(impls) != 2 {
		t.Errorf("Store implementers: %d, want 2", len(impls))
	}

	// Signature comparison ignores parameter and result names.
	impls, err = ix.Implementers(key(t, ix, "store.Reader"))
	if err != nil {
		t.Fatal(err)
	}
	if len(impls) != 1 || impls[0].Type.Key != "example.com/q/store.buf" || impls[0].ViaPointer {
		t.Errorf("Reader implementers = %+v; want buf by value (Read(p []byte) (n int, err error) vs Read(b []byte) (int, error))", impls)
	}

	// The reverse direction.
	ifaces, err := ix.InterfacesOf(key(t, ix, "store.fileStore"))
	if err != nil || len(ifaces) != 1 || ifaces[0].Interface.Key != "example.com/q/store.Store" {
		t.Errorf("interfaces of fileStore = %+v, %v", ifaces, err)
	}

	// A concrete type without methods satisfies no non-empty interface.
	if ifaces, err := ix.InterfacesOf(key(t, ix, "store.User")); err != nil || len(ifaces) != 0 {
		t.Errorf("interfaces of User = %+v, %v", ifaces, err)
	}

	// Refusals are explicit.
	if _, err := ix.Implementers(key(t, ix, "store.Empty")); err == nil || !strings.Contains(err.Error(), "no methods") {
		t.Errorf("empty interface: %v", err)
	}
	if _, err := ix.Implementers(key(t, ix, "store.User")); err == nil {
		t.Error("a struct type was accepted as an interface")
	}
	if _, err := ix.InterfacesOf(key(t, ix, "store.NewMem")); err == nil {
		t.Error("a function was accepted as a type")
	}
}

func TestCallChain(t *testing.T) {
	ix := open(t)

	// Certain path: Run -> foo -> bar.
	c, err := ix.CallChain(key(t, ix, "svc.Run"), key(t, ix, "svc.bar"), 0)
	if err != nil || !c.Found || c.Candidate || len(c.Steps) != 2 {
		t.Fatalf("Run -> bar: %+v, %v", c, err)
	}
	if c.Steps[0].To.Key != "example.com/q/svc.foo" || c.Steps[1].To.Key != "example.com/q/svc.bar" {
		t.Errorf("path = %s -> %s", c.Steps[0].To.Key, c.Steps[1].To.Key)
	}
	for _, s := range c.Steps {
		if s.Certainty != query.Certain || s.Site.Line == 0 {
			t.Errorf("step %+v", s)
		}
	}

	// Through an external function.
	c, _ = ix.CallChain(key(t, ix, "svc.Run"), key(t, ix, "fmt.Sprint"), 0)
	if !c.Found || len(c.Steps) != 2 {
		t.Errorf("Run -> fmt.Sprint: %+v", c)
	}

	// Only possible via an interface call: a candidate path, labelled as such.
	c, err = ix.CallChain(key(t, ix, "svc.CreateUser"), key(t, ix, "memStore.Save"), 0)
	if err != nil || !c.Found || !c.Candidate || c.Steps[0].Certainty != query.Candidate {
		t.Errorf("CreateUser -> memStore.Save: %+v, %v", c, err)
	}

	// No path, and depth limits are honoured.
	c, _ = ix.CallChain(key(t, ix, "svc.helper"), key(t, ix, "svc.bar"), 0)
	if c.Found {
		t.Errorf("helper -> bar found a path: %+v", c)
	}
	c, _ = ix.CallChain(key(t, ix, "svc.Run"), key(t, ix, "svc.bar"), 1)
	if c.Found {
		t.Error("depth limit 1 still found a 2-call path")
	}
}

// The `foo` example: b := a; bar(b, a); return b.
func TestEvents(t *testing.T) {
	ix := open(t)
	evs, err := ix.Events(key(t, ix, "foo::b"), query.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	var roles []string
	var arg *query.Event
	var ret *query.Event
	for i, e := range evs {
		roles = append(roles, e.Role)
		if e.ArgOf != 0 {
			arg = &evs[i]
		}
		if e.FlowTo != "" && strings.Contains(e.FlowTo, "#result") {
			ret = &evs[i]
		}
	}
	if !slices.Equal(roles, []string{"definition", "read", "read"}) {
		t.Errorf("events of b: %v", roles)
	}
	if arg == nil || arg.PassedTo == nil || arg.PassedTo.Key != "example.com/q/svc.bar" || arg.ArgIndex != 0 ||
		arg.Param != "example.com/q/svc.bar#param0" {
		t.Errorf("argument event: %+v", arg)
	}
	if ret == nil || ret.FlowTo != "example.com/q/svc.foo#result0" {
		t.Errorf("return event: %+v", ret)
	}
	if !slices.IsSortedFunc(evs, func(a, b query.Event) int { return a.Line - b.Line }) {
		t.Error("events are not in source order")
	}
}

func TestFilters(t *testing.T) {
	ix := open(t)
	all, _ := ix.References(key(t, ix, "store.User"), query.Filter{})
	roleOnly, _ := ix.References(key(t, ix, "store.User"), query.Filter{Roles: []model.Role{model.RoleConstruct}})
	if len(roleOnly) != 1 || roleOnly[0].Role != "construct" {
		t.Errorf("construct-only references = %v (of %d)", locs(roleOnly), len(all))
	}
}
