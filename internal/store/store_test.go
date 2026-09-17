package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"jobwatch/internal/providers"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func sampleJob() providers.Job {
	return providers.Job{
		Provider:    "greenhouse",
		CompanySlug: "stripe",
		CompanyName: "Stripe",
		ExternalID:  "12345",
		Title:       "Backend Engineer",
		Location:    "Remote",
		URL:         "https://stripe.com/jobs/12345",
		FirstSeenAt: time.Now().UTC(),
		Raw:         []byte(`{"id":12345}`),
	}
}

func TestInsertAndDedupe(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	job := sampleJob()

	exists, err := s.Exists(ctx, job.Provider, job.CompanySlug, job.ExternalID)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("expected job to not exist yet")
	}

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, job, StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	exists, err = s.Exists(ctx, job.Provider, job.CompanySlug, job.ExternalID)
	if err != nil {
		t.Fatalf("Exists after insert: %v", err)
	}
	if !exists {
		t.Fatal("expected job to exist after insert")
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Title != job.Title || got.Status != StatusNew {
		t.Errorf("GetJob = %+v", got)
	}
}

func TestInsertJobPersistsJDText(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	job := sampleJob()
	job.JDText = "We need a backend engineer with Go and Kubernetes experience."

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, job, StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ManualJDText != job.JDText {
		t.Errorf("ManualJDText = %q, want %q", got.ManualJDText, job.JDText)
	}
}

func TestInsertJobPersistsOutreachInstruction(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	job := sampleJob()
	job.OutreachInstruction = "Founder's email is jane@acme.com, mention our Kubernetes work."

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, job, StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ManualOutreachInstruction != job.OutreachInstruction {
		t.Errorf("ManualOutreachInstruction = %q, want %q", got.ManualOutreachInstruction, job.OutreachInstruction)
	}
}

func TestMigrateAddsManualJDTextColumnToExistingDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	// Simulate a DB created before manual_jd_text existed -- the original
	// schema, no ALTER TABLE, no manual_jd_text column at all.
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := raw.Exec(`
		CREATE TABLE jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			provider TEXT NOT NULL,
			company_slug TEXT NOT NULL,
			company_name TEXT NOT NULL,
			external_id TEXT NOT NULL,
			title TEXT NOT NULL,
			location TEXT NOT NULL DEFAULT '',
			url TEXT NOT NULL DEFAULT '',
			posted_at TEXT NULL,
			first_seen_at TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'new',
			notes TEXT NOT NULL DEFAULT '',
			raw JSON,
			UNIQUE(provider, company_slug, external_id)
		)`); err != nil {
		t.Fatalf("creating pre-migration schema: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("closing raw db: %v", err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open on pre-migration DB: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, sampleJob(), StatusNew)
	if err != nil {
		t.Fatalf("InsertJob after migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ManualJDText != "" {
		t.Errorf("ManualJDText = %q, want empty default for a job inserted with JDText unset", got.ManualJDText)
	}
}

func TestUpdateStatusRejectsInvalid(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tx, _ := s.BeginTx(ctx)
	id, _ := s.InsertJob(ctx, tx, sampleJob(), StatusNew)
	tx.Commit()

	if err := s.UpdateStatus(ctx, id, "bogus-status"); err == nil {
		t.Fatal("expected error for invalid status")
	}

	if err := s.UpdateStatus(ctx, id, StatusApplied); err != nil {
		t.Fatalf("UpdateStatus valid: %v", err)
	}

	got, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.Status != StatusApplied {
		t.Errorf("Status = %q, want applied", got.Status)
	}
}

func TestListJobsFilters(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	j1 := sampleJob()
	j1.ExternalID = "1"
	j1.Title = "Backend Engineer"
	j1.CompanyName = "Stripe"

	j2 := sampleJob()
	j2.ExternalID = "2"
	j2.Title = "Frontend Engineer"
	j2.CompanyName = "Razorpay"

	for _, j := range []providers.Job{j1, j2} {
		tx, _ := s.BeginTx(ctx)
		if _, err := s.InsertJob(ctx, tx, j, StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	all, err := s.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(all))
	}

	byCompany, err := s.ListJobs(ctx, JobFilter{Company: "Stripe"})
	if err != nil {
		t.Fatalf("ListJobs by company: %v", err)
	}
	if len(byCompany) != 1 || byCompany[0].CompanyName != "Stripe" {
		t.Errorf("byCompany = %+v", byCompany)
	}

	bySearch, err := s.ListJobs(ctx, JobFilter{Search: "Frontend"})
	if err != nil {
		t.Fatalf("ListJobs by search: %v", err)
	}
	if len(bySearch) != 1 || bySearch[0].Title != "Frontend Engineer" {
		t.Errorf("bySearch = %+v", bySearch)
	}
}

func TestListJobsFiltersProviderStatusesSinceAndBroadSearch(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	old := sampleJob()
	old.ExternalID = "old"
	old.Provider = "web3career"
	old.CompanyName = "Old Chain"
	old.Location = "Bengaluru, India"
	old.FirstSeenAt = time.Now().UTC().Add(-10 * 24 * time.Hour)

	recentGreenhouse := sampleJob()
	recentGreenhouse.ExternalID = "recent-gh"
	recentGreenhouse.Provider = "greenhouse"
	recentGreenhouse.CompanyName = "Brex"
	recentGreenhouse.Location = "Remote"
	recentGreenhouse.FirstSeenAt = time.Now().UTC()

	recentWeb3 := sampleJob()
	recentWeb3.ExternalID = "recent-w3"
	recentWeb3.Provider = "web3career"
	recentWeb3.CompanyName = "New Chain"
	recentWeb3.Location = "Remote"
	recentWeb3.FirstSeenAt = time.Now().UTC()

	for i, j := range []providers.Job{old, recentGreenhouse, recentWeb3} {
		tx, _ := s.BeginTx(ctx)
		status := StatusNew
		if i == 1 {
			status = StatusShortlisted
		}
		if _, err := s.InsertJob(ctx, tx, j, status); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	byProvider, err := s.ListJobs(ctx, JobFilter{Provider: "web3career"})
	if err != nil {
		t.Fatalf("ListJobs by provider: %v", err)
	}
	if len(byProvider) != 2 {
		t.Fatalf("byProvider len = %d, want 2", len(byProvider))
	}

	byStatuses, err := s.ListJobs(ctx, JobFilter{Statuses: []string{string(StatusNew), string(StatusShortlisted)}})
	if err != nil {
		t.Fatalf("ListJobs by statuses: %v", err)
	}
	if len(byStatuses) != 3 {
		t.Fatalf("byStatuses len = %d, want 3 (all seeded jobs are new or shortlisted)", len(byStatuses))
	}

	since := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	bySince, err := s.ListJobs(ctx, JobFilter{Since: since})
	if err != nil {
		t.Fatalf("ListJobs by since: %v", err)
	}
	if len(bySince) != 2 {
		t.Fatalf("bySince len = %d, want 2 (excludes the 10-day-old job)", len(bySince))
	}

	byLocationSearch, err := s.ListJobs(ctx, JobFilter{Search: "Bengaluru"})
	if err != nil {
		t.Fatalf("ListJobs by location search: %v", err)
	}
	if len(byLocationSearch) != 1 || byLocationSearch[0].CompanyName != "Old Chain" {
		t.Errorf("byLocationSearch = %+v, want the Bengaluru job matched via location text", byLocationSearch)
	}

	byCompanySearch, err := s.ListJobs(ctx, JobFilter{Search: "Brex"})
	if err != nil {
		t.Fatalf("ListJobs by company search: %v", err)
	}
	if len(byCompanySearch) != 1 || byCompanySearch[0].CompanyName != "Brex" {
		t.Errorf("byCompanySearch = %+v, want the Brex job matched via company_name text", byCompanySearch)
	}
}

func TestProviders(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	j1 := sampleJob()
	j1.ExternalID = "1"
	j1.Provider = "greenhouse"

	j2 := sampleJob()
	j2.ExternalID = "2"
	j2.Provider = "web3career"

	j3 := sampleJob()
	j3.ExternalID = "3"
	j3.Provider = "greenhouse"

	for _, j := range []providers.Job{j1, j2, j3} {
		tx, _ := s.BeginTx(ctx)
		if _, err := s.InsertJob(ctx, tx, j, StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	got, err := s.Providers(ctx)
	if err != nil {
		t.Fatalf("Providers: %v", err)
	}
	if len(got) != 2 || got[0] != "greenhouse" || got[1] != "web3career" {
		t.Errorf("Providers = %v, want [greenhouse web3career] (distinct, sorted)", got)
	}
}

func TestListJobsPaginationAndCount(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i := 0; i < 5; i++ {
		j := sampleJob()
		j.ExternalID = fmt.Sprintf("%d", i)
		j.CompanyName = "Stripe"
		tx, _ := s.BeginTx(ctx)
		if _, err := s.InsertJob(ctx, tx, j, StatusNew); err != nil {
			t.Fatalf("InsertJob: %v", err)
		}
		tx.Commit()
	}

	total, err := s.CountJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("CountJobs: %v", err)
	}
	if total != 5 {
		t.Fatalf("CountJobs = %d, want 5", total)
	}

	page1, err := s.ListJobs(ctx, JobFilter{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatalf("ListJobs page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}

	page3, err := s.ListJobs(ctx, JobFilter{Limit: 2, Offset: 4})
	if err != nil {
		t.Fatalf("ListJobs page3: %v", err)
	}
	if len(page3) != 1 {
		t.Fatalf("page3 len = %d, want 1", len(page3))
	}

	countFiltered, err := s.CountJobs(ctx, JobFilter{Company: "Stripe", Limit: 2})
	if err != nil {
		t.Fatalf("CountJobs filtered: %v", err)
	}
	if countFiltered != 5 {
		t.Fatalf("CountJobs filtered = %d, want 5 (Limit must not affect count)", countFiltered)
	}
}

func TestPollRunLifecycle(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	id, err := s.StartPollRun(ctx)
	if err != nil {
		t.Fatalf("StartPollRun: %v", err)
	}

	if err := s.FinishPollRun(ctx, id, 3, 1, 5, []string{"lever razorpay: timeout"}); err != nil {
		t.Fatalf("FinishPollRun: %v", err)
	}

	last, err := s.LastPollRun(ctx)
	if err != nil {
		t.Fatalf("LastPollRun: %v", err)
	}
	if last == nil {
		t.Fatal("expected a last poll run")
	}
	if last.NewJobs != 5 || last.CompaniesOK != 3 || last.CompaniesFailed != 1 {
		t.Errorf("last = %+v", last)
	}
}

func TestJobRowHasOutreachFieldsDefaultingEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, sampleJob(), StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.OutreachStatus != "" || job.FounderName != "" || job.FounderEmail != "" || job.OutreachDraftedAt != "" {
		t.Errorf("outreach fields = %+v, want all empty by default", job)
	}

	rows, err := s.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 || rows[0].OutreachStatus != "" {
		t.Errorf("ListJobs rows = %+v, want one row with empty OutreachStatus", rows)
	}
}

func TestMatchConnections(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	err := st.ReplaceConnections(ctx, []Connection{
		{FirstName: "A", Company: "Google"},
		{FirstName: "B", Company: "Stripe India"},
		{FirstName: "C", Company: ""},
	})
	if err != nil {
		t.Fatal(err)
	}
	for company, want := range map[string]int{"Google India": 1, "Stripe": 1, "Acme": 0, "": 0} {
		got, err := st.MatchConnections(ctx, company)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != want {
			t.Errorf("MatchConnections(%q) = %d, want %d", company, len(got), want)
		}
	}
	n, _ := st.CountConnections(ctx)
	if n != 3 {
		t.Errorf("CountConnections = %d, want 3", n)
	}
}
