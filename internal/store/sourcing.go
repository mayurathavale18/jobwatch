package store

import (
	"context"
	"database/sql"
	"jobwatch/internal/providers"
	"time"
)

// RefreshJobTx updates source facts while preserving candidate actions and manual overrides.
func (s *Store) RefreshJobTx(ctx context.Context, tx *sql.Tx, job providers.Job) error {
	var raw any
	if job.Raw != nil {
		raw = string(job.Raw)
	}
	_, err := tx.ExecContext(ctx, `UPDATE jobs SET title=?, location=?, url=?, raw=COALESCE(?,raw), last_seen_at=?
        WHERE provider=? AND company_slug=? AND external_id=?`,
		job.Title, job.Location, job.URL, raw, time.Now().UTC().Format(time.RFC3339),
		job.Provider, job.CompanySlug, job.ExternalID)
	return err
}

func (s *Store) RecordSourceScan(ctx context.Context, runID int64, provider, slug, scope string, fetched int, scanError string) error {
	status := "success"
	if scanError != "" {
		status = "failed"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO source_scans
        (poll_run_id, provider, company_slug, finished_at, scope, fetched, status, error)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, runID, provider, slug, time.Now().UTC().Format(time.RFC3339), scope, fetched, status, scanError)
	return err
}

// LinkSimilarJobsTx retains distinct requisitions and records similarity for human review.
func (s *Store) LinkSimilarJobsTx(ctx context.Context, tx *sql.Tx, id int64, job providers.Job, since time.Time) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO job_relations (job_id, related_job_id, evidence)
        SELECT ?, id, 'same_company_title_location'
        FROM jobs WHERE id < ? AND lower(trim(company_name))=lower(trim(?))
        AND lower(trim(title))=lower(trim(?)) AND lower(trim(location))=lower(trim(?))
        AND first_seen_at >= ?`, id, id, job.CompanyName, job.Title, job.Location, since.UTC().Format(time.RFC3339))
	return err
}
