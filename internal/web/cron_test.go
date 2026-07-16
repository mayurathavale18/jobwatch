package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeStatusFile(t *testing.T, dir, name string, lastRun time.Time, status, detail string) {
	t.Helper()
	body := `{"name":"` + name + `","last_run":"` + lastRun.UTC().Format(time.RFC3339) +
		`","status":"` + status + `","detail":"` + detail + `"}`
	if err := os.WriteFile(filepath.Join(dir, name+".status.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("writing status file: %v", err)
	}
}

func TestLoadCronStatusesNeverRun(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.logsDir = t.TempDir() // empty: no status files at all

	statuses := srv.loadCronStatuses()
	if len(statuses) != len(cronJobDefs) {
		t.Fatalf("expected %d job statuses, got %d", len(cronJobDefs), len(statuses))
	}
	for _, st := range statuses {
		if st.HasRun {
			t.Errorf("job %s: HasRun = true, want false (no status file exists)", st.Name)
		}
	}
}

func TestLoadCronStatusesParsesFile(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	srv.logsDir = dir

	now := time.Now()
	writeStatusFile(t, dir, "poll", now, "OK", "companies_ok=2")

	statuses := srv.loadCronStatuses()
	var poll *CronJobStatus
	for i := range statuses {
		if statuses[i].Name == "poll" {
			poll = &statuses[i]
		}
	}
	if poll == nil {
		t.Fatal("poll status not found")
	}
	if !poll.HasRun {
		t.Error("HasRun = false, want true")
	}
	if poll.Status != "OK" {
		t.Errorf("Status = %q, want OK", poll.Status)
	}
	if poll.Detail != "companies_ok=2" {
		t.Errorf("Detail = %q", poll.Detail)
	}
	if poll.Stale {
		t.Error("Stale = true for a just-written status, want false")
	}
}

func TestLoadCronStatusesFlagsStale(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := t.TempDir()
	srv.logsDir = dir

	// poll's expected interval is 15 min; 2 hours ago is well past 3x that.
	writeStatusFile(t, dir, "poll", time.Now().Add(-2*time.Hour), "OK", "stale test")

	statuses := srv.loadCronStatuses()
	for _, st := range statuses {
		if st.Name == "poll" {
			if !st.Stale {
				t.Error("expected poll status to be flagged stale")
			}
			return
		}
	}
	t.Fatal("poll status not found")
}
