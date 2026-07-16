package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestHandleAPIJobsReturnsInsertedJob(t *testing.T) {
	srv, st := newTestServer(t)
	insertTestJob(t, st)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp apiJobsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if resp.TotalJobs != 1 {
		t.Errorf("TotalJobs = %d, want 1", resp.TotalJobs)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].Title != "Backend Engineer" {
		t.Errorf("Jobs = %+v, want one job titled Backend Engineer", resp.Jobs)
	}
}

func TestHandleAPIPatchJobUpdatesStatusAndNotes(t *testing.T) {
	srv, st := newTestServer(t)
	id := insertTestJob(t, st)

	body := strings.NewReader(`{"status":"applied","notes":"followed up"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/jobs/"+strconv.FormatInt(id, 10), body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}

	job, err := st.GetJob(req.Context(), id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "applied" || job.Notes != "followed up" {
		t.Errorf("job = %+v, want status=applied notes='followed up'", job)
	}
}

func TestHandleAPIPatchJobRejectsInvalidStatus(t *testing.T) {
	srv, st := newTestServer(t)
	id := insertTestJob(t, st)

	body := strings.NewReader(`{"status":"bogus"}`)
	req := httptest.NewRequest(http.MethodPatch, "/api/jobs/"+strconv.FormatInt(id, 10), body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAPICronRunRejectsUnknownJob(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/cron/bogus/run", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleAPICronRunExecutesScript(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.logsDir = t.TempDir()

	markerDir := t.TempDir()
	marker := filepath.Join(markerDir, "ran")
	script := filepath.Join(markerDir, "fake-job.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\ntouch \""+marker+"\"\n"), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	original := cronJobDefs
	cronJobDefs = []cronJob{{Name: "fake-job", Script: script}}
	t.Cleanup(func() { cronJobDefs = original })

	req := httptest.NewRequest(http.MethodPost, "/api/cron/fake-job/run", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", w.Code, w.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake-job.sh did not run within 2s")
}

func TestHandleAPICronReturnsAllJobDefs(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/cron", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var jobs []CronJobStatus
	if err := json.Unmarshal(w.Body.Bytes(), &jobs); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if len(jobs) != len(cronJobDefs) {
		t.Errorf("len(jobs) = %d, want %d", len(jobs), len(cronJobDefs))
	}
	for _, j := range jobs {
		if j.HasRun {
			t.Errorf("job %s: HasRun = true in a fresh temp dir with no status files", j.Name)
		}
	}
}
