package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

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

func TestHandleIndexRenders(t *testing.T) {
	srv, st := newTestServer(t)
	insertTestJob(t, st)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "Backend Engineer") {
		t.Errorf("expected body to contain job title, got:\n%s", body)
	}
	if !strings.Contains(body, "jobwatch") {
		t.Errorf("expected body to contain page title")
	}
}

func TestHandlePatchJobUpdatesStatus(t *testing.T) {
	srv, st := newTestServer(t)
	id := insertTestJob(t, st)

	form := url.Values{"status": {store.StatusApplied}}
	req := httptest.NewRequest(http.MethodPatch, "/jobs/"+strconv.FormatInt(id, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	got, err := st.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != store.StatusApplied {
		t.Errorf("Status = %q, want %q", got.Status, store.StatusApplied)
	}
}

func TestHandlePatchJobUpdatesNotes(t *testing.T) {
	srv, st := newTestServer(t)
	id := insertTestJob(t, st)

	form := url.Values{"notes": {"applied via referral"}}
	req := httptest.NewRequest(http.MethodPatch, "/jobs/"+strconv.FormatInt(id, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", w.Code, w.Body.String())
	}

	got, err := st.GetJob(context.Background(), id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Notes != "applied via referral" {
		t.Errorf("Notes = %q", got.Notes)
	}
}

func TestHandlePatchJobRejectsInvalidStatus(t *testing.T) {
	srv, st := newTestServer(t)
	id := insertTestJob(t, st)

	form := url.Values{"status": {"bogus"}}
	req := httptest.NewRequest(http.MethodPatch, "/jobs/"+strconv.FormatInt(id, 10), strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
