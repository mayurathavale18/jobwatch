// Package web serves the local jobwatch dashboard: a React+Vite SPA (built
// to dist/, embedded below) backed by a JSON API.
package web

import (
	"context"
	"embed"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"jobwatch/internal/jobsubmit"
	"jobwatch/internal/store"
)

//go:embed dist
var distFS embed.FS

// Server serves the dashboard.
type Server struct {
	store   *store.Store
	dist    fs.FS
	logsDir string
	pages   jobsubmit.PageFetcher
}

func NewServer(st *store.Store) (*Server, error) {
	dist, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil, err
	}
	// Relative to the process's cwd, same convention as config.yaml's
	// db_path -- both assume `jobwatch serve` runs from the repo root
	// (which is how the dashboard-watchdog wrapper always starts it).
	return &Server{store: st, dist: dist, logsDir: "logs", pages: jobsubmit.HTTPPageFetcher{}}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/jobs", s.handleAPIJobs)
	mux.HandleFunc("POST /api/jobs/manual", s.handleAPIJobsManual)
	mux.HandleFunc("PATCH /api/jobs/{id}", s.handleAPIPatchJob)
	mux.HandleFunc("POST /api/jobs/{id}/outreach", s.handleAPIJobsOutreach)
	mux.HandleFunc("GET /api/cron", s.handleAPICron)
	mux.HandleFunc("POST /api/cron/{name}/run", s.handleAPICronRun)
	mux.Handle("/", s.spaHandler())
	return withLogging(mux)
}

// spaHandler serves the embedded Vite build: real files (JS/CSS/favicon) are
// served as-is, and any other path (e.g. /cron, a client-side route) falls
// back to index.html so the SPA's own routing takes over.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path != "/" {
			if _, err := fs.Stat(s.dist, path[1:]); err != nil {
				// index.html has no content hash in its filename (unlike
				// the JS/CSS bundles it references), so it must never be
				// cached -- otherwise a browser that cached it before a
				// deploy keeps loading the old JS bundle by its old,
				// still-valid hashed URL, silently missing new features.
				w.Header().Set("Cache-Control", "no-cache")
				r2 := new(http.Request)
				*r2 = *r
				r2.URL.Path = "/"
				fileServer.ServeHTTP(w, r2)
				return
			}
		}
		if path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		fileServer.ServeHTTP(w, r)
	})
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path)
	})
}

func httpError(w http.ResponseWriter, msg string, err error) {
	slog.Error(msg, "error", err)
	http.Error(w, msg, http.StatusInternalServerError)
}

// Serve starts the dashboard HTTP server and blocks until ctx is cancelled.
func Serve(ctx context.Context, addr string, st *store.Store) error {
	srv, err := NewServer(st)
	if err != nil {
		return err
	}

	httpServer := &http.Server{Addr: addr, Handler: srv.Handler()}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("dashboard listening", "addr", addr)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
