package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"jobwatch/internal/providers"
	"jobwatch/internal/store"
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

func TestHandleAPIJobsPaginates(t *testing.T) {
	srv, st := newTestServer(t)
	ctx := context.Background()
	for i := 0; i < 30; i++ {
		job := providers.Job{
			Provider: "greenhouse", CompanySlug: "stripe", CompanyName: "Stripe",
			ExternalID: fmt.Sprintf("%d", i), Title: "Backend Engineer", Location: "Remote",
			URL: fmt.Sprintf("https://example.com/%d", i), FirstSeenAt: time.Now().UTC(), Raw: []byte(`{}`),
		}
		tx, err := st.BeginTx(ctx)
		if err != nil {
			t.Fatalf("BeginTx: %v", err)
		}
		if _, err := st.InsertJob(ctx, tx, job, store.StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	// Default page (no ?page param): 25 of 30 jobs, page 1.
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	var resp apiJobsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if resp.Page != 1 || resp.PageSize != 25 || resp.TotalFiltered != 30 || len(resp.Jobs) != 25 {
		t.Fatalf("page1 resp = %+v, len(Jobs)=%d", resp, len(resp.Jobs))
	}

	// Page 2: remaining 5 jobs.
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?page=2", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if resp.Page != 2 || len(resp.Jobs) != 5 {
		t.Fatalf("page2 resp = %+v, len(Jobs)=%d", resp, len(resp.Jobs))
	}

	// Out-of-range page: empty jobs, no error.
	req = httptest.NewRequest(http.MethodGet, "/api/jobs?page=99", nil)
	w = httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if len(resp.Jobs) != 0 {
		t.Fatalf("out-of-range page Jobs = %+v, want empty", resp.Jobs)
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

func TestHandleAPIJobsManualInsertsAndTriggersOneOff(t *testing.T) {
	srv, st := newTestServer(t)
	// Same reason as TestHandleAPICronRunExecutesScript: NewServer's
	// default logsDir ("logs") is relative to cwd, which is this
	// package's directory under `go test`, not the repo root -- so the
	// handler's log-file open would fail without a real dir to write to.
	srv.logsDir = t.TempDir()

	body := strings.NewReader(`{"url":"https://valorem.keka.com/careers/jobdetails/124256"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/manual", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", w.Code, w.Body.String())
	}

	var resp apiManualJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}
	if resp.AlreadyExisted {
		t.Errorf("AlreadyExisted = true, want false for a fresh URL")
	}
	if resp.ID == 0 {
		t.Errorf("ID = 0, want a nonzero job id")
	}

	rows, err := st.ListJobs(req.Context(), store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 || rows[0].Provider != "manual" {
		t.Fatalf("rows = %+v, want one manual job inserted", rows)
	}
}

func TestHandleAPIJobsManualRejectsMissingURL(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/manual", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAPIJobsManualDuplicateReturns200(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.logsDir = t.TempDir() // see comment in TestHandleAPIJobsManualInsertsAndTriggersOneOff
	const jobURL = "https://valorem.keka.com/careers/jobdetails/124256"

	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/jobs/manual", strings.NewReader(`{"url":"`+jobURL+`"}`))
		w := httptest.NewRecorder()
		srv.Handler().ServeHTTP(w, req)
		return w
	}

	first := post()
	if first.Code != http.StatusAccepted {
		t.Fatalf("first submit status = %d, want 202", first.Code)
	}

	second := post()
	if second.Code != http.StatusOK {
		t.Fatalf("second submit status = %d, want 200 (already existed)", second.Code)
	}
	var resp apiManualJobResponse
	if err := json.Unmarshal(second.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.AlreadyExisted {
		t.Errorf("AlreadyExisted = false, want true on resubmit")
	}
}

func TestHandleAPIJobsManualPersistsJDText(t *testing.T) {
	srv, st := newTestServer(t)
	srv.logsDir = t.TempDir() // see comment in TestHandleAPIJobsManualInsertsAndTriggersOneOff

	body := strings.NewReader(`{"url":"https://valorem.keka.com/careers/jobdetails/124256","jdText":"  We need Go and Kubernetes experience.  "}`)
	req := httptest.NewRequest(http.MethodPost, "/api/jobs/manual", body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", w.Code, w.Body.String())
	}

	var resp apiManualJobResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody: %s", err, w.Body.String())
	}

	got, err := st.GetJob(req.Context(), resp.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ManualJDText != "We need Go and Kubernetes experience." {
		t.Errorf("ManualJDText = %q, want trimmed JD text", got.ManualJDText)
	}
}
