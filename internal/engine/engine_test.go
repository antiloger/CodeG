package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"codeg/internal/engine"
	"codeg/internal/index"
)

// fixture is a small module with the shapes that matter for invalidation:
//
//	lib   <- mid  <- app  <- top      mid exposes lib.Base; app exposes mid.Wrapper
//	other <- app                      app uses other internally only
//	util                              standalone, with build-constrained files
func fixture() map[string]string {
	return map[string]string{
		"go.mod": "module example.com/m\n\ngo 1.24\n",
		"lib/lib.go": `package lib

type Base struct{ ID int }

func (b Base) Hello() string { return "hello" }

func Add(a, b int) int { return a + b }
`,
		"mid/mid.go": `package mid

import "example.com/m/lib"

type Wrapper struct{ lib.Base }

func Make() Wrapper { return Wrapper{} }
`,
		"other/other.go": `package other

func Helper() int { return 1 }
`,
		"app/app.go": `package app

import (
	"example.com/m/mid"
	"example.com/m/other"
)

type App struct{ mid.Wrapper }

func Run() int { return other.Helper() + len(mid.Make().Hello()) }
`,
		"app/app_test.go": `package app

import "testing"

func TestRun(t *testing.T) { _ = Run() }
`,
		"top/top.go": `package top

import "example.com/m/app"

func Top() int { return app.Run() }
`,
		"util/util.go": `package util

func Name() string { return name }
`,
		"util/util_linux.go":   "//go:build linux\n\npackage util\n\nconst name = \"linux\"\n",
		"util/util_other.go":   "//go:build !linux\n\npackage util\n\nconst name = \"other\"\n",
		"util/util_windows.go": "package util\n\nconst windowsOnly = 1\n",
	}
}

const del = "\x00delete"

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if content == del {
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func opts(root, store string) engine.Options {
	return engine.Options{Root: root, StoreDir: store}
}

// step is one edit followed by an incremental run.
type step struct {
	name  string
	edit  map[string]string
	tags  []string // build tags for this and later steps
	want  []string // packages that must be re-indexed, exactly
	gone  []string // packages that must be reported removed
	nocom bool     // no commit expected
}

const (
	lib   = "example.com/m/lib"
	mid   = "example.com/m/mid"
	other = "example.com/m/other"
	app   = "example.com/m/app"
	top   = "example.com/m/top"
	util  = "example.com/m/util"
)

func TestIncrementalEqualsFromScratch(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go command many times")
	}
	root := t.TempDir()
	incStore := filepath.Join(t.TempDir(), "inc")
	write(t, root, fixture())

	libBody := strings.Replace(fixture()["lib/lib.go"], "a + b", "b + a", 1)
	all := []string{app, lib, mid, other, top, util}
	steps := []step{
		{name: "initial index", want: all},
		{name: "no change", want: nil, nocom: true},
		{name: "body change in lib", edit: map[string]string{"lib/lib.go": libBody},
			want: []string{lib}}, // early cutoff: API unchanged, importers untouched
		{name: "comments shift lines in lib", edit: map[string]string{
			"lib/lib.go": "// Package lib.\n//\n// More docs.\n" + libBody},
			want: []string{lib}}, // positions are not part of the API hash
		{name: "unexported addition in lib", edit: map[string]string{
			"lib/extra.go": "package lib\n\nfunc helper() int { return 1 }\n"},
			want: []string{lib}},
		{name: "rename a file", edit: map[string]string{
			"lib/extra.go": del, "lib/renamed.go": "package lib\n\nfunc helper() int { return 1 }\n"},
			want: []string{lib}}, // same content, new path: lib re-indexed, API unchanged
		{name: "signature change in lib", edit: map[string]string{
			"lib/lib.go": "// Package lib.\n//\n// More docs.\n" + strings.Replace(strings.Replace(libBody, "a, b int", "a, b, c int", 1), "b + a", "a + b + c", 1)},
			want: []string{lib, mid, app, top}}, // mid exposes lib.Base, app exposes mid.Wrapper, top imports app
		{name: "add exported func in lib", edit: map[string]string{
			"lib/more.go": "package lib\n\nfunc More() {}\n"},
			want: []string{lib, mid, app, top}},
		{name: "delete file in lib", edit: map[string]string{"lib/more.go": del},
			want: []string{lib, mid, app, top}},
		{name: "new method on exposed type reaches transitive importers", edit: map[string]string{
			"lib/base.go": "package lib\n\nfunc (b Base) Extra() int { return b.ID }\n"},
			want: []string{lib, mid, app, top}},
		{name: "api change in an internal-only dependency stops at its importer", edit: map[string]string{
			"other/other.go": "package other\n\nfunc Helper() int { return 1 }\n\nfunc New() {}\n"},
			want: []string{other, app}}, // app re-indexed; app's own API is unchanged so top is not
		{name: "build tag flip", edit: map[string]string{
			"util/util_linux.go": "//go:build !linux\n\npackage util\n\nconst name = \"linux\"\n",
			"util/util_other.go": "//go:build linux\n\npackage util\n\nconst name = \"other\"\n"},
			want: []string{util}},
		{name: "build configuration change", tags: []string{"experiment"}, want: all},
		{name: "go.mod edit", edit: map[string]string{"go.mod": "module example.com/m\n\ngo 1.24\n\n// touched\n"},
			want: all},
		{name: "package removed", edit: map[string]string{"util": del},
			want: nil, gone: []string{util}},
	}

	var tags []string
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			write(t, root, s.edit)
			if s.tags != nil {
				tags = s.tags
			}
			prev, _ := index.OpenStore(incStore).ReadManifest()
			o := opts(root, incStore)
			o.Tags = tags
			res, err := engine.Index(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if !sameSet(res.Reindexed, s.want) {
				t.Errorf("re-indexed %v, want %v", res.Reindexed, s.want)
			}
			if !sameSet(res.Removed, s.gone) {
				t.Errorf("removed %v, want %v", res.Removed, s.gone)
			}
			if s.nocom && res.Committed {
				t.Error("a run with no changes wrote a new manifest")
			}

			// The invariant: a from-scratch index of the same tree is equal.
			scratchStore := filepath.Join(t.TempDir(), "scratch")
			so := opts(root, scratchStore)
			so.Tags = tags
			if _, err := engine.Index(context.Background(), so); err != nil {
				t.Fatal(err)
			}
			compareStores(t, incStore, scratchStore)
			checkStoreFiles(t, incStore, prev)
		})
	}
}

// sameSet compares two package lists ignoring order.
func sameSet(a, b []string) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	slices.Sort(a)
	slices.Sort(b)
	return slices.Equal(a, b)
}

// compareStores fails if the two stores differ in anything but stat data and
// timestamps.
func compareStores(t *testing.T, aDir, bDir string) {
	t.Helper()
	a, b := index.OpenStore(aDir), index.OpenStore(bDir)
	am, err := a.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	bm, err := b.ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if am.Schema != bm.Schema || am.EnvHash != bm.EnvHash || am.Root != bm.Root || !reflect.DeepEqual(am.Config, bm.Config) {
		t.Errorf("manifest headers differ:\n inc     %+v\n scratch %+v", am, bm)
	}
	if len(am.Packages) != len(bm.Packages) {
		t.Fatalf("package sets differ: incremental %d, scratch %d", len(am.Packages), len(bm.Packages))
	}
	for i := range am.Packages {
		ap, bp := am.Packages[i], bm.Packages[i]
		ap.Files, bp.Files = stripStat(ap.Files), stripStat(bp.Files)
		if !reflect.DeepEqual(ap, bp) {
			t.Errorf("package %s entries differ:\n inc     %+v\n scratch %+v", ap.Path, ap, bp)
			continue
		}
		ar, err := a.OpenPackage(&am.Packages[i])
		if err != nil {
			t.Fatal(err)
		}
		br, err := b.OpenPackage(&bm.Packages[i])
		if err != nil {
			t.Fatal(err)
		}
		af, err := ar.Facts()
		if err != nil {
			t.Fatal(err)
		}
		bf, err := br.Facts()
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(af, bf) {
			t.Errorf("package %s facts differ between incremental and from-scratch", ap.Path)
		}
	}
	as, err := a.ReadSummary(am)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := b.ReadSummary(bm)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(as, bs) {
		t.Errorf("summaries differ:\n inc     %+v\n scratch %+v", as, bs)
	}
}

func stripStat(files []index.FileEntry) []index.FileEntry {
	out := slices.Clone(files)
	for i := range out {
		out[i].Size, out[i].ModTime = 0, 0
	}
	return out
}

// checkStoreFiles checks the on-disk layout: exactly the files the manifest
// names (garbage collection ran), and nothing else.
func checkStoreFiles(t *testing.T, dir string, prev *index.Manifest) {
	t.Helper()
	m, err := index.OpenStore(dir).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"manifest.bin": true, "lock": true, m.Summary: true}
	for _, p := range m.Packages {
		want[filepath.Join("pkg", p.File)] = true
	}
	// The previous generation's files stay for one more run, so readers that
	// opened it keep working (see Store.GC).
	allowed := map[string]bool{}
	if prev != nil {
		allowed[prev.Summary] = true
		for _, p := range prev.Packages {
			allowed[filepath.Join("pkg", p.File)] = true
		}
	}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if !want[rel] && !allowed[rel] {
			t.Errorf("stray file in the store: %s", rel)
		}
		delete(want, rel)
		return nil
	})
	for f := range want {
		t.Errorf("file named by the manifest is missing: %s", f)
	}
}

// A file edited right after an index run, keeping its size and modification
// time, must still be noticed: its mtime is inside the racy window.
func TestRacyEditIsDetected(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	store := filepath.Join(t.TempDir(), "s")
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(root, "lib", "lib.go")
	st, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	edited := []byte(strings.Replace(string(data), "hello", "HELLO", 1)) // same length
	if err := os.WriteFile(path, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(path, st.ModTime(), st.ModTime()) // and the same mtime

	res, err := engine.Index(context.Background(), opts(root, store))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Reindexed, lib) {
		t.Errorf("same-size same-mtime edit went unnoticed; re-indexed %v", res.Reindexed)
	}
}

// Old files, by contrast, are trusted by stat data alone.
func TestStatFastPathSkipsRehash(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	old := time.Now().Add(-time.Hour)
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			os.Chtimes(p, old, old)
		}
		return nil
	})
	store := filepath.Join(t.TempDir(), "s")
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Index(context.Background(), opts(root, store))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Reindexed) != 0 || res.Committed {
		t.Errorf("unchanged tree: reindexed %v, committed %v", res.Reindexed, res.Committed)
	}
}

// A run that fails or is cancelled must leave the last committed index intact
// and readable, and the next run must recover.
func TestInterruptedRunKeepsLastIndex(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	store := filepath.Join(t.TempDir(), "s")
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(store, "manifest.bin"))

	write(t, root, map[string]string{"lib/lib.go": strings.Replace(fixture()["lib/lib.go"], "a + b", "a - b", 1)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := engine.Index(ctx, opts(root, store)); err == nil {
		t.Fatal("cancelled run reported success")
	}
	after, _ := os.ReadFile(filepath.Join(store, "manifest.bin"))
	if string(before) != string(after) {
		t.Error("cancelled run changed the manifest")
	}
	if _, err := index.OpenStore(store).ReadManifest(); err != nil {
		t.Errorf("last index unreadable after a cancelled run: %v", err)
	}

	res, err := engine.Index(context.Background(), opts(root, store))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(res.Reindexed, lib) {
		t.Errorf("recovery run did not re-index lib: %v", res.Reindexed)
	}
	scratch := filepath.Join(t.TempDir(), "scratch")
	if _, err := engine.Index(context.Background(), opts(root, scratch)); err != nil {
		t.Fatal(err)
	}
	compareStores(t, store, scratch)
}

func TestSecondIndexerIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	store := filepath.Join(t.TempDir(), "s")
	unlock, err := index.OpenStore(store).Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	if _, err := engine.Index(context.Background(), opts(root, store)); err == nil || !strings.Contains(err.Error(), "lock") {
		t.Errorf("want a lock error, got %v", err)
	}
}

// Go files under node_modules match ./... but are not part of the project.
func TestNodeModulesIsNotIndexed(t *testing.T) {
	root := t.TempDir()
	files := fixture()
	files["web/node_modules/thing/golang/thing.go"] = "package thing\n\nfunc F() {}\n"
	write(t, root, files)
	res, err := engine.Index(context.Background(), opts(root, filepath.Join(t.TempDir(), "s")))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Reindexed {
		if strings.Contains(p, "node_modules") {
			t.Errorf("indexed a node_modules package: %s", p)
		}
	}
	if len(res.Reindexed) != 6 {
		t.Errorf("indexed %d packages, want the 6 project packages: %v", len(res.Reindexed), res.Reindexed)
	}
}

// An unreadable summary is rebuilt even when no package changed.
func TestDamagedSummaryIsRebuilt(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	store := filepath.Join(t.TempDir(), "s")
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	m, _ := index.OpenStore(store).ReadManifest()
	if err := os.WriteFile(filepath.Join(store, m.Summary), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := engine.Index(context.Background(), opts(root, store))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || len(res.Reindexed) != 0 {
		t.Errorf("committed=%v reindexed=%v: want a rebuilt summary and no re-indexing", res.Committed, res.Reindexed)
	}
	m2, err := index.OpenStore(store).ReadManifest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := index.OpenStore(store).ReadSummary(m2); err != nil {
		t.Errorf("summary still unreadable: %v", err)
	}
}

// Files of the previous generation survive one commit, then go: a reader that
// opened the old index keeps working through a commit, and disk use is bounded.
func TestPreviousGenerationIsKeptOneRun(t *testing.T) {
	root := t.TempDir()
	write(t, root, fixture())
	store := filepath.Join(t.TempDir(), "s")
	st := index.OpenStore(store)
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	gen1, _ := st.ReadManifest()
	oldFile := filepath.Join(store, "pkg", gen1.Package(lib).File)

	write(t, root, map[string]string{"lib/lib.go": strings.Replace(fixture()["lib/lib.go"], "a + b", "b + a", 1)})
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFile); err != nil {
		t.Errorf("generation 1's lib file was removed at the commit of generation 2: %v", err)
	}
	// A reader opened before the commit can still load it.
	if _, err := st.OpenPackage(gen1.Package(lib)); err != nil {
		t.Errorf("reading the previous generation after a commit: %v", err)
	}

	write(t, root, map[string]string{"lib/lib.go": strings.Replace(fixture()["lib/lib.go"], "a + b", "a - b", 1)})
	if _, err := engine.Index(context.Background(), opts(root, store)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldFile); err == nil {
		t.Error("generation 1's file is still there after generation 3")
	}
}
