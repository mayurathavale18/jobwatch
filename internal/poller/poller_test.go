package poller

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

// fakeProvider returns a fixed job list or error per company slug.
type fakeProvider struct {
	jobs map[string][]providers.Job
	errs map[string]error
}

func (f *fakeProvider) Fetch(ctx context.Context, company config.Company) ([]providers.Job, error) {
	if err, ok := f.errs[company.Slug]; ok {
		return nil, err
	}
	return f.jobs[company.Slug], nil
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent [][]providers.Job
}

func (f *fakeNotifier) NotifyJobs(ctx context.Context, jobs []providers.Job) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, jobs)
	return nil
}

func newTestPoller(t *testing.T, fp *fakeProvider, notifier Notifier) (*Poller, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	cfg := &config.Config{
		Companies: []config.Company{
			{Name: "Stripe", Provider: "greenhouse", Slug: "stripe"},
			{Name: "Razorpay", Provider: "lever", Slug: "razorpay"},
		},
		Filters: config.Filters{
			IncludeKeywords:  []string{"backend"},
			ExcludeKeywords:  []string{"staff"},
			LocationsInclude: []string{},
		},
	}

	p := New(st, cfg, notifier)
	p.ProviderFor = func(name string) providers.Provider { return fp }
	return p, st
}

func job(slug, id, title string) providers.Job {
	return providers.Job{
		Provider:    "greenhouse",
		CompanySlug: slug,
		CompanyName: slug,
		ExternalID:  id,
		Title:       title,
		Location:    "Remote",
		URL:         "https://example.com/" + id,
		FirstSeenAt: time.Now().UTC(),
		Raw:         []byte(`{}`),
	}
}

func TestRunInsertsNewJobsAndNotifies(t *testing.T) {
	fp := &fakeProvider{
		jobs: map[string][]providers.Job{
			"stripe":   {job("stripe", "1", "Backend Engineer")},
			"razorpay": {job("razorpay", "2", "Staff Backend Engineer")}, // excluded
		},
	}
	notifier := &fakeNotifier{}
	p, st := newTestPoller(t, fp, notifier)

	result, err := p.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.CompaniesOK != 2 || result.CompaniesFailed != 0 {
		t.Errorf("result = %+v", result)
	}
	if result.NewJobs != 2 {
		t.Errorf("NewJobs = %d, want 2", result.NewJobs)
	}

	notifier.mu.Lock()
	totalNotified := 0
	for _, batch := range notifier.sent {
		totalNotified += len(batch)
	}
	notifier.mu.Unlock()
	if totalNotified != 1 {
		t.Errorf("expected 1 job notified (the one passing filters), got %d", totalNotified)
	}

	rows, err := st.ListJobs(context.Background(), store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 jobs stored, got %d", len(rows))
	}
}

func TestRunSecondPollProducesZeroNewJobs(t *testing.T) {
	fp := &fakeProvider{
		jobs: map[string][]providers.Job{
			"stripe":   {job("stripe", "1", "Backend Engineer")},
			"razorpay": {job("razorpay", "2", "Backend Engineer")},
		},
	}
	notifier := &fakeNotifier{}
	p, _ := newTestPoller(t, fp, notifier)

	if _, err := p.Run(context.Background(), false); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	result, err := p.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if result.NewJobs != 0 {
		t.Errorf("second run NewJobs = %d, want 0 (dedupe should suppress)", result.NewJobs)
	}
}

func TestRunBackfillSkipsNotifications(t *testing.T) {
	fp := &fakeProvider{
		jobs: map[string][]providers.Job{
			"stripe":   {job("stripe", "1", "Backend Engineer")},
			"razorpay": {job("razorpay", "2", "Backend Engineer")},
		},
	}
	notifier := &fakeNotifier{}
	p, st := newTestPoller(t, fp, notifier)

	result, err := p.Run(context.Background(), true)
	if err != nil {
		t.Fatalf("Run backfill: %v", err)
	}
	if result.NewJobs != 2 {
		t.Errorf("NewJobs = %d, want 2", result.NewJobs)
	}

	notifier.mu.Lock()
	n := len(notifier.sent)
	notifier.mu.Unlock()
	if n != 0 {
		t.Errorf("expected no notifications during backfill, got %d batches", n)
	}

	rows, err := st.ListJobs(context.Background(), store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 jobs stored by backfill, got %d", len(rows))
	}
}

func TestRunFuzzyDedupSuppressesSamePostingFromDifferentProvider(t *testing.T) {
	// Same real posting, discovered via two different providers with
	// unrelated external IDs -- exact (provider, company_slug, external_id)
	// dedup can't catch this, but ExistsFuzzyTx should.
	fp := &fakeProvider{
		jobs: map[string][]providers.Job{
			"stripe": {{
				Provider:    "greenhouse",
				CompanySlug: "stripe",
				CompanyName: "Acme",
				ExternalID:  "1",
				Title:       "Backend Engineer",
				Location:    "Remote",
				URL:         "https://boards.greenhouse.io/acme/1",
				FirstSeenAt: time.Now().UTC(),
				Raw:         []byte(`{}`),
			}},
			"razorpay": {{
				Provider:    "lever",
				CompanySlug: "acme-via-aggregator",
				CompanyName: "Acme",
				ExternalID:  "unrelated-id-2",
				Title:       "Backend Engineer",
				Location:    "Remote",
				URL:         "https://remoteok.com/remote-jobs/acme-2",
				FirstSeenAt: time.Now().UTC(),
				Raw:         []byte(`{}`),
			}},
		},
	}
	notifier := &fakeNotifier{}
	p, st := newTestPoller(t, fp, notifier)

	result, err := p.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.NewJobs != 1 {
		t.Errorf("NewJobs = %d, want 1 (second is a fuzzy-dedup match of the first)", result.NewJobs)
	}

	rows, err := st.ListJobs(context.Background(), store.JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 job stored, got %d", len(rows))
	}
}

func TestRunContinuesAfterCompanyFailure(t *testing.T) {
	fp := &fakeProvider{
		jobs: map[string][]providers.Job{
			"stripe": {job("stripe", "1", "Backend Engineer")},
		},
		errs: map[string]error{
			"razorpay": errors.New("connection refused"),
		},
	}
	notifier := &fakeNotifier{}
	p, _ := newTestPoller(t, fp, notifier)

	result, err := p.Run(context.Background(), false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.CompaniesOK != 1 || result.CompaniesFailed != 1 {
		t.Errorf("result = %+v", result)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("expected 1 error, got %v", result.Errors)
	}
}
