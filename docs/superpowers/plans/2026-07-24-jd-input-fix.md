# Mobile JD-Input Fix + JD Text/File Ingestion Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix the dashboard's silent mobile submit failure on the manual job-link form, and let the user supply JD text directly (paste or `.txt`/`.md` file upload) so `tailor_resume.py` uses it verbatim instead of attempting an unreliable scrape.

**Architecture:** A new `manual_jd_text` column on the `jobs` table carries user-supplied JD text end-to-end: frontend form → `POST /api/jobs/manual` → `jobsubmit.InsertManualJob` → `store.InsertJob`. `tailor_resume.py`'s `process_job`/`rebuild_one` read it back (already re-reading the full job row) and use it in place of a scrape when present. Separately, the frontend form drops reliance on native HTML5 `type="url"`/`required` validation (the actual cause of the silent mobile failure) in favor of explicit JS validation that always surfaces a result.

**Tech Stack:** Go (`internal/store`, `internal/jobsubmit`, `internal/web`), Python (`scripts/tailor_resume.py`), TypeScript/React (`frontend/src`).

## Global Constraints

- The job URL stays required — JD text is an optional supplement, never a replacement for URL-based identity/dedupe/the Telegram "Apply" link.
- `.txt`/`.md` only for file upload, read client-side via `FileReader.readAsText`. No PDF, no server-side file parsing.
- Greenhouse JD fetch keeps priority over user-supplied JD text (spec: "no change to the Greenhouse path... user-supplied text only kicks in for the 'everything else' branch"). Order: Greenhouse fetch → `manual_jd_text` (if present) → `fetch_jd_generic` scrape (process_job only; rebuild_one has never called this) → title-only.
- The Telegram bare-URL-forward path (`tgsync.go`'s `addManualJob`) has no way to supply JD text — always passes an empty string through; out of scope to add one.
- `go test ./...` and `gofmt -l .` must stay clean; `pytest scripts/test_tailor_resume.py -v` must stay clean except the one pre-existing unrelated failure (`test_process_job_skips_non_engineering_role`, missing `DB_PATH` mock — predates all work in this repo's recent branches, not this plan's responsibility).
- No test harness exists in `frontend/` (no Vitest/RTL configured) — frontend changes are verified manually via browser, not automated tests.

Spec: `docs/superpowers/specs/2026-07-24-jd-input-fix-design.md`

---

## File Structure

- **Modify `internal/providers/provider.go`**: `Job` struct gains `JDText string`.
- **Modify `internal/store/store.go`**: `migrate()`'s schema gains a `manual_jd_text` column (in `CREATE TABLE` for fresh DBs, plus an idempotent `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` for existing ones); `InsertJob` persists `job.JDText` into it; `JobRow` gains `ManualJDText string`, populated by `ListJobs`/`GetJob`/`GetJobTx`'s `getJobQuerier`.
- **Modify `internal/store/store_test.go`**: new tests for JDText persistence and for the migration succeeding against a pre-existing (pre-migration) DB.
- **Modify `internal/jobsubmit/jobsubmit.go`**: `InsertManualJob` gains a `jdText string` parameter, threaded into the `providers.Job{}` literal's new `JDText` field.
- **Modify `internal/jobsubmit/jobsubmit_test.go`**: update the 4 existing `InsertManualJob` call sites for the new parameter; add one new persistence test.
- **Modify `internal/tgsync/tgsync.go`**: `addManualJob`'s call to `InsertManualJob` passes `""` for the new parameter (Telegram has no JD-text source).
- **Modify `internal/web/api.go`**: `apiManualJobRequest` gains `JDText string \`json:"jdText"\``; `handleAPIJobsManual` trims it and passes it to `InsertManualJob`.
- **Modify `internal/web/api_test.go`**: new test asserting `jdText` flows from the HTTP request through to the persisted job row.
- **Modify `scripts/tailor_resume.py`**: `process_job()` and `rebuild_one()` both check `job.get("manual_jd_text")` before falling through to a scrape/title-only.
- **Modify `scripts/test_tailor_resume.py`**: new tests for both functions' `manual_jd_text` handling.
- **Modify `frontend/src/pages/Jobs.tsx`**: `noValidate` on the form, JS-side URL validation (trim + http(s) prefix check) replacing native constraint validation, new JD-text textarea + `.txt`/`.md` file input.
- **Modify `frontend/src/api.ts`**: `submitManualJob(url, jdText?)`.
- **Modify `frontend/src/index.css`**: new `.add-job-jd` styling; `.add-job` restructured as the top row of a two-row form.

---

### Task 1: Store layer — `manual_jd_text` column + migration + `JobRow` (Go)

**Files:**
- Modify: `internal/providers/provider.go`
- Modify: `internal/store/store.go`
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `providers.Job.JDText string` (new field); `store.JobRow.ManualJDText string` (new field, populated by `ListJobs`/`GetJob`/`GetJobTx`); `Store.InsertJob` now persists `job.JDText` into the `jobs.manual_jd_text` column.

- [ ] **Step 1: Write the failing tests**

Add `"database/sql"` to `internal/store/store_test.go`'s imports (needed for the migration test's raw `sql.Open` — the `modernc.org/sqlite` driver is already registered package-wide via `store.go`'s blank import, no need to re-import it here):

```go
import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"jobwatch/internal/providers"
)
```

Add these two tests to `internal/store/store_test.go` (after `TestInsertAndDedupe`):

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/store/... -run "TestInsertJobPersistsJDText|TestMigrateAddsManualJDTextColumnToExistingDB" -v`
Expected: FAIL — `job.JDText undefined`, `got.ManualJDText undefined` (compile errors, since neither field exists yet).

- [ ] **Step 3: Implement**

In `internal/providers/provider.go`, add the new field to `Job` (after `Raw`):

```go
type Job struct {
	// ID is the database row id. It is zero for freshly-fetched jobs and
	// populated by the poller after insert, so it can be embedded in
	// outgoing Telegram notifications for the #J{id} reply-tag.
	ID          int64
	Provider    string
	CompanySlug string
	CompanyName string
	ExternalID  string
	Title       string
	Location    string
	URL         string
	PostedAt    *time.Time
	FirstSeenAt time.Time
	Raw         json.RawMessage
	// JDText is user-supplied JD text (pasted or from an uploaded file)
	// from the dashboard's manual-submit form. Empty for every polled
	// provider -- only jobsubmit.InsertManualJob ever sets it.
	JDText string
}
```

In `internal/store/store.go`, replace the `migrate()` schema constant's `jobs` table definition and add the idempotent `ALTER TABLE` (the full constant, unchanged aside from the noted additions):

```go
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

ALTER TABLE jobs ADD COLUMN IF NOT EXISTS manual_jd_text TEXT NOT NULL DEFAULT '';
`
	_, err := s.db.Exec(schema)
	return err
}
```

(The column is in both the `CREATE TABLE` — for fresh installs — and the trailing `ALTER TABLE ... IF NOT EXISTS` — for existing DBs, and harmlessly a no-op on fresh ones where it's already there. `modernc.org/sqlite` is v1.53.0, well past the SQLite 3.35.0 baseline that added `ADD COLUMN IF NOT EXISTS` support, so no manual duplicate-column-error handling is needed.)

Update `InsertJob` to persist `job.JDText`:

```go
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
```

Update `JobRow` (add the new field):

```go
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
```

Update `ListJobs`'s query and scan:

```go
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
```

Update `getJobQuerier` (used by both `GetJob` and `GetJobTx`):

```go
func getJobQuerier(ctx context.Context, q querier, id int64) (JobRow, error) {
	var j JobRow
	err := q.QueryRowContext(ctx,
		`SELECT id, provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, notes, manual_jd_text FROM jobs WHERE id = ?`,
		id,
	).Scan(&j.ID, &j.Provider, &j.CompanySlug, &j.CompanyName, &j.ExternalID,
		&j.Title, &j.Location, &j.URL, &j.PostedAt, &j.FirstSeenAt, &j.Status, &j.Notes, &j.ManualJDText)
	return j, err
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/... -v`
Expected: PASS (all tests, including the pre-existing suite)

- [ ] **Step 5: Run full Go suite + formatting check**

Run: `go test ./... && gofmt -l .`
Expected: all packages PASS; `gofmt -l .` prints nothing. Both new fields (`providers.Job.JDText`, `store.JobRow.ManualJDText`) are purely additive — no positional (unnamed-field) struct literals of either type exist anywhere in the repo, so nothing else needs to change for the whole build to stay green after this task alone.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/provider.go internal/store/store.go internal/store/store_test.go
git commit -m "Add manual_jd_text column, JobRow field, and providers.Job.JDText"
```

---

### Task 2: `jobsubmit.InsertManualJob` + call sites (Go)

**Files:**
- Modify: `internal/jobsubmit/jobsubmit.go`
- Modify: `internal/jobsubmit/jobsubmit_test.go`
- Modify: `internal/tgsync/tgsync.go`

**Interfaces:**
- Consumes: `providers.Job.JDText`, `store.JobRow.ManualJDText` (Task 1).
- Produces: `InsertManualJob(ctx, st, rawURL, jdText string, pages) (id, alreadyExisted, company, title, err)` — new `jdText` parameter inserted after `rawURL`, before `pages`.

- [ ] **Step 1: Write the failing tests**

Update all 4 existing call sites in `internal/jobsubmit/jobsubmit_test.go` to pass `""` as the new `jdText` argument:

```go
	id, existed, company, title, err := InsertManualJob(ctx, st, jobURL, "", pages)
```
(in `TestInsertManualJobInsertsNewJob`)

```go
	id1, _, _, _, err := InsertManualJob(ctx, st, jobURL, "", pages)
	...
	id2, existed, _, _, err := InsertManualJob(ctx, st, jobURL, "", pages)
```
(in `TestInsertManualJobDuplicateURLIsNoOp`)

```go
	id, existed, _, title, err := InsertManualJob(ctx, st, jobURL, "", fakePages{err: context.DeadlineExceeded})
```
(in `TestInsertManualJobTitleFetchFailsStillInserts`)

```go
			_, existed, company, _, err := InsertManualJob(ctx, st, tt.url, "", pages)
```
(in `TestInsertManualJobCompanyFromURLHandlesCcTLDs`, inside the `t.Run` closure)

Add one new test, after `TestInsertManualJobInsertsNewJob`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/jobsubmit/... -v`
Expected: FAIL — compile errors (`too few arguments in call to InsertManualJob`) since the signature hasn't changed yet.

- [ ] **Step 3: Implement**

In `internal/jobsubmit/jobsubmit.go`, update `InsertManualJob`'s signature and the `providers.Job{}` literal:

```go
// InsertManualJob inserts rawURL as a new "manual" provider job (status
// "new", so it rides the existing tailor-resume pipeline the same as any
// polled job) and reports whether it already existed. Re-submitting the
// same URL is a no-op (dedupe key includes the URL itself as external_id).
// jdText is optional user-supplied JD text (dashboard's "paste JD text"
// field or an uploaded .txt/.md file) -- stored verbatim so
// tailor_resume.py can use it instead of attempting its own scrape, which
// routinely fails for the exact sources this manual-submit path exists
// for (Workday, Naukri, etc). Empty for the Telegram bare-URL-forward
// path, which has no way to supply it.
func InsertManualJob(ctx context.Context, st *store.Store, rawURL string, jdText string, pages PageFetcher) (id int64, alreadyExisted bool, company string, title string, err error) {
	company = companyFromURL(rawURL)
	slug := manualSlug(rawURL)

	tx, err := st.BeginTx(ctx)
	if err != nil {
		return 0, false, "", "", err
	}
	defer tx.Rollback() //nolint:errcheck // no-op if already committed

	exists, err := st.ExistsTx(ctx, tx, "manual", slug, rawURL)
	if err != nil {
		return 0, false, "", "", err
	}
	if exists {
		existingID, gErr := existingJobIDTx(ctx, tx, "manual", slug, rawURL)
		if gErr != nil {
			return 0, false, "", "", gErr
		}
		return existingID, true, company, "", nil
	}

	fetchedTitle, ferr := pages.FetchTitle(ctx, rawURL)
	if ferr != nil {
		fetchedTitle = ""
	}
	if fetchedTitle == "" {
		fetchedTitle = "(title unknown — reply \"note: <real title>\" to fix)"
	}

	job := providers.Job{
		Provider:    "manual",
		CompanySlug: slug,
		CompanyName: company,
		ExternalID:  rawURL,
		Title:       fetchedTitle,
		URL:         rawURL,
		FirstSeenAt: time.Now().UTC(),
		Raw:         json.RawMessage(`{}`),
		JDText:      jdText,
	}

	newID, err := st.InsertJob(ctx, tx, job, store.StatusNew)
	if err != nil {
		return 0, false, "", "", err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, "", "", err
	}

	return newID, false, company, fetchedTitle, nil
}
```

In `internal/tgsync/tgsync.go`, update `addManualJob`'s call (Telegram has no JD-text source, passes `""`):

```go
func (s *Syncer) addManualJob(ctx context.Context, msg *notify.Message, rawURL string) error {
	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.Store, rawURL, "", s.Pages)
	if err != nil {
		return err
	}
	if existed {
		return s.TG.Reply(ctx, msg.MessageID, "Already added that one.")
	}
	return s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("✓ Added — %s — %s\n#J%d", company, title, id))
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/jobsubmit/... ./internal/tgsync/... -v`
Expected: PASS (all tests)

- [ ] **Step 5: Commit**

```bash
git add internal/jobsubmit/jobsubmit.go internal/jobsubmit/jobsubmit_test.go internal/tgsync/tgsync.go
git commit -m "Thread jdText through InsertManualJob; Telegram path passes empty"
```

---

### Task 3: Web API layer — `apiManualJobRequest.JDText` (Go)

**Files:**
- Modify: `internal/web/api.go`
- Test: `internal/web/api_test.go`

**Interfaces:**
- Consumes: `jobsubmit.InsertManualJob(ctx, st, rawURL, jdText, pages)` (Task 2).
- Produces: `apiManualJobRequest.JDText string` (new JSON field `jdText`).

- [ ] **Step 1: Write the failing test**

Add to `internal/web/api_test.go`, after `TestHandleAPIJobsManualInsertsAndTriggersOneOff`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestHandleAPIJobsManualPersistsJDText -v`
Expected: FAIL — compile error (`too few arguments in call to jobsubmit.InsertManualJob`), since `handleAPIJobsManual` hasn't been updated yet.

- [ ] **Step 3: Implement**

In `internal/web/api.go`, update `apiManualJobRequest` and `handleAPIJobsManual`:

```go
type apiManualJobRequest struct {
	URL    string `json:"url"`
	JDText string `json:"jdText"`
}
```

```go
func (s *Server) handleAPIJobsManual(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req apiManualJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}
	req.URL = strings.TrimSpace(req.URL)
	if req.URL == "" || (!strings.HasPrefix(req.URL, "http://") && !strings.HasPrefix(req.URL, "https://")) {
		http.Error(w, "url must be a non-empty http(s) URL", http.StatusBadRequest)
		return
	}
	req.JDText = strings.TrimSpace(req.JDText)

	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.store, req.URL, req.JDText, s.pages)
	if err != nil {
		httpError(w, "inserting manual job", err)
		return
	}
```

(the rest of the function — everything from `resp := apiManualJobResponse{...}` onward — is unchanged.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/web/... -v`
Expected: PASS (all tests, including the pre-existing suite)

- [ ] **Step 5: Run full Go suite + formatting check**

Run: `go test ./... && gofmt -l .`
Expected: all packages pass; `gofmt -l .` prints nothing

- [ ] **Step 6: Commit**

```bash
git add internal/web/api.go internal/web/api_test.go
git commit -m "Accept jdText in the manual-job-submit API"
```

---

### Task 4: `tailor_resume.py` — consume `manual_jd_text` (Python)

**Files:**
- Modify: `scripts/tailor_resume.py` (`process_job`, `rebuild_one`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `job["manual_jd_text"]` — present on every job dict now that `SELECT *`/`dict(row)` includes the new column (both `process_job`'s caller and `rebuild_one`'s internal `SELECT * FROM jobs WHERE id=?` pick it up automatically, no query changes needed).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`, after `test_process_job_marks_failed_when_compile_fails`:

```python
def test_process_job_uses_manual_jd_text_skips_fetch_jd_generic(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)

    def fail_if_called(url):
        raise AssertionError("fetch_jd_generic should not be called when manual_jd_text is present")
    monkeypatch.setattr(tr, "fetch_jd_generic", fail_if_called)
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    job = {
        "id": 3, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/3",
        "manual_jd_text": "We need a backend engineer with Go, Kubernetes, and PostgreSQL experience.",
    }
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "sent"
```

Add to `scripts/test_tailor_resume.py`, near the other `test_rebuild_one_*` tests:

```python
def test_rebuild_one_uses_manual_jd_text_from_db(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)

    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY, manual_jd_text TEXT NOT NULL DEFAULT '')")
    conn.execute(
        "INSERT INTO jobs (id, manual_jd_text) VALUES (1, ?)",
        ("We need a backend engineer with Go, Kubernetes, and PostgreSQL experience.",),
    )
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    captured_jd_data = {}

    def fake_build_resume_fields(job, jd_data, tight=False):
        captured_jd_data.update(jd_data)
        return "skills", ["bullet"], "projects", "focus", ["kw"]

    monkeypatch.setattr(tr, "build_resume_fields", fake_build_resume_fields)
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction=None)

    assert ok is True
    assert captured_jd_data["content_text"] == "We need a backend engineer with Go, Kubernetes, and PostgreSQL experience."
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `pytest scripts/test_tailor_resume.py -k "manual_jd_text" -v`
Expected: FAIL — `test_process_job_uses_manual_jd_text_skips_fetch_jd_generic` fails because `fetch_jd_generic` IS called (`AssertionError` raised); `test_rebuild_one_uses_manual_jd_text_from_db` fails because `captured_jd_data["content_text"]` is the title, not the JD text.

- [ ] **Step 3: Implement**

In `scripts/tailor_resume.py`, in `process_job`, replace:

```python
    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = None
    if gh_id and gh_slug:
        jd_data = fetch_greenhouse_job(gh_slug, gh_id)

    jd_unavailable = False
    if not jd_data:
        jd_data = fetch_jd_generic(url)
```

with:

```python
    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = None
    if gh_id and gh_slug:
        jd_data = fetch_greenhouse_job(gh_slug, gh_id)

    if not jd_data:
        manual_jd_text = (job.get("manual_jd_text") or "").strip()
        if manual_jd_text:
            jd_data = {
                "title": title, "company_name": company, "content_text": manual_jd_text,
                "content_html": "", "location": "", "absolute_url": url,
            }

    jd_unavailable = False
    if not jd_data:
        jd_data = fetch_jd_generic(url)
```

(the rest of `process_job`, starting from `if not jd_data or len(jd_data.get("content_text", "")) < MIN_USABLE_JD_CHARS:`, is unchanged.)

In `rebuild_one`, replace:

```python
    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = fetch_greenhouse_job(gh_slug, gh_id) if (gh_id and gh_slug) else None
    if not jd_data:
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }
```

with:

```python
    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = fetch_greenhouse_job(gh_slug, gh_id) if (gh_id and gh_slug) else None
    if not jd_data:
        manual_jd_text = (job.get("manual_jd_text") or "").strip()
        if manual_jd_text:
            jd_data = {
                "title": title, "company_name": company, "content_text": manual_jd_text,
                "content_html": "", "location": "", "absolute_url": url,
            }
    if not jd_data:
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pytest scripts/test_tailor_resume.py -k "manual_jd_text" -v`
Expected: PASS (both tests)

- [ ] **Step 5: Run full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all tests pass except the one known pre-existing unrelated failure (`test_process_job_skips_non_engineering_role`)

- [ ] **Step 6: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Use manual_jd_text instead of scraping when present"
```

---

### Task 5: Frontend — mobile validation fix + JD text/file input

**Files:**
- Modify: `frontend/src/pages/Jobs.tsx`
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/index.css`

**Interfaces:**
- Consumes: `POST /api/jobs/manual` now accepting an optional `jdText` field (Task 3).
- Produces: `submitManualJob(url: string, jdText?: string): Promise<ManualJobResponse>` (extended signature).

No automated test harness exists for `frontend/` — this task is implemented then verified manually (Step 4/5), not via TDD.

- [ ] **Step 1: Update `frontend/src/api.ts`**

Replace `submitManualJob`:

```ts
export async function submitManualJob(url: string, jdText?: string): Promise<ManualJobResponse> {
  const res = await fetch('/api/jobs/manual', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(jdText ? { url, jdText } : { url }),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/manual: ${res.status}`)
  return res.json()
}
```

- [ ] **Step 2: Update `frontend/src/pages/Jobs.tsx`**

Add a new state variable next to the existing manual-submit state:

```tsx
  const [manualUrl, setManualUrl] = useState('')
  const [manualJdText, setManualJdText] = useState('')
  const [manualStatus, setManualStatus] = useState<string | null>(null)
  const [manualSubmitting, setManualSubmitting] = useState(false)
```

Replace `handleSubmitManualJob` in full:

```tsx
  // Typed as a minimal structural type (just the one method this handler
  // needs) rather than importing React.FormEvent -- this file has no
  // existing `import React` or `FormEvent` import to hang that off of,
  // and pulling one in for a single event handler isn't worth it.
  async function handleSubmitManualJob(e: { preventDefault: () => void }) {
    e.preventDefault()
    const url = manualUrl.trim()
    if (!url) return
    // Validation moved here from the native <input required type="url">
    // constraint: on some mobile browsers/layouts, a failing native
    // constraint cancels the submit event before this handler ever runs
    // and renders no visible bubble -- the tap just does nothing, with
    // no way to tell what went wrong. Explicit JS validation always
    // surfaces a result through manualStatus, on every platform.
    if (!url.startsWith('http://') && !url.startsWith('https://')) {
      setManualStatus('Enter a valid http(s) URL.')
      return
    }
    setManualSubmitting(true)
    setManualStatus(null)
    try {
      const result = await submitManualJob(url, manualJdText.trim())
      setManualStatus(
        result.alreadyExisted
          ? `Already added — #J${result.id} (${result.company})`
          : `Added #J${result.id} — ${result.company} — resume incoming on Telegram`,
      )
      setManualUrl('')
      setManualJdText('')
    } catch (err) {
      setManualStatus(`Failed to add: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setManualSubmitting(false)
    }
  }

  // Reads a dropped/picked .txt or .md file client-side and drops its
  // content into the JD-text textarea, overwriting whatever was there --
  // one JD-text source at a time, not appended.
  function handleJdFileChange(e: { target: { files: FileList | null } }) {
    const file = e.target.files?.[0]
    if (!file) return
    const reader = new FileReader()
    reader.onload = () => {
      if (typeof reader.result === 'string') setManualJdText(reader.result)
    }
    reader.readAsText(file)
  }
```

Replace the `<form className="add-job" ...>` JSX block in full:

```tsx
      <form className="add-job-form" onSubmit={handleSubmitManualJob} noValidate>
        <div className="add-job">
          <input
            type="url"
            placeholder="Paste a job posting URL (Keka, Workday, anywhere)..."
            value={manualUrl}
            onChange={(e) => setManualUrl(e.target.value)}
          />
          <button type="submit" disabled={manualSubmitting}>
            {manualSubmitting ? 'Adding…' : 'Add & Tailor'}
          </button>
          {manualStatus && <span className="add-job-status">{manualStatus}</span>}
        </div>
        <div className="add-job-jd">
          <textarea
            placeholder="Paste JD text (optional) — used instead of scraping the link"
            value={manualJdText}
            onChange={(e) => setManualJdText(e.target.value)}
          />
          <input type="file" accept=".txt,.md" onChange={handleJdFileChange} />
        </div>
      </form>
```

(Note: `required` is removed from the `<input type="url">` — validation now lives entirely in `handleSubmitManualJob`. `type="url"` itself is kept, since it still gets the right mobile keyboard layout.)

- [ ] **Step 3: Update `frontend/src/index.css`**

Replace the existing `.add-job`/`.add-job-status` rules with:

```css
.add-job {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}

.add-job input[type='url'] {
  flex: 1;
  min-width: 240px;
}

.add-job-status {
  font-size: 0.9em;
  color: #888;
}

.add-job-jd {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  margin-bottom: 12px;
}

.add-job-jd textarea {
  flex: 1;
  min-width: 240px;
  min-height: 60px;
  resize: vertical;
}
```

- [ ] **Step 4: Build and type-check**

Run: `cd frontend && npm run build`
Expected: builds successfully (runs `tsc -b && vite build` per `package.json`), no TypeScript errors.

- [ ] **Step 5: Manual browser verification**

Use the `webapp-testing`/`run` skill (or start the dashboard locally per this repo's own dev workflow) and check, in an actual browser (resize to a mobile viewport, e.g. 375×667, via devtools):

1. Submitting an empty URL: no crash, no submission attempt.
2. Submitting `not a url`: `manualStatus` shows "Enter a valid http(s) URL." — visible feedback, not silence.
3. Submitting a valid `https://...` URL with no JD text: succeeds as before, `manualStatus` shows the success message, both fields clear.
4. Pasting JD text into the textarea, then submitting: request body includes `jdText` (check via browser devtools' Network tab).
5. Choosing a small `.txt` file via the file input: textarea populates with the file's content.
6. At the narrow mobile viewport width: confirm the URL input, button, and status text don't overflow/clip in a way that would hide feedback (the actual layout-driven part of the original bug).

- [ ] **Step 6: Commit**

```bash
git add frontend/src/pages/Jobs.tsx frontend/src/api.ts frontend/src/index.css
git commit -m "Fix mobile silent-fail on job submit; add JD text/file input"
```

---

### Task 6: Full-suite verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full Go suite + formatting check**

Run: `go test ./... && gofmt -l .`
Expected: all packages PASS; `gofmt -l .` prints nothing

- [ ] **Step 2: Run the full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all tests PASS except the one known pre-existing unrelated failure (`test_process_job_skips_non_engineering_role`)

- [ ] **Step 3: Rebuild frontend and confirm the Go server embeds it cleanly**

Run: `make build` (runs `make frontend` then `go build -o bin/jobwatch ./cmd/jobwatch`, per the `Makefile`)
Expected: succeeds, no errors.

- [ ] **Step 4: End-to-end manual smoke test (local)**

Run the dashboard locally (`./bin/jobwatch serve -config config.yaml`, or per this repo's own documented dev workflow) and, via a real browser at a mobile viewport width:

1. Submit a job URL with JD text pasted in — confirm the job appears in the jobs list.
2. Confirm (via a local sqlite browse of the dev DB, or by triggering the tailor-resume cron job manually) that the tailored resume's coverage scoring reflects the pasted JD text, not a title-only fallback.

- [ ] **Step 5: Deploy per CLAUDE.md's "Deploying a change" section**

This branch touches Go (`internal/providers`, `internal/store`, `internal/jobsubmit`, `internal/web`, `internal/tgsync`), a Python script (`scripts/tailor_resume.py`), and the frontend (`frontend/src/*`, needing a rebuild before deploy since the Go server embeds `internal/web/dist` via `go:embed`). Sync accordingly and verify with `poll-wrapper.sh`, matching this plan's reply-instructions predecessor's deploy pattern. **Do not deploy without explicit sign-off** — this changes the `jobs` table schema on the live, populated production DB (`jobwatch.db` on the EC2 box), which is a one-way migration (new column added, never removed) — low risk given it's additive-only and guarded by `IF NOT EXISTS`, but still a real schema change to data that can't be casually recreated.
