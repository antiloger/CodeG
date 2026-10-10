package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeg/internal/server"
)

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dir := fs.String("dir", ".", "module or workspace root")
	store := fs.String("store", "", "index directory (default: <dir>/.codeg)")
	addr := fs.String("addr", "127.0.0.1:7373", "address to listen on; loopback only (use port 0 for a free one)")
	dev := fs.String("dev-origin", "", "comma-separated extra Origins allowed to call the API (the Vite dev server, http://localhost:5173)")
	verbose := fs.Bool("v", false, "log index reloads and errors")
	fs.Parse(args)

	sd := *store
	if sd == "" {
		sd = filepath.Join(*dir, ".codeg")
	}
	o := server.Options{StoreDir: sd}
	if *dev != "" {
		o.DevOrigins = strings.Split(*dev, ",")
	}
	if *verbose {
		o.Logf = func(format string, a ...any) { fmt.Fprintf(os.Stderr, "codeg: "+format+"\n", a...) }
	}
	srv, err := server.New(o)
	if err != nil {
		return err
	}
	ln, err := server.Listen(*addr)
	if err != nil {
		return err
	}
	fmt.Printf("codeg: serving %s at http://%s (Ctrl-C to stop)\n", sd, ln.Addr())
	return srv.Serve(ctx, ln)
}
