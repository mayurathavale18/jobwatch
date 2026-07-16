// Package web serves the local jobwatch dashboard.
package web

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"jobwatch/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// Server serves the dashboard.
type Server struct {
	store   *store.Store
	tmpl    *template.Template
	logsDir string
}

func NewServer(st *store.Store) (*Server, error) {
	tmpl, err := template.ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	// Relative to the process's cwd, same convention as config.yaml's
	// db_path -- both assume `jobwatch serve` runs from the repo root
	// (which is how the dashboard-watchdog wrapper always starts it).
	return &Server{store: st, tmpl: tmpl, logsDir: "logs"}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("PATCH /jobs/{id}", s.handlePatchJob)
	mux.HandleFunc("GET /cron", s.handleCron)
	return withLogging(mux)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path)
	})
}

type indexData struct {
	Jobs          []store.JobRow
	Companies     []string
	StatusCounts  map[string]int
	TotalJobs     int
	LastPoll      *store.PollRun
	Statuses      []string
	FilterStatus  string
	FilterCompany string
	FilterSearch  string
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	filter := store.JobFilter{
		Status:  q.Get("status"),
		Company: q.Get("company"),
		Search:  q.Get("q"),
	}

	jobs, err := s.store.ListJobs(ctx, filter)
	if err != nil {
		httpError(w, "listing jobs", err)
		return
	}

	companies, err := s.store.CompanyNames(ctx)
	if err != nil {
		httpError(w, "listing companies", err)
		return
	}

	counts, err := s.store.StatusCounts(ctx)
	if err != nil {
		httpError(w, "counting statuses", err)
		return
	}

	total := 0
	for _, n := range counts {
		total += n
	}

	lastPoll, err := s.store.LastPollRun(ctx)
	if err != nil {
		httpError(w, "loading last poll run", err)
		return
	}

	data := indexData{
		Jobs:          jobs,
		Companies:     companies,
		StatusCounts:  counts,
		TotalJobs:     total,
		LastPoll:      lastPoll,
		Statuses:      store.ValidStatuses,
		FilterStatus:  filter.Status,
		FilterCompany: filter.Company,
		FilterSearch:  filter.Search,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "index.html", data); err != nil {
		slog.Error("rendering index template", "error", err)
	}
}

func (s *Server) handlePatchJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	if status := r.Form.Get("status"); status != "" {
		if !store.IsValidStatus(status) {
			http.Error(w, "invalid status", http.StatusBadRequest)
			return
		}
		if err := s.store.UpdateStatus(ctx, id, status); err != nil {
			httpError(w, "updating status", err)
			return
		}
	}

	if r.Form.Has("notes") {
		if err := s.store.UpdateNotes(ctx, id, r.Form.Get("notes")); err != nil {
			httpError(w, "updating notes", err)
			return
		}
	}

	w.WriteHeader(http.StatusOK)
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
