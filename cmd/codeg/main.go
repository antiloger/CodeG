// Command codeg indexes a Go repository into a compact on-disk code graph.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"codeg/internal/engine"
	"codeg/internal/index"
)

const usage = `usage: codeg <command> [flags]

commands:
  index    build or update the index in .codeg/ (only changed packages are re-indexed)
  status   show what the index contains
  serve    serve the index and web UI on localhost
  query    ask the index: def, refs, callers, callees, reads, writes, types, implements, chain, events

Run "codeg <command> -h" for a command's flags.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var err error
	switch cmd, args := os.Args[1], os.Args[2:]; cmd {
	case "index":
		err = runIndex(ctx, args)
	case "status":
		err = runStatus(args)
	case "query":
		err = runQuery(args)
	case "serve":
		err = runServe(ctx, args)
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "codeg: unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "codeg:", err)
		os.Exit(1)
	}
}

func runIndex(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	dir := fs.String("dir", ".", "module or workspace root")
	store := fs.String("store", "", "index directory (default: <dir>/.codeg)")
	goos := fs.String("goos", "", "target GOOS (default: host)")
	goarch := fs.String("goarch", "", "target GOARCH (default: host)")
	tags := fs.String("tags", "", "comma-separated build tags")
	cgo := fs.String("cgo", "", `"0" or "1" to force CGO_ENABLED`)
	verbose := fs.Bool("v", false, "log which packages are loaded")
	fs.Parse(args)

	o := engine.Options{Root: *dir, StoreDir: *store, GOOS: *goos, GOARCH: *goarch, CGO: *cgo}
	if *tags != "" {
		o.Tags = strings.Split(*tags, ",")
	}
	if *verbose {
		o.Logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, format+"\n", a...) }
	}
	start := time.Now()
	res, err := engine.Index(ctx, o)
	if err != nil {
		return err
	}
	fmt.Printf("indexed %d package(s), reused %d, removed %d in %s\n",
		len(res.Reindexed), len(res.Reused), len(res.Removed), time.Since(start).Round(time.Millisecond))
	if !res.Committed {
		fmt.Println("index is up to date")
	}
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	dir := fs.String("dir", ".", "module or workspace root")
	store := fs.String("store", "", "index directory (default: <dir>/.codeg)")
	fs.Parse(args)

	sd := *store
	if sd == "" {
		sd = filepath.Join(*dir, ".codeg")
	}
	s := index.OpenStore(sd)
	m, err := s.ReadManifest()
	if err != nil {
		if err == index.ErrNoIndex {
			return fmt.Errorf("no index in %s; run \"codeg index\" first", sd)
		}
		return err
	}
	var files, excluded, withErrors int
	var bytes int64
	for i := range m.Packages {
		p := &m.Packages[i]
		if p.HasErrors {
			withErrors++
		}
		for _, f := range p.Files {
			if f.Kind == index.FileExcluded {
				excluded++
			} else {
				files++
			}
		}
		if st, err := os.Stat(filepath.Join(sd, "pkg", p.File)); err == nil {
			bytes += st.Size()
		}
	}
	fmt.Printf("root:      %s\n", m.Root)
	fmt.Printf("built for: %s %s/%s cgo=%s tags=[%s]\n", m.Config.GoVersion, m.Config.GOOS, m.Config.GOARCH, m.Config.CGO, strings.Join(m.Config.Tags, ","))
	fmt.Printf("indexed:   %s ago\n", time.Since(time.Unix(0, m.WrittenAt)).Round(time.Second))
	fmt.Printf("packages:  %d (%d with errors)\n", len(m.Packages), withErrors)
	fmt.Printf("files:     %d indexed, %d excluded by build constraints\n", files, excluded)
	fmt.Printf("size:      %.1f KiB of package files\n", float64(bytes)/1024)
	for i := range m.Packages {
		for _, f := range m.Packages[i].Files {
			if f.Kind == index.FileExcluded {
				fmt.Printf("  excluded %s: %s\n", f.Path, f.Reason)
			}
		}
	}
	return nil
}
