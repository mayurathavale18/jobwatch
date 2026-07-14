package store

import (
	"context"
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
