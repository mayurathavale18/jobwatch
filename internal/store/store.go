// Package store persists jobs and poll run history in SQLite.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"jobwatch/internal/providers"
)

const (
	StatusNew         = "new"
	StatusIgnored     = "ignored"
	StatusShortlisted = "shortlisted"
	StatusApplied     = "applied"
	StatusRejected    = "rejected"
	StatusInterview   = "interview"
	StatusOffer       = "offer"
)

// ValidStatuses lists every status the dashboard may set on a job.
var ValidStatuses = []string{
	StatusNew, StatusIgnored, StatusShortlisted, StatusApplied,
	StatusRejected, StatusInterview, StatusOffer,
}

func IsValidStatus(s string) bool {
	return slices.Contains(ValidStatuses, s)
}

type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path, enables
// WAL mode, and runs migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("opening db %s: %w", path, err)
	}

	// modernc.org/sqlite has no real connection pool benefit and concurrent
	// writers just contend on the same file; keep it simple and serialized.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("enabling foreign keys: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrating: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS jobs (
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
	manual_jd_text TEXT NOT NULL DEFAULT '',
	UNIQUE(provider, company_slug, external_id)
);

CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_company ON jobs(company_slug);
CREATE INDEX IF NOT EXISTS idx_jobs_first_seen ON jobs(first_seen_at);

CREATE TABLE IF NOT EXISTS poll_runs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	finished_at TEXT NULL,
	companies_ok INTEGER NOT NULL DEFAULT 0,
	companies_failed INTEGER NOT NULL DEFAULT 0,
	new_jobs INTEGER NOT NULL DEFAULT 0,
	errors TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS kv (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	// Add the column for existing DBs (created before manual_jd_text existed)
	// Try to add the column; ignore "duplicate column" errors from pre-existing DBs that already have it
	_, _ = s.db.Exec(`ALTER TABLE jobs ADD COLUMN manual_jd_text TEXT NOT NULL DEFAULT ''`)
	return nil
}

// Exists reports whether a job with the given dedupe key is already stored.
func (s *Store) Exists(ctx context.Context, provider, companySlug, externalID string) (bool, error) {
	return existsQuerier(ctx, s.db, provider, companySlug, externalID)
}

// ExistsTx is Exists scoped to an open transaction. Callers that already
// hold the store's single connection via a transaction (see BeginTx) must
// use this instead of Exists, since the connection pool is limited to one
// connection and Exists would otherwise deadlock waiting for a connection
// the caller's own transaction is holding.
func (s *Store) ExistsTx(ctx context.Context, tx *sql.Tx, provider, companySlug, externalID string) (bool, error) {
	return existsQuerier(ctx, tx, provider, companySlug, externalID)
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func existsQuerier(ctx context.Context, q querier, provider, companySlug, externalID string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx,
		`SELECT 1 FROM jobs WHERE provider = ? AND company_slug = ? AND external_id = ? LIMIT 1`,
		provider, companySlug, externalID,
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ExistsFuzzyTx reports whether a job with the same normalized
// company/title/location already exists (from any provider) with
// first_seen_at at or after since. This catches the same real posting
// being discovered twice -- once via a company's own ATS board, once via
// a search aggregator (RemoteOK, We Work Remotely) indexing it -- which
// the exact (provider, company_slug, external_id) key in Exists/ExistsTx
// can't, since the two providers assign unrelated external IDs to the same
// job.
func (s *Store) ExistsFuzzyTx(ctx context.Context, tx *sql.Tx, companyName, title, location string, since time.Time) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `
		SELECT 1 FROM jobs
		WHERE LOWER(TRIM(company_name)) = LOWER(TRIM(?))
		  AND LOWER(TRIM(title)) = LOWER(TRIM(?))
		  AND LOWER(TRIM(location)) = LOWER(TRIM(?))
		  AND first_seen_at >= ?
		LIMIT 1`,
		companyName, title, location, since.UTC().Format(time.RFC3339),
	).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// InsertJob inserts a new job row with the given status. It must only be
// called for jobs that Exists reported false for (dedupe is enforced by the
// caller, not by relying on the UNIQUE constraint, so we can distinguish
// "new job" from "no-op" cleanly in the poller).
func (s *Store) InsertJob(ctx context.Context, tx *sql.Tx, job providers.Job, status string) (int64, error) {
	var postedAt any
	if job.PostedAt != nil {
		postedAt = job.PostedAt.UTC().Format(time.RFC3339)
	}

	raw := job.Raw
	if raw == nil {
		raw = json.RawMessage("null")
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO jobs (provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, raw, manual_jd_text)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.Provider, job.CompanySlug, job.CompanyName, job.ExternalID,
		job.Title, job.Location, job.URL, postedAt,
		job.FirstSeenAt.UTC().Format(time.RFC3339), status, string(raw), job.JDText,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BeginTx starts a transaction for the caller to batch writes in.
func (s *Store) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return s.db.BeginTx(ctx, nil)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// UpdateStatus sets a job's status.
func (s *Store) UpdateStatus(ctx context.Context, id int64, status string) error {
	return updateStatusExecer(ctx, s.db, id, status)
}

// UpdateStatusTx is UpdateStatus scoped to an open transaction (see ExistsTx
// for why this is needed instead of UpdateStatus while a tx is open).
func (s *Store) UpdateStatusTx(ctx context.Context, tx *sql.Tx, id int64, status string) error {
	return updateStatusExecer(ctx, tx, id, status)
}

func updateStatusExecer(ctx context.Context, e execer, id int64, status string) error {
	if !IsValidStatus(status) {
		return fmt.Errorf("invalid status %q", status)
	}
	_, err := e.ExecContext(ctx, `UPDATE jobs SET status = ? WHERE id = ?`, status, id)
	return err
}

// UpdateNotes sets a job's free-text notes.
func (s *Store) UpdateNotes(ctx context.Context, id int64, notes string) error {
	return updateNotesExecer(ctx, s.db, id, notes)
}

// UpdateNotesTx is UpdateNotes scoped to an open transaction.
func (s *Store) UpdateNotesTx(ctx context.Context, tx *sql.Tx, id int64, notes string) error {
	return updateNotesExecer(ctx, tx, id, notes)
}

func updateNotesExecer(ctx context.Context, e execer, id int64, notes string) error {
	_, err := e.ExecContext(ctx, `UPDATE jobs SET notes = ? WHERE id = ?`, notes, id)
	return err
}

// JobRow is a job as read back from the database for display.
type JobRow struct {
	ID           int64
	Provider     string
	CompanySlug  string
	CompanyName  string
	ExternalID   string
	Title        string
	Location     string
	URL          string
	PostedAt     sql.NullString
	FirstSeenAt  string
	Status       string
	Notes        string
	ManualJDText string
}

// JobFilter narrows ListJobs results.
type JobFilter struct {
	Status  string // exact match, empty = any
	Company string // exact match on company_name, empty = any
	Search  string // substring match on title, empty = any
}

func (s *Store) ListJobs(ctx context.Context, f JobFilter) ([]JobRow, error) {
	query := `SELECT id, provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, notes, manual_jd_text FROM jobs WHERE 1=1`
	var args []any

	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Company != "" {
		query += ` AND company_name = ?`
		args = append(args, f.Company)
	}
	if f.Search != "" {
		query += ` AND title LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(f.Search)+"%")
	}
	query += ` ORDER BY first_seen_at DESC, id DESC`

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []JobRow
	for rows.Next() {
		var j JobRow
		if err := rows.Scan(&j.ID, &j.Provider, &j.CompanySlug, &j.CompanyName, &j.ExternalID,
			&j.Title, &j.Location, &j.URL, &j.PostedAt, &j.FirstSeenAt, &j.Status, &j.Notes, &j.ManualJDText); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

// GetJob returns a single job by id.
func (s *Store) GetJob(ctx context.Context, id int64) (JobRow, error) {
	return getJobQuerier(ctx, s.db, id)
}

// GetJobTx is GetJob scoped to an open transaction.
func (s *Store) GetJobTx(ctx context.Context, tx *sql.Tx, id int64) (JobRow, error) {
	return getJobQuerier(ctx, tx, id)
}

func getJobQuerier(ctx context.Context, q querier, id int64) (JobRow, error) {
	var j JobRow
	err := q.QueryRowContext(ctx,
		`SELECT id, provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, notes, manual_jd_text FROM jobs WHERE id = ?`,
		id,
	).Scan(&j.ID, &j.Provider, &j.CompanySlug, &j.CompanyName, &j.ExternalID,
		&j.Title, &j.Location, &j.URL, &j.PostedAt, &j.FirstSeenAt, &j.Status, &j.Notes, &j.ManualJDText)
	return j, err
}

// StatusCounts returns the number of jobs per status.
func (s *Store) StatusCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT status, COUNT(*) FROM jobs GROUP BY status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			return nil, err
		}
		counts[status] = n
	}
	return counts, rows.Err()
}

// CompanyNames returns the distinct set of company names present in jobs,
// for populating the dashboard filter bar.
func (s *Store) CompanyNames(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT company_name FROM jobs ORDER BY company_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// PollRun records one polling cycle.
type PollRun struct {
	ID              int64
	StartedAt       string
	FinishedAt      sql.NullString
	CompaniesOK     int
	CompaniesFailed int
	NewJobs         int
	Errors          string
}

// StartPollRun records the start of a polling cycle and returns its ID.
func (s *Store) StartPollRun(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO poll_runs (started_at, companies_ok, companies_failed, new_jobs, errors) VALUES (?, 0, 0, 0, '')`,
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishPollRun records the outcome of a polling cycle.
func (s *Store) FinishPollRun(ctx context.Context, id int64, companiesOK, companiesFailed, newJobs int, errs []string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE poll_runs SET finished_at = ?, companies_ok = ?, companies_failed = ?, new_jobs = ?, errors = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339), companiesOK, companiesFailed, newJobs, strings.Join(errs, "; "), id,
	)
	return err
}

// LastPollRun returns the most recently finished poll run, if any.
func (s *Store) LastPollRun(ctx context.Context) (*PollRun, error) {
	var r PollRun
	err := s.db.QueryRowContext(ctx,
		`SELECT id, started_at, finished_at, companies_ok, companies_failed, new_jobs, errors
		 FROM poll_runs WHERE finished_at IS NOT NULL ORDER BY id DESC LIMIT 1`,
	).Scan(&r.ID, &r.StartedAt, &r.FinishedAt, &r.CompaniesOK, &r.CompaniesFailed, &r.NewJobs, &r.Errors)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetKV returns the value for key and whether it was present.
func (s *Store) GetKV(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return value, true, nil
}

// SetKV upserts a key/value pair.
func (s *Store) SetKV(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}
