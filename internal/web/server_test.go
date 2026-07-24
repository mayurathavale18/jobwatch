package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

type fakePages struct{}

func (fakePages) FetchTitle(ctx context.Context, rawURL string) (string, error) {
	return "Fake Title", nil
}

func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	srv, err := NewServer(st)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	srv.pages = fakePages{}
	return srv, st
}

func insertTestJob(t *testing.T, st *store.Store) int64 {
	t.Helper()
	ctx := context.Background()
	job := providers.Job{
		Provider: "greenhouse", CompanySlug: "stripe", CompanyName: "Stripe",
		ExternalID: "1", Title: "Backend Engineer", Location: "Remote",
		URL: "https://example.com/1", FirstSeenAt: time.Now().UTC(), Raw: []byte(`{}`),
	}
	tx, err := st.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := st.InsertJob(ctx, tx, job, store.StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	return id
}

func TestHandleIndexServesSPAShell(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "jobwatch") {
		t.Errorf("expected embedded index.html to contain page title, got:\n%s", body)
	}
	if got := w.Header().Get("Cache-Control"); got != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache (browsers must revalidate index.html on every load)", got)
	}
}

func TestHandleCronPathFallsBackToSPAShell(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/cron", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "jobwatch") {
		t.Errorf("expected /cron to fall back to the SPA shell, got:\n%s", w.Body.String())
	}
}
