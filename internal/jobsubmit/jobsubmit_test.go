package jobsubmit

import (
	"context"
	"path/filepath"
	"testing"

	"jobwatch/internal/store"
)

type fakePages struct {
	titles map[string]string
	err    error
}

func (f fakePages) FetchTitle(ctx context.Context, rawURL string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.titles[rawURL], nil
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestInsertManualJobInsertsNewJob(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const jobURL = "https://valorem.keka.com/careers/jobdetails/124256"
	pages := fakePages{titles: map[string]string{jobURL: "Backend Engineer - Valorem"}}

	id, existed, company, title, err := InsertManualJob(ctx, st, jobURL, "", pages)
	if err != nil {
		t.Fatalf("InsertManualJob: %v", err)
	}
	if existed {
		t.Errorf("existed = true, want false for a fresh URL")
	}
	if company != "Keka" {
		t.Errorf("company = %q, want Keka (guessed from host)", company)
	}
	if title != "Backend Engineer - Valorem" {
		t.Errorf("title = %q", title)
	}
	if id == 0 {
		t.Errorf("id = 0, want a nonzero row id")
	}

	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 || rows[0].Provider != "manual" || rows[0].Status != store.StatusNew {
		t.Fatalf("rows = %+v, want one manual/new job", rows)
	}
}

func TestInsertManualJobPersistsJDText(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const jobURL = "https://valorem.keka.com/careers/jobdetails/124256"
	pages := fakePages{titles: map[string]string{jobURL: "Backend Engineer"}}
	const jdText = "We need a backend engineer with Go, Kubernetes, and PostgreSQL experience."

	id, _, _, _, err := InsertManualJob(ctx, st, jobURL, jdText, pages)
	if err != nil {
		t.Fatalf("InsertManualJob: %v", err)
	}

	got, err := st.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ManualJDText != jdText {
		t.Errorf("ManualJDText = %q, want %q", got.ManualJDText, jdText)
	}
}

func TestInsertManualJobDuplicateURLIsNoOp(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const jobURL = "https://valorem.keka.com/careers/jobdetails/124256"
	pages := fakePages{titles: map[string]string{jobURL: "Backend Engineer"}}

	id1, _, _, _, err := InsertManualJob(ctx, st, jobURL, "", pages)
	if err != nil {
		t.Fatalf("first InsertManualJob: %v", err)
	}

	id2, existed, _, _, err := InsertManualJob(ctx, st, jobURL, "", pages)
	if err != nil {
		t.Fatalf("second InsertManualJob: %v", err)
	}
	if !existed {
		t.Errorf("existed = false, want true for a resubmitted URL")
	}
	if id2 != id1 {
		t.Errorf("id2 = %d, want %d (same job)", id2, id1)
	}

	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected still 1 job after resubmit, got %d", len(rows))
	}
}

func TestInsertManualJobTitleFetchFailsStillInserts(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const jobURL = "https://wellfound.com/jobs/12345"

	id, existed, _, title, err := InsertManualJob(ctx, st, jobURL, "", fakePages{err: context.DeadlineExceeded})
	if err != nil {
		t.Fatalf("InsertManualJob: %v", err)
	}
	if existed || id == 0 {
		t.Fatalf("id=%d existed=%v, want a fresh insert", id, existed)
	}
	if title == "" {
		t.Errorf("title empty, want a placeholder mentioning the fetch failure")
	}
}

func TestInsertManualJobCompanyFromURLHandlesCcTLDs(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	tests := []struct {
		url           string
		expectedTitle string
		wantCompany   string
	}{
		{
			url:         "https://somecompany.co.in/careers/123",
			wantCompany: "Somecompany",
		},
		{
			url:         "https://somecompany.co.uk/jobs/123",
			wantCompany: "Somecompany",
		},
		{
			url:         "https://valorem.keka.com/careers/jobdetails/124256",
			wantCompany: "Keka",
		},
	}

	for _, tt := range tests {
		t.Run(tt.wantCompany, func(t *testing.T) {
			pages := fakePages{titles: map[string]string{tt.url: "Test Job Title"}}
			_, existed, company, _, err := InsertManualJob(ctx, st, tt.url, "", pages)
			if err != nil {
				t.Fatalf("InsertManualJob: %v", err)
			}
			if existed {
				t.Errorf("existed = true, want false for a fresh URL")
			}
			if company != tt.wantCompany {
				t.Errorf("company = %q, want %q", company, tt.wantCompany)
			}
		})
	}
}
