// Package poller runs one polling cycle across all configured companies.
package poller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

const (
	maxConcurrency = 5
	// Workday's provider makes up to workdayMaxPages sequential requests
	// (~1s each) per company, well past what a single Greenhouse/Lever/Ashby
	// request needs -- sized for that instead of the single-request case.
	requestTimeout = 60 * time.Second
)

// Notifier is the subset of *notify.Telegram the poller needs, so tests can
// substitute a fake.
type Notifier interface {
	NotifyJobs(ctx context.Context, jobs []providers.Job) error
}

// Poller runs polling cycles against configured companies.
type Poller struct {
	Store    *store.Store
	Config   *config.Config
	Notifier Notifier // nil is allowed: notifications are skipped, a warning is logged

	// ProviderFor resolves a provider by name; overridable for tests.
	ProviderFor func(name string) providers.Provider
}

func New(st *store.Store, cfg *config.Config, notifier Notifier) *Poller {
	return &Poller{
		Store:       st,
		Config:      cfg,
		Notifier:    notifier,
		ProviderFor: providers.ForName,
	}
}

// Result summarizes the outcome of one polling cycle.
type Result struct {
	CompaniesOK     int
	CompaniesFailed int
	NewJobs         int
	Errors          []string
}

type companyOutcome struct {
	ok       bool
	errMsg   string
	inserted []providers.Job // jobs that passed filters (candidates for notification)
	newCount int
}

// Run polls every configured company concurrently (bounded to maxConcurrency
// in flight), stores newly discovered jobs, and — unless backfill is true —
// sends Telegram notifications for new jobs that pass the configured
// filters. Backfill mode inserts everything so future real polls won't
// re-notify, but never sends notifications.
func (p *Poller) Run(ctx context.Context, backfill bool) (Result, error) {
	runID, err := p.Store.StartPollRun(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("recording poll run start: %w", err)
	}

	sem := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	outcomes := make([]companyOutcome, len(p.Config.Companies))

	for i, company := range p.Config.Companies {
		wg.Add(1)
		go func(i int, company config.Company) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			outcomes[i] = p.pollCompany(ctx, company, backfill)
		}(i, company)
	}
	wg.Wait()

	var result Result
	var toNotify []providers.Job
	for i, o := range outcomes {
		company := p.Config.Companies[i]
		if o.ok {
			result.CompaniesOK++
		} else {
			result.CompaniesFailed++
			result.Errors = append(result.Errors, fmt.Sprintf("%s (%s/%s): %s", company.Name, company.Provider, company.Slug, o.errMsg))
			slog.Error("poll company failed", "company", company.Name, "provider", company.Provider, "slug", company.Slug, "error", o.errMsg)
		}
		result.NewJobs += o.newCount
		if !backfill {
			toNotify = append(toNotify, o.inserted...)
		}
	}

	if err := p.Store.FinishPollRun(ctx, runID, result.CompaniesOK, result.CompaniesFailed, result.NewJobs, result.Errors); err != nil {
		slog.Error("recording poll run finish", "error", err)
	}

	if !backfill && len(toNotify) > 0 {
		if p.Notifier == nil {
			slog.Warn("skipping notifications: no telegram notifier configured", "matched_jobs", len(toNotify))
		} else if err := p.Notifier.NotifyJobs(ctx, toNotify); err != nil {
			slog.Error("sending telegram notifications", "error", err)
			result.Errors = append(result.Errors, fmt.Sprintf("telegram: %s", err.Error()))
		}
	}

	return result, nil
}

func (p *Poller) pollCompany(ctx context.Context, company config.Company, backfill bool) companyOutcome {
	provider := p.ProviderFor(company.Provider)
	if provider == nil {
		return companyOutcome{ok: false, errMsg: fmt.Sprintf("unsupported provider %q", company.Provider)}
	}

	reqCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	jobs, err := provider.Fetch(reqCtx, company)
	if err != nil {
		return companyOutcome{ok: false, errMsg: err.Error()}
	}

	tx, err := p.Store.BeginTx(ctx)
	if err != nil {
		return companyOutcome{ok: false, errMsg: fmt.Sprintf("begin tx: %v", err)}
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	var inserted []providers.Job
	newCount := 0

	for _, job := range jobs {
		exists, err := p.Store.ExistsTx(ctx, tx, job.Provider, job.CompanySlug, job.ExternalID)
		if err != nil {
			slog.Error("checking dedupe", "company", company.Name, "external_id", job.ExternalID, "error", err)
			continue
		}
		if exists {
			continue
		}

		status := store.StatusIgnored
		passes := Passes(job, p.Config.Filters)
		if passes {
			status = store.StatusNew
		}

		id, err := p.Store.InsertJob(ctx, tx, job, status)
		if err != nil {
			slog.Error("inserting job", "company", company.Name, "external_id", job.ExternalID, "error", err)
			continue
		}

		newCount++
		if passes && !backfill {
			job.ID = id
			inserted = append(inserted, job)
		}
	}

	if err := tx.Commit(); err != nil {
		return companyOutcome{ok: false, errMsg: fmt.Sprintf("commit tx: %v", err)}
	}

	return companyOutcome{ok: true, inserted: inserted, newCount: newCount}
}
