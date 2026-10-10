// Package server is the localhost HTTP backend for the web UI.
//
// It binds to loopback only and rejects requests whose Host or Origin is not
// its own address, which is what stops a web page on another site (or a DNS
// rebinding attack) from reading source code through the browser. It is
// read-only: GET and HEAD, no cookies, no state. It serves only files that are
// in the index manifest.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"codeg/internal/query"
	"codeg/internal/webui"
)

// Options configure a Server.
type Options struct {
	StoreDir string
	// DevOrigins are extra Origins allowed to call the API, for the Vite dev
	// server (http://localhost:5173) which proxies to this server.
	DevOrigins []string
	// UI is the static site; nil serves the embedded build.
	UI fs.FS
	// Logf receives non-fatal events (index reloads, errors). Nil discards.
	Logf func(format string, args ...any)
}

// Server serves one index. It re-reads the index when a new generation is
// committed, so a running server follows `codeg index`.
type Server struct {
	opts Options
	ui   fs.FS

	mu       sync.Mutex
	ix       *query.Index
	stamp    stamp
	hosts    map[string]bool
	origins  map[string]bool
	devHosts map[string]bool
}

type stamp struct {
	mod  time.Time
	size int64
}

// New opens the index in opts.StoreDir.
func New(opts Options) (*Server, error) {
	s := &Server{opts: opts, ui: opts.UI, hosts: map[string]bool{}, origins: map[string]bool{}}
	if s.ui == nil {
		s.ui = webui.FS()
	}
	if err := s.reload(); err != nil {
		return nil, err
	}
	for _, o := range opts.DevOrigins {
		s.origins[strings.ToLower(strings.TrimRight(o, "/"))] = true
	}
	return s, nil
}

func (s *Server) logf(format string, args ...any) {
	if s.opts.Logf != nil {
		s.opts.Logf(format, args...)
	}
}

func (s *Server) manifestStamp() (stamp, error) {
	st, err := os.Stat(filepath.Join(s.opts.StoreDir, "manifest.bin"))
	if err != nil {
		return stamp{}, err
	}
	return stamp{st.ModTime(), st.Size()}, nil
}

// reload opens the current index generation.
func (s *Server) reload() error {
	st, err := s.manifestStamp()
	if err != nil {
		return fmt.Errorf("no index in %s; run \"codeg index\" first", s.opts.StoreDir)
	}
	ix, err := query.Open(s.opts.StoreDir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ix, s.stamp = ix, st
	s.mu.Unlock()
	return nil
}

// index returns the current index, switching to a newer generation if the
// manifest was replaced. Until the swap completes readers keep the old one, and
// a failed reload keeps serving it.
func (s *Server) index() *query.Index {
	st, err := s.manifestStamp()
	s.mu.Lock()
	changed := err == nil && st != s.stamp
	s.mu.Unlock()
	if changed {
		if err := s.reload(); err != nil {
			s.logf("reloading the index failed, still serving the previous one: %v", err)
		} else {
			s.logf("index reloaded")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ix
}

// Listen binds a loopback address. Anything else is refused: this server
// exposes source code and has no authentication.
func Listen(addr string) (net.Listener, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("bad address %q: %w", addr, err)
	}
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("refusing to listen on %q: codeg serves source code without authentication, so it binds to 127.0.0.1 only", addr)
	}
	if host == "localhost" {
		addr = net.JoinHostPort("127.0.0.1", addr[strings.LastIndex(addr, ":")+1:])
	}
	return net.Listen("tcp", addr)
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Serve serves on ln until ctx is cancelled.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	s.allowAddr(ln.Addr())
	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          log.New(discard{}, "", 0),
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// allowAddr records the Host and Origin values that name this server.
func (s *Server) allowAddr(a net.Addr) {
	_, port, _ := net.SplitHostPort(a.String())
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range []string{"127.0.0.1", "localhost", "[::1]"} {
		host := h + ":" + port
		s.hosts[host] = true
		s.origins["http://"+host] = true
	}
}

// Handler returns the HTTP handler. Call allowAddr (Serve does) first, or use
// AllowHost in tests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h func(*query.Index, http.ResponseWriter, *http.Request)) {
		mux.HandleFunc("GET "+pattern, func(w http.ResponseWriter, r *http.Request) {
			h(s.index(), w, r)
		})
	}
	api("/api/status", s.status)
	api("/api/graph/packages", s.graphPackages)
	api("/api/graph/package", s.graphPackage)
	api("/api/graph/function", s.graphFunction)
	api("/api/symbol", s.symbol)
	api("/api/source", s.source)
	api("/api/resolve", s.resolve)
	api("/api/references", s.references)
	api("/api/callers", s.calls)
	api("/api/callees", s.calls)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint: "+r.URL.Path)
	})
	mux.Handle("/", s.static())
	return s.guard(mux)
}

// AllowHost adds a Host (and matching http Origin) to the allowed set. It is
// for tests that use httptest servers.
func (s *Server) AllowHost(host string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosts[strings.ToLower(host)] = true
	s.origins["http://"+strings.ToLower(host)] = true
}

// guard enforces the security policy on every request.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; worker-src 'self' blob:; connect-src 'self'; frame-ancestors 'none'")

		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			h.Set("Allow", "GET, HEAD")
			writeError(w, http.StatusMethodNotAllowed, "read-only server")
			return
		}
		s.mu.Lock()
		hostOK := s.hosts[strings.ToLower(r.Host)]
		origin := strings.ToLower(r.Header.Get("Origin"))
		originOK := origin == "" || s.origins[origin]
		s.mu.Unlock()
		// A DNS-rebinding request arrives with the attacker's hostname in Host;
		// a cross-site fetch carries the attacker's site in Origin.
		if !hostOK {
			writeError(w, http.StatusForbidden, "unexpected Host header")
			return
		}
		if !originOK {
			writeError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// static serves the UI, falling back to index.html for client-side routes.
func (s *Server) static() http.Handler {
	files := http.FileServer(http.FS(s.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if st, err := fs.Stat(s.ui, p); err != nil || st.IsDir() {
			// Unknown path without an extension is a UI route; a missing asset is a 404.
			if path.Ext(p) != "" {
				http.NotFound(w, r)
				return
			}
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/"
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r2)
			return
		}
		if strings.HasPrefix(p, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable") // content-hashed names
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// fail maps a query error to a status code.
func fail(w http.ResponseWriter, err error) {
	var amb *query.AmbiguousError
	switch {
	case errors.As(err, &amb):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "candidates": amb.Candidates})
	case errors.Is(err, query.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func boolParam(r *http.Request, name string) bool {
	v := r.URL.Query().Get(name)
	return v == "1" || v == "true"
}

func intParam(r *http.Request, name string, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get(name)); err == nil {
		return n
	}
	return def
}
