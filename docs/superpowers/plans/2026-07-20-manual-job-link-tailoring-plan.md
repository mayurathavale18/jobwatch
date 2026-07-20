# Manual Job-Link Submission + LLM-Assisted JD Tailoring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Mayur paste any job posting URL into the jobwatch dashboard and get a tailored resume + a real recruiter-lens verdict (screen vs reject-risk, missing keywords, coverage score) on Telegram within ~10-30 seconds, without waiting for the 30-minute cron — using free HTML scraping with an OpenCode Go LLM fallback for JD extraction/judging on sources jobwatch can't poll directly (Keka, etc).

**Architecture:** Dashboard UI posts a URL to a new Go endpoint, which inserts the job (shared insert logic factored out of the existing Telegram manual-submit path) and spawns a detached one-off Python process for just that job — bypassing the normal cron batch/budget gating but reusing every other part of the existing tailoring pipeline. JD extraction tries a free HTML scrape first, falls back to one LLM call only when the scrape is too thin. A second LLM call judges the JD-vs-resume fit. Resume *content selection* stays 100% deterministic/template-based (unchanged truth-lock); only already-selected bullet *phrasing* gets a bounded LLM reword pass, validated by a mechanical post-check (not the LLM's own word) before it's ever used.

**Tech Stack:** Go 1.x (existing `internal/*` packages), Python 3 stdlib (`urllib.request`, no new pip deps — matches existing `tailor_resume.py` conventions), React/TypeScript (existing dashboard), OpenCode Go (OpenAI-compatible endpoint `https://opencode.ai/zen/go/v1`).

## Global Constraints

- No new Python pip dependencies — use `urllib.request`/`json` (stdlib), matching every existing HTTP call in `tailor_resume.py` (`fetch_greenhouse_job`'s pattern).
- No `config.yaml`/YAML parsing added to `tailor_resume.py` — it has never read `config.yaml` (only the Go poller does); the LLM model name is a module-level constant instead, matching how `MAX_PER_CYCLE`/`DAILY_BUDGET` are already plain constants. This is a deliberate, smaller-footprint substitute for the spec's "new config.yaml key" line — same swappability (edit one constant), zero new parsing code.
- `OPENCODE_API_KEY` sourced from `.env` (gitignored, already the pattern for `JOBWATCH_TG_TOKEN`/`JOBWATCH_TG_CHAT`) — never hardcoded, never committed.
- Every new Go package/function gets a table-driven or scenario test in the same PR; every new Python function gets a `pytest` test in `scripts/test_tailor_resume.py` (new file — no Python tests exist yet in this repo).
- `gofmt -l .` must stay empty; `go test ./...` must stay green after every task.
- Resume content *selection* (which bullets, which focus) never changes in this plan — only JD-keyword-driven *ordering* and a bounded, mechanically-validated *phrasing* pass. Nothing here may cause a claim to appear that isn't backed by `facts.md`'s `TRUTHFUL_SKILLS`/`FAMILIAR_SKILLS`/`ADJACENT_SKILLS` tiers defined in Task 6.

---

## Task 1: `internal/jobsubmit` package — shared manual-insert logic

**Files:**
- Create: `internal/jobsubmit/jobsubmit.go`
- Create: `internal/jobsubmit/jobsubmit_test.go`

**Interfaces:**
- Produces: `jobsubmit.PageFetcher` interface (`FetchTitle(ctx, rawURL) (string, error)`), `jobsubmit.HTTPPageFetcher{}` (real implementation), `jobsubmit.InsertManualJob(ctx, st *store.Store, rawURL string, pages PageFetcher) (id int64, alreadyExisted bool, company string, title string, err error)`.
- Consumes: `store.Store` (`BeginTx`, `ExistsTx`, `InsertJob`), `providers.Job`.

This extracts `internal/tgsync/tgsync.go`'s current `addManualJob` body (lines ~345-420: `companyFromURL`, `manualSlug`, the `httpPageFetcher`/`pageFetcher` type, and the insert/dedupe logic) into a package both `tgsync` and the new web handler (Task 3) can call, so the exact same dedupe/slug behavior isn't duplicated in two places.

- [ ] **Step 1: Write the failing tests**

```go
// internal/jobsubmit/jobsubmit_test.go
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

	id, existed, company, title, err := InsertManualJob(ctx, st, jobURL, pages)
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

func TestInsertManualJobDuplicateURLIsNoOp(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	const jobURL = "https://valorem.keka.com/careers/jobdetails/124256"
	pages := fakePages{titles: map[string]string{jobURL: "Backend Engineer"}}

	id1, _, _, _, err := InsertManualJob(ctx, st, jobURL, pages)
	if err != nil {
		t.Fatalf("first InsertManualJob: %v", err)
	}

	id2, existed, _, _, err := InsertManualJob(ctx, st, jobURL, pages)
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

	id, existed, _, title, err := InsertManualJob(ctx, st, jobURL, fakePages{err: context.DeadlineExceeded})
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && go test ./internal/jobsubmit/... -v`
Expected: FAIL — package `jobsubmit` doesn't exist yet (`no Go files in ...`).

- [ ] **Step 3: Write the implementation**

```go
// internal/jobsubmit/jobsubmit.go

// Package jobsubmit inserts a job from a bare URL jobwatch can't poll
// directly (Naukri/Keka/YC/Wellfound/etc), shared by both the Telegram
// manual-submit path (internal/tgsync) and the dashboard's "add job link"
// endpoint (internal/web) so the dedupe/slug logic exists exactly once.
package jobsubmit

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"jobwatch/internal/providers"
	"jobwatch/internal/store"
)

// PageFetcher fetches a job posting page's <title>, used as a best-effort
// display title for a freshly-submitted URL. Interfaced so tests and the
// tg-sync fixture-backed fake don't make real HTTP requests.
type PageFetcher interface {
	FetchTitle(ctx context.Context, rawURL string) (string, error)
}

const maxPageFetchBytes = 200 * 1024

var titleTagRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

// HTTPPageFetcher is the real PageFetcher used outside tests.
type HTTPPageFetcher struct{ Client *http.Client }

func (h HTTPPageFetcher) FetchTitle(ctx context.Context, rawURL string) (string, error) {
	client := h.Client
	if client == nil {
		client = http.DefaultClient
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; jobwatch/1.0; personal job tracker)")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageFetchBytes))
	if err != nil {
		return "", err
	}

	m := titleTagRe.FindSubmatch(body)
	if m == nil {
		return "", nil
	}
	return strings.TrimSpace(html.UnescapeString(string(m[1]))), nil
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

// companyFromURL guesses a display company name from a job URL's host
// (e.g. "www.keka.com" -> "Keka"). Placeholder, not a real company lookup
// -- correct it via a "note:" Telegram reply if it's wrong.
func companyFromURL(rawURL string) string {
	u, err := neturl.Parse(rawURL)
	if err != nil || u.Host == "" {
		return "Unknown"
	}
	host := strings.TrimPrefix(u.Hostname(), "www.")
	root := strings.SplitN(host, ".", 2)[0]
	if root == "" {
		return "Unknown"
	}
	return strings.ToUpper(root[:1]) + root[1:]
}

// manualSlug derives a stable company_slug from a job URL's host, for the
// (provider, company_slug, external_id) dedupe key.
func manualSlug(rawURL string) string {
	return nonAlnum.ReplaceAllString(strings.ToLower(companyFromURL(rawURL)), "-")
}

// InsertManualJob inserts rawURL as a new "manual" provider job (status
// "new", so it rides the existing tailor-resume pipeline the same as any
// polled job) and reports whether it already existed. Re-submitting the
// same URL is a no-op (dedupe key includes the URL itself as external_id).
func InsertManualJob(ctx context.Context, st *store.Store, rawURL string, pages PageFetcher) (id int64, alreadyExisted bool, company string, title string, err error) {
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
		existingID, gErr := existingJobID(ctx, st, "manual", slug, rawURL)
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

`existingJobID` is one small helper this step still needs — write it for real rather than leaving a gap:

```go
// existingJobID looks up the row id for an already-existing (provider,
// company_slug, external_id) triple, used when InsertManualJob finds a
// duplicate and needs to report which job it already is.
func existingJobID(ctx context.Context, st *store.Store, provider, companySlug, externalID string) (int64, error) {
	rows, err := st.ListJobs(ctx, store.JobFilter{})
	if err != nil {
		return 0, err
	}
	for _, r := range rows {
		if r.Provider == provider && r.CompanySlug == companySlug && r.URL == externalID {
			return r.ID, nil
		}
	}
	return 0, fmt.Errorf("job existed per ExistsTx but not found in ListJobs")
}
```

Add `neturl "net/url"` to the import block (aliased since `url` collides with the `rawURL` parameter name pattern already used elsewhere in this codebase).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && go test ./internal/jobsubmit/... -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add internal/jobsubmit
git commit -m "Add internal/jobsubmit: shared manual-job insert/dedupe logic"
```

---

## Task 2: Refactor `internal/tgsync` to use `jobsubmit`

**Files:**
- Modify: `internal/tgsync/tgsync.go`

**Interfaces:**
- Consumes: `jobsubmit.PageFetcher`, `jobsubmit.HTTPPageFetcher`, `jobsubmit.InsertManualJob` (from Task 1).
- Produces: `Syncer.Pages` field retyped to `jobsubmit.PageFetcher` (was the now-deleted local `pageFetcher` interface — structurally identical, so `fakePages` in `tgsync_test.go` keeps compiling unchanged).

No behavior change — this only removes duplication now that Task 1 exists.

- [ ] **Step 1: Remove the now-duplicated code from `tgsync.go`**

Delete from `internal/tgsync/tgsync.go`:
- The `pageFetcher` interface (lines ~45-49).
- `maxPageFetchBytes`, `titleTagRe`, `httpPageFetcher` struct + its `FetchTitle` method (lines ~53-88).
- `nonAlnum` var, `companyFromURL`, `manualSlug` functions (lines ~398-420, near the bottom).
- The body of `addManualJob` (lines ~350-396).

Replace the `Pages pageFetcher` field in the `Syncer` struct with:

```go
	// Pages fetches manually-submitted job URLs' titles. Defaults to a real
	// HTTP fetcher; overridable for tests.
	Pages jobsubmit.PageFetcher
```

Replace `New(...)`'s `Pages: httpPageFetcher{}` with `Pages: jobsubmit.HTTPPageFetcher{}`.

Replace `addManualJob`'s body with:

```go
// addManualJob inserts rawURL as a new manual job via the shared
// jobsubmit package and confirms with the #J{id} tag reply-based status
// control depends on.
func (s *Syncer) addManualJob(ctx context.Context, msg *notify.Message, rawURL string) error {
	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.Store, rawURL, s.Pages)
	if err != nil {
		return err
	}
	if existed {
		return s.TG.Reply(ctx, msg.MessageID, "Already added that one.")
	}
	return s.TG.Reply(ctx, msg.MessageID, fmt.Sprintf("✓ Added — %s — %s\n#J%d", company, title, id))
}
```

Add `"jobwatch/internal/jobsubmit"` to the import block; remove now-unused imports (`html`, `io`, `net/http` — check each is still used elsewhere in the file before removing; `net/url` is still used by `manualJobURL`, keep it).

- [ ] **Step 2: Run the existing test suite to confirm nothing broke**

Run: `cd /home/dev-mayur/jobwatch && go test ./internal/tgsync/... -v`
Expected: PASS — `TestAddManualJobInsertsNewJob`, `TestAddManualJobDuplicateURLIsNoOp`, `TestAddManualJobTitleFetchFailsStillInserts`, and every other existing test all still pass unchanged (the fake `fakePages` type already structurally satisfies `jobsubmit.PageFetcher`, so no test edits needed).

- [ ] **Step 3: Run the full suite + gofmt**

Run: `cd /home/dev-mayur/jobwatch && go build ./... && gofmt -l . && go test ./...`
Expected: build succeeds, `gofmt -l .` prints nothing, all tests PASS.

- [ ] **Step 4: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add internal/tgsync/tgsync.go
git commit -m "Refactor tgsync manual-submit to use shared internal/jobsubmit"
```

---

## Task 3: Go endpoint `POST /api/jobs/manual` + `scripts/tailor-one.sh`

**Files:**
- Create: `scripts/tailor-one.sh`
- Modify: `internal/web/api.go`
- Modify: `internal/web/server.go`
- Modify: `internal/web/api_test.go`

**Interfaces:**
- Consumes: `jobsubmit.InsertManualJob` (Task 1).
- Produces: `POST /api/jobs/manual` — request `{"url": string}`, response `202 {"id": int64, "alreadyExisted": bool, "company": string, "title": string}` on success, `400` on invalid/missing URL.

- [ ] **Step 1: Create the wrapper script**

```bash
#!/usr/bin/env bash
# jobwatch tailor-one — tailors a resume for exactly one job id immediately,
# bypassing the batch cron's daily budget/rate-limit gating. Invoked by the
# dashboard's "add job link" endpoint (internal/web) right after a manual
# job is inserted, so the Telegram resume+verdict arrives in ~10-30s
# instead of waiting for the next tailor-resume cron tick.
#
# Usage: tailor-one.sh <job_id>

set -euo pipefail

JOB_ID="${1:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
LOCK_FILE="$ROOT_DIR/.tailor-resume.lock"

cd "$ROOT_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

export PATH="$ROOT_DIR:$PATH"

# Shares the batch cron's lock (tailor-resume-wrapper.sh) since both
# mutate tailored.json and the jobs table -- but waits (up to 5 minutes)
# rather than skipping outright, since an on-demand submission should
# still get processed once the batch run finishes, not silently vanish.
exec 200>"$LOCK_FILE"
if ! flock -w 300 200; then
  echo "TAILOR_ONE_FAILED: could not acquire lock within 5 minutes (batch cron still running?)" >&2
  exit 1
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" --job-id "$JOB_ID"
```

Make it executable: `chmod +x scripts/tailor-one.sh`.

- [ ] **Step 2: Write the failing Go test**

Add to `internal/web/api_test.go`:

```go
func TestHandleAPIJobsManualInsertsAndTriggersOneOff(t *testing.T) {
	srv, st := newTestServer(t)

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
```

Add `"jobwatch/internal/store"` to `api_test.go`'s imports if not already present (it's used by other tests in the same package already, so likely already imported — check before adding a duplicate).

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/dev-mayur/jobwatch && go test ./internal/web/... -run TestHandleAPIJobsManual -v`
Expected: FAIL — `apiManualJobResponse` undefined / route not found (404).

- [ ] **Step 4: Implement the handler**

Add to `internal/web/api.go`:

```go
type apiManualJobRequest struct {
	URL string `json:"url"`
}

type apiManualJobResponse struct {
	ID             int64  `json:"id"`
	AlreadyExisted bool   `json:"alreadyExisted"`
	Company        string `json:"company"`
	Title          string `json:"title"`
}

// tailorOneScript is the wrapper spawned for a single freshly-submitted
// job, relative to the process's cwd -- same convention as cronJobDefs'
// Script paths (see cron.go), always run from the repo root.
const tailorOneScript = "scripts/tailor-one.sh"

// handleAPIJobsManual inserts a job from an arbitrary URL (dashboard's
// "add job link" input) and spawns a detached one-off tailoring run for
// it, so the tailored resume + verdict reaches Telegram in roughly
// 10-30s instead of waiting for the next tailor-resume cron tick. Mirrors
// handleAPICronRun's detached-exec pattern: the HTTP response doesn't
// wait for tailoring to finish, since the result arrives via Telegram the
// same way every other job notification already does.
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

	id, existed, company, title, err := jobsubmit.InsertManualJob(ctx, s.store, req.URL, jobsubmit.HTTPPageFetcher{})
	if err != nil {
		httpError(w, "inserting manual job", err)
		return
	}

	resp := apiManualJobResponse{ID: id, AlreadyExisted: existed, Company: company, Title: title}

	if existed {
		w.WriteHeader(http.StatusOK)
		writeJSON(w, resp)
		return
	}

	logFile, err := os.OpenFile(filepath.Join(s.logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpError(w, "opening cron.log", err)
		return
	}

	cmd := exec.Command("bash", tailorOneScript, strconv.FormatInt(id, 10))
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		httpError(w, "starting tailor-one", err)
		return
	}

	go func() {
		defer logFile.Close()
		if err := cmd.Wait(); err != nil {
			slog.Error("tailor-one exited non-zero", "job_id", id, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, resp)
}
```

Add `"jobwatch/internal/jobsubmit"` and `"strings"` to `api.go`'s imports.

Register the route in `internal/web/server.go`'s `Handler()`:

```go
	mux.HandleFunc("POST /api/jobs/manual", s.handleAPIJobsManual)
```//add this line right after the existing `GET /api/jobs` registration.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && go test ./internal/web/... -v`
Expected: PASS for all three new tests plus every pre-existing `internal/web` test.

- [ ] **Step 6: Full suite + gofmt**

Run: `cd /home/dev-mayur/jobwatch && gofmt -l . && go test ./...`
Expected: clean gofmt output, all tests PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor-one.sh internal/web/api.go internal/web/server.go internal/web/api_test.go
git commit -m "Add POST /api/jobs/manual endpoint + tailor-one.sh one-off tailoring"
```

---

## Task 4: Frontend "Add job link" form

**Files:**
- Modify: `frontend/src/api.ts`
- Modify: `frontend/src/pages/Jobs.tsx`
- Modify: `frontend/src/index.css`

**Interfaces:**
- Consumes: `POST /api/jobs/manual` (Task 3).
- Produces: `submitManualJob(url: string): Promise<{id: number; alreadyExisted: boolean; company: string; title: string}>` in `api.ts`.

- [ ] **Step 1: Add the API function**

Add to `frontend/src/api.ts`:

```ts
export interface ManualJobResponse {
  id: number
  alreadyExisted: boolean
  company: string
  title: string
}

export async function submitManualJob(url: string): Promise<ManualJobResponse> {
  const res = await fetch('/api/jobs/manual', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ url }),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/manual: ${res.status}`)
  return res.json()
}
```

- [ ] **Step 2: Add the form to `Jobs.tsx`**

Add imports and state near the top of the component:

```tsx
import { useEffect, useState } from 'react'
import { fetchJobs, patchJob, submitManualJob, type JobsResponse } from '../api'
```

Inside `export default function Jobs()`, add state and a handler right after the existing `filters`/`data` state:

```tsx
  const [manualUrl, setManualUrl] = useState('')
  const [manualStatus, setManualStatus] = useState<string | null>(null)
  const [manualSubmitting, setManualSubmitting] = useState(false)

  // Typed as a minimal structural type (just the one method this handler
  // needs) rather than importing React.FormEvent -- this file has no
  // existing `import React` or `FormEvent` import to hang that off of,
  // and pulling one in for a single event handler isn't worth it.
  async function handleSubmitManualJob(e: { preventDefault: () => void }) {
    e.preventDefault()
    if (!manualUrl.trim()) return
    setManualSubmitting(true)
    setManualStatus(null)
    try {
      const result = await submitManualJob(manualUrl.trim())
      setManualStatus(
        result.alreadyExisted
          ? `Already added — #J${result.id} (${result.company})`
          : `Added #J${result.id} — ${result.company} — resume incoming on Telegram`,
      )
      setManualUrl('')
    } catch (err) {
      setManualStatus(`Failed to add: ${err instanceof Error ? err.message : String(err)}`)
    } finally {
      setManualSubmitting(false)
    }
  }
```

Render the form just above `<div className="stats">`:

```tsx
      <form className="add-job" onSubmit={handleSubmitManualJob}>
        <input
          type="url"
          placeholder="Paste a job posting URL (Keka, Workday, anywhere)..."
          value={manualUrl}
          onChange={(e) => setManualUrl(e.target.value)}
          required
        />
        <button type="submit" disabled={manualSubmitting}>
          {manualSubmitting ? 'Adding…' : 'Add & Tailor'}
        </button>
        {manualStatus && <span className="add-job-status">{manualStatus}</span>}
      </form>
```

- [ ] **Step 3: Add minimal styling**

Add to `frontend/src/index.css`, near the existing `.filters` block:

```css
.add-job {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.add-job input[type='url'] {
  flex: 1;
  min-width: 240px;
}

.add-job-status {
  font-size: 0.9em;
  color: var(--muted-color, #888);
}
```

(If `--muted-color` isn't an existing CSS variable in this file, check `.muted`'s existing color value and use that literal instead, to match the existing look rather than inventing a new variable.)

- [ ] **Step 4: Build the frontend and verify no type errors**

Run: `cd /home/dev-mayur/jobwatch/frontend && npm run build`
Expected: builds cleanly, no TypeScript errors.

- [ ] **Step 5: Manual browser verification**

Run: `cd /home/dev-mayur/jobwatch && go build -o bin/jobwatch ./cmd/jobwatch && ./bin/jobwatch serve -config config.yaml &`
Then open `http://127.0.0.1:8787` (or whatever `dashboard.addr` is set to) in a browser, paste `https://valorem.keka.com/careers/jobdetails/124256` into the new input, submit, and confirm the status message appears and a row for the new job shows up in the table after a refresh. Kill the background server afterward (`kill %1` or find the PID via `jobs`).

- [ ] **Step 6: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add frontend/src/api.ts frontend/src/pages/Jobs.tsx frontend/src/index.css
git commit -m "Add 'Add job link' form to the dashboard Jobs page"
```

---

## Task 5: `facts.md` honesty-tier additions + new keyword candidates

**Files:**
- Modify: `resume/facts.md`
- Modify: `scripts/tailor_resume.py` (only the `KEYWORD_CANDIDATES` and `CANONICAL_CASE` dicts — no logic yet, that's Task 6)

This is pure data/content prep so Task 6's code has real sets to work with. `facts.md` already has an honesty-tier system (`production`/`used`/`familiar`) that the code doesn't fully use yet.

**Important:** Mayur has already hand-edited `resume/facts.md` with his own real adjacency list (uncommitted, currently sitting as a local modification — check with `git diff resume/facts.md` before touching this file). It reads:

```markdown
# Adjecent Skills ( soft hand on these skills )
- Databases : cassandra
- GCP : BigQuery
- Automation : Ansible, Jenkins
- AI/ML : PyTorch, TensorFlow, Scikit-learn
- Languages: Java, Kotlin
- Frameworks: Spring, Spring Boot
```

Do NOT insert the spec's example section (Terraform->Pulumi etc — that was illustrative only, written before this real content existed). Instead:

- [ ] **Step 1: Fix the section header typo and commit Mayur's existing edit as-is**

`git diff resume/facts.md` first to confirm the section is still there unchanged. Fix only the header typo ("Adjecent" -> "Adjacent"), leave every skill/category line exactly as Mayur wrote it — these are his judgment calls, not this task's content to invent or second-guess:

```markdown
# Adjacent Skills ( soft hand on these skills )
- Databases : cassandra
- GCP : BigQuery
- Automation : Ansible, Jenkins
- AI/ML : PyTorch, TensorFlow, Scikit-learn
- Languages: Java, Kotlin
- Frameworks: Spring, Spring Boot
```

(Also leave his other already-filled `[FILL: ...]` metrics edits in the same file untouched — this task only touches the Adjacent Skills header.)

- [ ] **Step 2: Add matching keyword candidates to `tailor_resume.py`**

The code needs to recognize these exact terms in a JD for the adjacency tier to ever trigger. Add to `KEYWORD_CANDIDATES`:

In the `# Data / queues / search` group, add `"cassandra"`:

```python
    "redshift", "athena", "rabbitmq", "kafka", "cassandra",
```

In the `# Infra / DevOps` group, add `"bigquery"`, `"ansible"`, `"jenkins"`:

```python
    # Infra / DevOps
    "aws", "gcp", "azure", "terraform", "docker", "kubernetes", "k8s", "github actions", "ci/cd",
    "ecs", "ec2", "rds", "lambda", "secrets manager", "cloudwatch", "cloudfront", "route 53",
    "vpc", "iam", "fargate", "bigquery", "ansible", "jenkins",
```

Add a new `# AI / ML frameworks (adjacent)` entries to the existing `# AI / LLM` group: `"pytorch"`, `"tensorflow"`, `"scikit-learn"`:

```python
    "knn", "bm25", "hybrid retrieval", "pytorch", "tensorflow", "scikit-learn",
```

Add `"kotlin"` to the `# Languages` group:

```python
    "go", "golang", "python", "typescript", "javascript", "java", "scala", "rust", "sql", "bash", "kotlin",
```

Add `"spring"`, `"spring boot"` to the `# Backend / frameworks` group:

```python
    "rest", "rest api", "rest apis", "microservices", "fastapi", "nestjs", "node.js", "nodejs",
    "gin", "krakend", "api gateway", "api gateways", "temporal", "graphql", "hasura", "trpc",
    "spring", "spring boot",
```

Add `"kubernetes"` and `"gcp"` if not already present as JD-detectable candidates (both already exist in the current `KEYWORD_CANDIDATES` — confirm, don't duplicate).

Add casing entries to `CANONICAL_CASE` for every new term above that doesn't already have one (check each against the existing dict first — `gcp` may already exist):

```python
    "cassandra": "Cassandra", "bigquery": "BigQuery", "ansible": "Ansible", "jenkins": "Jenkins",
    "pytorch": "PyTorch", "tensorflow": "TensorFlow", "scikit-learn": "Scikit-learn",
    "kotlin": "Kotlin", "spring": "Spring", "spring boot": "Spring Boot",
```

- [ ] **Step 3: Verify the file still parses / imports cleanly**

Run: `cd /home/dev-mayur/jobwatch && python3 -c "import sys; sys.path.insert(0, 'scripts'); import tailor_resume; print(len(tailor_resume.KEYWORD_CANDIDATES), len(tailor_resume.CANONICAL_CASE))"`
Expected: prints two numbers with no traceback (import succeeds — this only exercises module-level code, no network calls happen at import time per the existing `REPO_ROOT`-relative design).

- [ ] **Step 4: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add resume/facts.md scripts/tailor_resume.py
git commit -m "Add adjacent-tool-exposure tier to facts.md and new keyword candidates"
```

---

## Task 6: Honesty-tier keyword classification (`ADJACENT_SKILLS`, `FAMILIAR_SKILLS`, 3-bucket coverage)

**Files:**
- Modify: `scripts/tailor_resume.py`
- Create: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `FAMILIAR_SKILLS: set[str]`, `ADJACENT_SKILLS: set[str]` (Mayur's curated "soft hand" tools from `facts.md`'s "Adjacent Skills" section — a flat set, not anchor-mapped, matching how he actually wrote that section as category-grouped lists rather than 1:1 pairs), `classify_keyword(kw: str) -> str` (returns `"direct"`, `"hedged_familiar"`, `"hedged_adjacent"`, or `"fabrication_risk"`), `score_coverage_tiered(keywords: list[str], resume_text: str) -> dict` (returns `{"score": float, "direct": [...], "hedged": [...], "missing": [...]}`).
- Consumes: `TRUTHFUL_SKILLS`, `KEYWORD_CANDIDATES`, `keyword_in_text` (all existing).

This is the code counterpart to Task 5's `facts.md` additions — existing `score_coverage`/`TRUTHFUL_SKILLS` stay untouched (still used as-is elsewhere), this adds the new tiered classification alongside them.

- [ ] **Step 1: Write the failing tests**

```python
# scripts/test_tailor_resume.py
"""Tests for scripts/tailor_resume.py. Run with: pytest scripts/test_tailor_resume.py -v"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import tailor_resume as tr


def test_classify_keyword_direct_for_truthful_skill():
    assert tr.classify_keyword("terraform") == "direct"


def test_classify_keyword_hedged_familiar_for_kafka():
    assert tr.classify_keyword("kafka") == "hedged_familiar"


def test_classify_keyword_hedged_adjacent_for_cassandra():
    # Cassandra is one of Mayur's curated "Adjacent Skills" in facts.md
    # (Databases : cassandra) -- not in facts.md's production/used/familiar
    # tiers at all, but a fair hedged claim per his own judgment call.
    assert tr.classify_keyword("cassandra") == "hedged_adjacent"


def test_classify_keyword_fabrication_risk_for_unrelated_tool():
    assert tr.classify_keyword("salesforce") == "fabrication_risk"


def test_score_coverage_tiered_buckets_keywords_correctly():
    keywords = ["terraform", "kafka", "cassandra", "salesforce"]
    resume_text = "Owned AWS infrastructure via Terraform for 5 services."
    result = tr.score_coverage_tiered(keywords, resume_text)

    assert "terraform" in result["direct"]
    assert "kafka" not in result["direct"] and "kafka" not in result["hedged"]
    assert "cassandra" not in result["hedged"]  # not present in resume_text yet
    assert "salesforce" in result["missing"]
    assert result["score"] == 0.25  # only "terraform" actually appears in the text


def test_score_coverage_tiered_counts_hedged_keyword_present_in_text():
    keywords = ["kafka"]
    resume_text = "Has working exposure to Kafka for adjacent messaging needs."
    result = tr.score_coverage_tiered(keywords, resume_text)
    assert "kafka" in result["hedged"]
    assert result["score"] == 1.0
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'classify_keyword'` (and similarly for `score_coverage_tiered`).

- [ ] **Step 3: Implement**

Add directly after the existing `TRUTHFUL_SKILLS` set definition in `scripts/tailor_resume.py`:

```python
# Facts.md's "familiar" tier: concepts known, NOT hands-on production use.
# A JD keyword landing here is eligible for a resume claim, but only with
# hedging language ("exposure to", "familiarity with") -- never phrased as
# if hands-on. See facts.md's "Skills inventory (honesty tiers)" section.
FAMILIAR_SKILLS = {
    "kafka", "kubernetes", "k8s", "gcp", "langchain", "mongodb", "rabbitmq",
    "trpc", "firebase",
}

# Mayur's curated "soft hand" tools -- see facts.md's "Adjacent Skills"
# section, which this must be kept in sync with (same manual-sync pattern
# as TRUTHFUL_SKILLS itself). Not in facts.md's production/used/familiar
# tiers at all, but a fair hedged "exposure to" claim per his own judgment.
ADJACENT_SKILLS = {
    "cassandra", "bigquery", "ansible", "jenkins",
    "pytorch", "tensorflow", "scikit-learn",
    "kotlin", "spring", "spring boot",
}

def classify_keyword(kw):
    """Classify a JD keyword into one of four honesty tiers for resume
    claims: "direct" (facts.md production/used tier, claim plainly),
    "hedged_familiar" (facts.md familiar tier, claim only with hedge
    language), "hedged_adjacent" (Mayur's curated ADJACENT_SKILLS, claim
    only with hedge language), or "fabrication_risk" (nothing related at
    all -- never claimed).
    """
    if kw in TRUTHFUL_SKILLS:
        return "direct"
    if kw in FAMILIAR_SKILLS:
        return "hedged_familiar"
    if kw in ADJACENT_SKILLS:
        return "hedged_adjacent"
    return "fabrication_risk"

def score_coverage_tiered(keywords, resume_text):
    """Like score_coverage, but buckets keywords by honesty tier instead
    of a flat covered/not-covered split. Returns a dict with:
    - score: fraction of `keywords` actually present in resume_text
    - direct: direct-tier keywords present in resume_text
    - hedged: hedged-tier (familiar or adjacent) keywords present in
      resume_text
    - missing: keywords absent from resume_text, regardless of tier
      (includes fabrication_risk keywords, which should never be
      injected regardless of presence)
    """
    direct, hedged, missing = [], [], []
    for kw in keywords:
        present = keyword_in_text(kw, resume_text)
        tier = classify_keyword(kw)
        if present and tier == "direct":
            direct.append(kw)
        elif present and tier in ("hedged_familiar", "hedged_adjacent"):
            hedged.append(kw)
        else:
            missing.append(kw)
    score = (len(direct) + len(hedged)) / len(keywords) if keywords else 1.0
    return {"score": round(score, 2), "direct": direct, "hedged": hedged, "missing": missing}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all 6 tests).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add tiered keyword classification (direct/hedged/fabrication-risk)"
```

---

## Task 7: `fetch_jd_generic()` — free HTML scrape for non-Greenhouse JD text

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `html_to_jd_text(raw_html: str) -> str`, `fetch_jd_generic(url: str) -> dict | None` (same dict shape as `fetch_greenhouse_job`'s return value: `title`, `company_name`, `content_text`, `content_html`, `location`, `absolute_url`).
- Consumes: `urlopen`, `Request` (already imported).

This task is the free-scrape half only — no LLM call yet (Task 9 wires that in as a fallback).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`:

```python
def test_html_to_jd_text_strips_tags_and_scripts():
    html_input = """
    <html><head><script>var x = 1;</script><style>.a{color:red}</style></head>
    <body><nav>Home | About</nav>
    <main><h1>Backend Engineer</h1><p>We need someone who knows Go and Kubernetes.</p></main>
    <footer>© 2026 Example Corp</footer></body></html>
    """
    text = tr.html_to_jd_text(html_input)
    assert "Backend Engineer" in text
    assert "Go and Kubernetes" in text
    assert "var x = 1" not in text
    assert "color:red" not in text


def test_fetch_jd_generic_returns_none_on_fetch_error(monkeypatch):
    def raise_error(*args, **kwargs):
        raise OSError("connection refused")
    monkeypatch.setattr(tr, "urlopen", raise_error)
    assert tr.fetch_jd_generic("https://example.com/job/1") is None


def test_fetch_jd_generic_parses_real_looking_page(monkeypatch):
    fake_html = (
        "<html><body><main><h1>Software Engineer</h1>"
        "<p>Looking for someone with Python and Terraform experience, "
        "5+ years, distributed systems background required.</p></main></body></html>"
    ).encode("utf-8")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return fake_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())
    result = tr.fetch_jd_generic("https://valorem.keka.com/careers/jobdetails/124256")
    assert result is not None
    assert "Terraform" in result["content_text"]
    assert result["absolute_url"] == "https://valorem.keka.com/careers/jobdetails/124256"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'html_to_jd_text'` (and `fetch_jd_generic`).

- [ ] **Step 3: Implement**

Add directly after `fetch_greenhouse_job` in `scripts/tailor_resume.py`:

```python
def html_to_jd_text(raw_html):
    """Strip a job posting page down to plausible JD body text: drop
    script/style/nav/header/footer blocks entirely (boilerplate, never JD
    content), convert block tags to newlines so paragraphs don't run
    together, strip remaining tags, decode entities, collapse whitespace.
    Lightweight and heuristic -- good enough for most static ATS pages,
    not a real DOM parser. See fetch_jd_generic for the fallback when this
    isn't enough (JS-rendered pages return almost nothing usable here).
    """
    text = raw_html
    for tag in ("script", "style", "nav", "header", "footer"):
        text = re.sub(rf"<{tag}[^>]*>.*?</{tag}>", " ", text, flags=re.S | re.I)
    text = re.sub(r"</(p|div|li|h[1-6]|br)\s*>", "\n", text, flags=re.I)
    text = re.sub(r"<li[^>]*>", "\n- ", text, flags=re.I)
    text = re.sub(r"<[^>]+>", " ", text)
    text = html.unescape(text)
    text = re.sub(r"[ \t]+", " ", text)
    text = re.sub(r"\n\s*\n+", "\n\n", text)
    return text.strip()

# Below this many characters of scraped text, fetch_jd_generic treats the
# page as effectively empty (JS-rendered SPA shell, blocked fetch, etc)
# and falls back to the OpenCode Go LLM extraction path (see Task 9).
MIN_USABLE_JD_CHARS = 150

def fetch_jd_generic(url):
    """Fetch full JD text from an arbitrary (non-Greenhouse) job posting
    URL via a plain HTML GET + tag-stripping. Returns the same dict shape
    as fetch_greenhouse_job so callers don't need to branch on source.
    Returns None only on a hard fetch failure (network error, non-200);
    a thin/empty result is still returned so the caller (process_job, see
    Task 15) can decide whether to fall back to an LLM extraction pass.
    """
    try:
        req = Request(url, headers={"User-Agent": "jobwatch-resume-tailor/1.0"})
        with urlopen(req, timeout=15) as resp:
            raw_html = resp.read().decode("utf-8", errors="replace")
    except Exception as e:
        log(f"WARN: Failed to fetch JD page for {url}: {e}")
        return None

    text = html_to_jd_text(raw_html)
    return {
        "title": "",
        "company_name": "",
        "content_text": text,
        "content_html": raw_html,
        "location": "",
        "absolute_url": url,
    }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests from Tasks 6 and 7).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add fetch_jd_generic: free HTML scrape for non-Greenhouse JD text"
```

---

## Task 8: OpenCode Go client helper

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `call_opencode(system_prompt: str, user_content: str, model: str = DEFAULT_OPENCODE_MODEL, timeout: int = 20) -> str | None` (returns the assistant message content, or `None` on any failure — never raises, callers always get a clean fallback path).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`:

```python
def test_call_opencode_returns_none_without_api_key(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "")
    assert tr.call_opencode("system", "user") is None


def test_call_opencode_returns_content_on_success(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            import json as _json
            return _json.dumps({
                "choices": [{"message": {"content": "hello from the model"}}]
            }).encode("utf-8")

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=20: FakeResponse())
    result = tr.call_opencode("system prompt", "user prompt")
    assert result == "hello from the model"


def test_call_opencode_returns_none_on_http_error(monkeypatch):
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")
    def raise_error(*args, **kwargs):
        raise OSError("timed out")
    monkeypatch.setattr(tr, "urlopen", raise_error)
    assert tr.call_opencode("system", "user") is None
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'call_opencode'`.

- [ ] **Step 3: Implement**

Add near the top of `scripts/tailor_resume.py`, right after the existing `TG_TOKEN`/`TG_CHAT` env block:

```python
# OpenCode Go config (OpenAI-compatible gateway, https://opencode.ai/docs/zen).
# Used only for JD extraction fallback (Task 9), judging (Task 10), and
# bounded bullet rewording (Task 13) -- never for resume content selection.
OPENCODE_API_KEY = os.environ.get("OPENCODE_API_KEY", "")
OPENCODE_BASE_URL = "https://opencode.ai/zen/go/v1"
DEFAULT_OPENCODE_MODEL = "deepseek-v4-pro"
```

Add the client function near `fetch_greenhouse_job` (same section, after `fetch_jd_generic`):

```python
def call_opencode(system_prompt, user_content, model=DEFAULT_OPENCODE_MODEL, timeout=20):
    """Call OpenCode Go's OpenAI-compatible chat completions endpoint.
    Returns the assistant's raw message content, or None on any failure
    (missing key, timeout, non-200, malformed response) -- callers always
    have a non-LLM fallback and must never block on this.
    """
    if not OPENCODE_API_KEY:
        return None

    payload = json.dumps({
        "model": model,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": user_content},
        ],
    }).encode("utf-8")

    try:
        req = Request(
            f"{OPENCODE_BASE_URL}/chat/completions",
            data=payload,
            method="POST",
            headers={
                "Authorization": f"Bearer {OPENCODE_API_KEY}",
                "Content-Type": "application/json",
            },
        )
        with urlopen(req, timeout=timeout) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return data["choices"][0]["message"]["content"]
    except Exception as e:
        log(f"WARN: OpenCode call failed: {e}")
        return None
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add call_opencode: OpenCode Go chat-completions client helper"
```

---

## Task 9: Wire LLM fallback into `fetch_jd_generic`

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Modifies: `fetch_jd_generic` (Task 7) to call `call_opencode` (Task 8) when the scrape is too thin.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_fetch_jd_generic_falls_back_to_llm_when_scrape_thin(monkeypatch):
    # Simulate a JS-rendered SPA shell: almost no text in the raw HTML.
    thin_html = b"<html><body><div id='root'></div><script src='app.js'></script></body></html>"

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return thin_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())
    monkeypatch.setattr(tr, "OPENCODE_API_KEY", "fake-key")
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Backend Engineer role requiring Go, Terraform, and distributed systems experience.",
    )

    result = tr.fetch_jd_generic("https://valorem.keka.com/careers/jobdetails/124256")
    assert result is not None
    assert "Terraform" in result["content_text"]


def test_fetch_jd_generic_skips_llm_when_scrape_is_already_usable(monkeypatch):
    good_html = (
        b"<html><body><main><h1>Backend Engineer</h1>"
        b"<p>" + b"Requires Go and PostgreSQL experience. " * 10 + b"</p></main></body></html>"
    )

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return good_html

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())

    def fail_if_called(*args, **kwargs):
        raise AssertionError("call_opencode should not be called when the scrape is already usable")
    monkeypatch.setattr(tr, "call_opencode", fail_if_called)

    result = tr.fetch_jd_generic("https://example.com/job/1")
    assert "PostgreSQL" in result["content_text"]
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — the first new test fails because `fetch_jd_generic` doesn't call the LLM fallback yet (`content_text` stays thin, "Terraform" not present).

- [ ] **Step 3: Implement**

Replace `fetch_jd_generic`'s body in `scripts/tailor_resume.py` with:

```python
def fetch_jd_generic(url):
    """Fetch full JD text from an arbitrary (non-Greenhouse) job posting
    URL. Tries a free HTML scrape first; if that yields too little usable
    text (JS-rendered SPA shell, blocked fetch, etc), falls back to one
    OpenCode Go call asking it to extract the JD text from the raw HTML.
    Returns the same dict shape as fetch_greenhouse_job. Returns None only
    on a hard fetch failure -- a thin/empty result after both attempts
    still returns a dict (with whatever text was found), so the caller
    can flag "JD text unavailable" rather than treat it as a fetch error.
    """
    try:
        req = Request(url, headers={"User-Agent": "jobwatch-resume-tailor/1.0"})
        with urlopen(req, timeout=15) as resp:
            raw_html = resp.read().decode("utf-8", errors="replace")
    except Exception as e:
        log(f"WARN: Failed to fetch JD page for {url}: {e}")
        return None

    text = html_to_jd_text(raw_html)

    if len(text) < MIN_USABLE_JD_CHARS:
        log(f"JD scrape too thin ({len(text)} chars) for {url}; trying OpenCode extraction")
        llm_text = call_opencode(
            system_prompt=(
                "You extract job description text from raw HTML. Return ONLY the "
                "job description body text (responsibilities, requirements, "
                "qualifications) as plain text, no HTML, no commentary. If the HTML "
                "genuinely contains no job description content, return an empty string."
            ),
            user_content=raw_html[:20000],
        )
        if llm_text and len(llm_text.strip()) >= MIN_USABLE_JD_CHARS:
            text = llm_text.strip()

    return {
        "title": "",
        "company_name": "",
        "content_text": text,
        "content_html": raw_html,
        "location": "",
        "absolute_url": url,
    }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add OpenCode LLM fallback to fetch_jd_generic for thin scrapes"
```

---

## Task 10: `llm_judge()` — recruiter-lens verdict

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `llm_judge(jd_text: str, resume_text: str, title: str, company: str) -> dict` (always returns `{"verdict": "screen"|"reject_risk", "missing_keywords": [...], "reason": str, "source": "llm"|"rule_based"}` — never raises, falls back to a rule-based verdict via `score_coverage` on any LLM failure).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`:

```python
def test_llm_judge_parses_valid_json_response(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": ["kubernetes"], "reason": "Strong backend match."}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["verdict"] == "screen"
    assert result["missing_keywords"] == ["kubernetes"]
    assert result["reason"] == "Strong backend match."
    assert result["source"] == "llm"


def test_llm_judge_falls_back_to_rule_based_on_llm_failure(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20: None,
    )
    result = tr.llm_judge(
        "Requires Go, Terraform, Kubernetes, GraphQL, Rust.",
        "Owned AWS infrastructure via Terraform for 5 services.",
        "Backend Engineer", "Acme",
    )
    assert result["source"] == "rule_based"
    assert result["verdict"] in ("screen", "reject_risk")
    assert isinstance(result["missing_keywords"], list)


def test_llm_judge_falls_back_to_rule_based_on_malformed_json(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "not valid json at all",
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["source"] == "rule_based"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'llm_judge'`.

- [ ] **Step 3: Implement**

Add after `call_opencode` in `scripts/tailor_resume.py`:

```python
RULE_BASED_REJECT_THRESHOLD = 0.4

def llm_judge(jd_text, resume_text, title, company):
    """Judge how a busy recruiter (5-10 seconds per resume, 100+ resumes
    to screen) would react to this resume against this JD: screen it
    forward, or reject-risk. Always returns a usable result -- falls back
    to a rule-based verdict (score_coverage threshold) on any LLM
    failure, timeout, or malformed response, tagged via "source" so the
    Telegram message can show which one produced it.
    """
    raw = call_opencode(
        system_prompt=(
            "You are a hiring manager screening resumes for a "
            f"{title} role at {company}. You see 100+ resumes and spend "
            "5-10 seconds on each. Given the job description and a "
            "candidate's resume text, decide: would you screen this "
            "resume forward for a closer look, or is it reject-risk? "
            "Respond with ONLY valid JSON, no markdown fences, no "
            "commentary, in this exact shape: "
            '{"verdict": "screen"|"reject_risk", '
            '"missing_keywords": ["keyword1", "keyword2"], '
            '"reason": "one sentence explaining the verdict"}'
        ),
        user_content=f"JOB DESCRIPTION:\n{jd_text}\n\nRESUME:\n{resume_text}",
    )

    if raw:
        try:
            parsed = json.loads(raw.strip().strip("`").removeprefix("json").strip())
            if parsed.get("verdict") in ("screen", "reject_risk") and isinstance(parsed.get("missing_keywords"), list):
                return {
                    "verdict": parsed["verdict"],
                    "missing_keywords": parsed["missing_keywords"],
                    "reason": str(parsed.get("reason", "")),
                    "source": "llm",
                }
        except (json.JSONDecodeError, AttributeError):
            log("WARN: llm_judge got malformed JSON from OpenCode, falling back to rule-based")

    jd_keywords = extract_keywords(jd_text)
    score, _covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    verdict = "reject_risk" if score < RULE_BASED_REJECT_THRESHOLD else "screen"
    return {
        "verdict": verdict,
        "missing_keywords": not_covered + not_truthful,
        "reason": f"Rule-based: {score:.2f} keyword coverage (LLM unavailable).",
        "source": "rule_based",
    }
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add llm_judge: recruiter-lens verdict with rule-based fallback"
```

---

## Task 11: Reorder `build_skills_section` by JD match

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Modifies: `build_skills_section(focus, jd_keywords)` — same signature, changes internal ordering only.

- [ ] **Step 1: Read the current implementation to know exactly what to change**

Run: `sed -n '312,361p' /home/dev-mayur/jobwatch/scripts/tailor_resume.py` and read the output before writing the test/implementation below — the category lists inside this function must be identified precisely (they're not shown in this plan verbatim since they're long hardcoded skill lists per category; the change is structural, not a content rewrite).

- [ ] **Step 2: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_build_skills_section_lists_jd_matched_keyword_first():
    # Pick a category this function definitely emits (Languages) and a
    # keyword from it that would normally NOT be first alphabetically/
    # as-authored, to prove JD-match reordering actually happened.
    section_with_match = tr.build_skills_section(["backend"], ["python"])
    section_without_match = tr.build_skills_section(["backend"], [])

    # The JD-matched run must place "Python" before whatever led the line
    # in the unmatched run (order changed because of the JD keyword).
    def first_language_in_line(section):
        for line in section.split("\n"):
            if "Go" in line or "Python" in line:
                # crude split on the skill-list line's comma-joined items
                items = line.split("{")[-1].split("}")[0].split(", ")
                return items[0]
        return None

    matched_first = first_language_in_line(section_with_match)
    assert matched_first == "Python"
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v -k skills_section`
Expected: FAIL (current implementation lists skills in a fixed as-authored order, not JD-match-first) — if it unexpectedly passes, inspect the actual current ordering (read via Step 1) and adjust the test's expected value to a genuinely-reordered case before proceeding.

- [ ] **Step 4: Implement the reordering**

This step's exact code depends on `build_skills_section`'s current per-category list structure (read in Step 1). The change pattern to apply to **each** category's skill list before it's joined into a line:

```python
def _jd_first_order(skills, jd_keywords):
    """Reorder a category's skill list so JD-matched items come first,
    preserving relative order within each group otherwise -- a keyword-
    forward ordering for a recruiter's 5-10 second scan, not a random
    shuffle."""
    matched = [s for s in skills if any(keyword_in_text(kw, s) for kw in jd_keywords)]
    unmatched = [s for s in skills if s not in matched]
    return matched + unmatched
```

Add this helper function directly above `build_skills_section`, then wrap each category's skill list with it right before the list gets joined with `", ".join(...)` — e.g. if the current code has something like:

```python
languages = ["Go", "Python", "TypeScript", "JavaScript", "SQL", "Bash"]
...
f"\\techSkill{{Languages}}{{{', '.join(languages)}}}"
```

change it to:

```python
languages = _jd_first_order(["Go", "Python", "TypeScript", "JavaScript", "SQL", "Bash"], jd_keywords)
...
f"\\techSkill{{Languages}}{{{', '.join(languages)}}}"
```

Apply the same `_jd_first_order(...)` wrap to every category list in the function (Languages, Backend/Frameworks, Data/Infra, etc — whatever categories Step 1's read reveals), using each category's actual current literal list as the first argument, unchanged, just wrapped.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far). If a category's test still shows unmatched ordering, confirm that category's list was actually wrapped in Step 4 — a missed category is the most likely cause of a lingering failure.

- [ ] **Step 6: Compile-verify against the real template (per project convention)**

Run:
```bash
cd /home/dev-mayur/jobwatch
python3 -c "
import sys; sys.path.insert(0, 'scripts')
import tailor_resume as tr
tex, focus, kws = tr.generate_resume(
    {'id': 999999, 'company_name': 'TestCo', 'title': 'Backend Engineer', 'url': 'https://example.com/job'},
    {'title': 'Backend Engineer', 'company_name': 'TestCo', 'content_text': 'Requires Python and PostgreSQL experience.', 'content_html': '', 'location': '', 'absolute_url': 'https://example.com/job'},
)
open('/tmp/test_resume.tex', 'w').write(tex)
"
tectonic /tmp/test_resume.tex --outdir /tmp
pdftotext /tmp/test_resume.pdf - | head -30
```
Expected: compiles to a 1-page PDF with no LaTeX errors, `pdftotext` output shows "Python" appearing before other languages in the Skills line — confirms the reordering survives actual LaTeX compilation, not just the Python string check (per this project's existing convention of verifying generated output against the real compiled artifact, not just source).

- [ ] **Step 7: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Reorder build_skills_section to list JD-matched keywords first"
```

---

## Task 12: Extend `inject_keyword_emphasis` for hedged-tier keywords

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Modifies: `inject_keyword_emphasis(bullets, skills_section, jd_keywords)` — same signature and return shape (`(bullets, extra_skills_line)`), now also folds in hedged-tier keywords with explicit hedge language, kept visibly distinct from direct-tier injections.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_inject_keyword_emphasis_hedges_familiar_tier_keyword():
    bullets = ["Built a backend service using Go and PostgreSQL."]
    skills_section = "\\techSkill{Languages}{Go, Python}"
    jd_keywords = ["kafka"]  # hedged_familiar tier, not in TRUTHFUL_SKILLS

    new_bullets, extra_line = tr.inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    combined = new_bullets[0] + (extra_line or "")
    assert "Kafka" in combined
    # Must use hedging language, not a plain/confident claim.
    assert any(hedge in combined for hedge in ("exposure to", "familiarity with", "working knowledge of"))


def test_inject_keyword_emphasis_never_touches_fabrication_risk_keyword():
    bullets = ["Built a backend service using Go and PostgreSQL."]
    skills_section = "\\techSkill{Languages}{Go, Python}"
    jd_keywords = ["salesforce"]  # fabrication_risk tier

    new_bullets, extra_line = tr.inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    combined = new_bullets[0] + (extra_line or "")
    assert "Salesforce" not in combined and "salesforce" not in combined
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v -k inject_keyword_emphasis`
Expected: FAIL — current `inject_keyword_emphasis` only handles `TRUTHFUL_SKILLS`-gated direct keywords via `score_coverage`'s flat `not_covered` list, so "kafka" never gets added at all today (correctly excluded as a `not_truthful` gap, but no hedged path exists yet to add it safely).

- [ ] **Step 3: Implement**

Replace `inject_keyword_emphasis`'s body in `scripts/tailor_resume.py`:

```python
def inject_keyword_emphasis(bullets, skills_section, jd_keywords):
    """Close JD-keyword coverage gaps without fabricating anything.

    Direct-tier gaps (facts.md production/used skills missing from the
    fixed bullet text) get folded in plainly, same as before. Hedged-tier
    gaps (facts.md familiar tier, or Mayur's curated ADJACENT_SKILLS) get
    folded in too, but only with explicit hedging language -- never
    phrased as hands-on. Fabrication-risk keywords
    (score_coverage_tiered's "missing" bucket that isn't hedge-eligible)
    are never touched here.
    """
    draft_text = skills_section + "\n" + "\n".join(bullets)
    result = score_coverage_tiered(jd_keywords, draft_text)

    # Direct-tier gaps: present in jd_keywords, not yet in draft_text, and
    # classify as "direct" (i.e. would have landed in old score_coverage's
    # not_covered bucket).
    direct_gaps = [kw for kw in jd_keywords
                   if not keyword_in_text(kw, draft_text) and classify_keyword(kw) == "direct"]
    hedged_gaps = [kw for kw in jd_keywords
                   if not keyword_in_text(kw, draft_text)
                   and classify_keyword(kw) in ("hedged_familiar", "hedged_adjacent")]

    if not direct_gaps and not hedged_gaps:
        return bullets, None

    bullets = list(bullets)
    inject_direct, remaining_direct = direct_gaps[:MAX_INJECTED_KEYWORDS], direct_gaps[MAX_INJECTED_KEYWORDS:]
    inject_hedged, remaining_hedged = hedged_gaps[:MAX_INJECTED_KEYWORDS], hedged_gaps[MAX_INJECTED_KEYWORDS:]

    if inject_direct and bullets:
        bolded = ", ".join(f"\\textbf{{{canonical_case(kw)}}}" for kw in inject_direct)
        bullets[0] = bullets[0].rstrip() + f" Also applies {bolded} in this work."

    if inject_hedged and bullets:
        hedged_list = ", ".join(canonical_case(kw) for kw in inject_hedged)
        bullets[0] = bullets[0].rstrip() + f" Has working exposure to {hedged_list} for adjacent needs."

    extra_skills_line = None
    remaining = remaining_direct + remaining_hedged
    if remaining:
        plain = ", ".join(canonical_case(kw) for kw in remaining)
        extra_skills_line = f"\\techSkill{{Additional Relevant Skills}}{{{plain}}}"

    return bullets, extra_skills_line
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far, including the pre-existing `inject_keyword_emphasis` behavior for direct-tier gaps — if any prior test for direct-tier injection exists and now fails, check that `direct_gaps` computation still matches the old `not_covered` semantics exactly before proceeding).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Extend inject_keyword_emphasis to hedge-claim familiar/adjacent keywords"
```

---

## Task 13: `llm_reword_bullet()` + mechanical safety check

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `llm_reword_bullet(bullet_text: str, direct_keywords: list[str], hedged_keywords: list[str]) -> str` (returns the reworded bullet, or the original unchanged on any failure/safety-check rejection), `reword_is_safe(original: str, reworded: str, approved_keywords: list[str]) -> bool`.

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`:

```python
def test_reword_is_safe_rejects_changed_numbers():
    original = "Handled 1k RPS with 250ms P99 latency."
    reworded = "Handled 5k RPS with 250ms P99 latency."  # number changed: 1k -> 5k
    assert tr.reword_is_safe(original, reworded, approved_keywords=[]) is False


def test_reword_is_safe_rejects_unapproved_keyword():
    original = "Built a backend service using Go."
    reworded = "Built a backend service using Go and Kubernetes."  # kubernetes not approved
    assert tr.reword_is_safe(original, reworded, approved_keywords=["go"]) is False


def test_reword_is_safe_accepts_valid_rewording():
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    reworded = "Owned a production Go API gateway, routing traffic across 12 microservices."
    assert tr.reword_is_safe(original, reworded, approved_keywords=["go", "microservices"]) is True


def test_llm_reword_bullet_uses_llm_output_when_safe(monkeypatch):
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Owned a Go API gateway routing traffic across 12 microservices, with RESTful design throughout.",
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go", "rest"], hedged_keywords=[])
    assert "RESTful" in result or "REST" in result


def test_llm_reword_bullet_falls_back_to_original_on_unsafe_output(monkeypatch):
    original = "Built and owned a Go-based API gateway routing traffic across 12 microservices."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            "Built and owned a Kubernetes-based API gateway routing traffic across 50 microservices.",
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go"], hedged_keywords=[])
    assert result == original  # unsafe (unapproved keyword + changed number) -> unchanged


def test_llm_reword_bullet_falls_back_to_original_on_llm_failure(monkeypatch):
    original = "Built and owned a Go-based API gateway."
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20: None,
    )
    result = tr.llm_reword_bullet(original, direct_keywords=["go"], hedged_keywords=[])
    assert result == original
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'reword_is_safe'` (and `llm_reword_bullet`).

- [ ] **Step 3: Implement**

Add after `inject_keyword_emphasis` in `scripts/tailor_resume.py`:

```python
NUMBER_RE = re.compile(r"\d+(?:\.\d+)?%?")

def reword_is_safe(original, reworded, approved_keywords):
    """Mechanically validate an LLM-reworded bullet before it's ever used:
    every number/percentage in the original must appear unchanged in the
    reworded version, and no KEYWORD_CANDIDATES term may appear in the
    reworded version unless it was already in the original OR is in
    approved_keywords. This is a code check, not the LLM's word -- the
    LLM cannot talk its way past it.
    """
    original_numbers = set(NUMBER_RE.findall(original))
    reworded_numbers = set(NUMBER_RE.findall(reworded))
    if not original_numbers.issubset(reworded_numbers):
        return False

    approved = {kw.lower() for kw in approved_keywords}
    original_lower = original.lower()
    for candidate in KEYWORD_CANDIDATES:
        if keyword_in_text(candidate, reworded) and not keyword_in_text(candidate, original_lower):
            if candidate not in approved:
                return False

    # Balanced \textbf{...} braces -- an unbalanced brace would break the
    # LaTeX compile downstream.
    if reworded.count("{") != reworded.count("}"):
        return False

    return True

def llm_reword_bullet(bullet_text, direct_keywords, hedged_keywords):
    """Reword one already-selected bullet's phrasing to naturally surface
    the given pre-approved keywords -- selection is untouched (this never
    picks which keywords are fair game, only how to phrase the ones it's
    handed). Falls back to the original bullet unchanged on any LLM
    failure or safety-check rejection (see reword_is_safe): a phrasing
    task, never a content-invention task.
    """
    approved = direct_keywords + hedged_keywords
    if not approved:
        return bullet_text

    direct_list = ", ".join(canonical_case(k) for k in direct_keywords) or "none"
    hedged_list = ", ".join(canonical_case(k) for k in hedged_keywords) or "none"

    reworded = call_opencode(
        system_prompt=(
            "You reword a single resume bullet point to naturally surface "
            "specific keywords for a job application, for a recruiter "
            "scanning resumes in 5-10 seconds. Rules, all mandatory: "
            "(1) Preserve every number and percentage from the original "
            "bullet exactly. "
            "(2) Do not invent any new action, tool, metric, or outcome "
            "not already in the original bullet. "
            f"(3) You may plainly state these keywords if relevant: {direct_list}. "
            f"(4) These keywords may ONLY appear with hedging language like "
            f"'exposure to' or 'working knowledge of', never as if hands-on: {hedged_list}. "
            "(5) Preserve any LaTeX \\textbf{...} markup structure (balanced braces). "
            "(6) Return ONLY the reworded bullet text, no commentary, no quotes."
        ),
        user_content=bullet_text,
    )

    if not reworded:
        return bullet_text

    reworded = reworded.strip().strip('"')
    if not reword_is_safe(bullet_text, reworded, approved):
        log("WARN: llm_reword_bullet output failed safety check, falling back to original")
        return bullet_text

    return reworded
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add llm_reword_bullet with mechanical numeric/keyword safety check"
```

---

## Task 14: Refactor `main()` into `process_job()` + `--job-id` CLI flag

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `process_job(job: dict, tailored: dict) -> tuple[dict, str]` (returns the updated `tailored` dict and one of `"sent"`, `"failed"`, `"skipped_non_eng"`).
- Consumes: everything from Tasks 6-13, plus existing `is_engineering_role`, `company_slug`, `slugify`, `generate_resume`, `compile_tex`, `get_page_count`, `update_job_status`, `save_tailored`.

This is the largest single refactor — moving `main()`'s existing per-job block (JD fetch through Telegram send) into a standalone function, with no behavior change for the batch path, so both the cron loop and the new `--job-id` one-off path share it.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`. This test stubs out the expensive parts (compile, LLM calls) so it runs fast and exercises the control flow, not `tectonic`:

```python
def test_process_job_skips_non_engineering_role(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    job = {"id": 1, "company_name": "Acme", "title": "Account Executive", "url": "https://example.com/1"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "skipped_non_eng"
    assert tailored["1"]["status"] == "ignored"


def test_process_job_marks_failed_when_compile_fails(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "output")
    monkeypatch.setattr(
        tr, "fetch_greenhouse_job", lambda slug, gh_id: None,
    )
    monkeypatch.setattr(
        tr, "fetch_jd_generic",
        lambda url: {"title": "", "company_name": "", "content_text": "Backend Engineer role.",
                      "content_html": "", "location": "", "absolute_url": url},
    )
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (False, "fake LaTeX error"))

    job = {"id": 2, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/2"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "failed"
    assert tailored["2"]["status"] == "failed"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v -k process_job`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'process_job'`.

- [ ] **Step 3: Implement `process_job`**

Read `main()`'s current per-job loop body first (`sed -n '837,988p' scripts/tailor_resume.py`) to copy its exact logic — the following is that same logic moved into a function, with the new JD-fetch fallback (Task 9), `llm_judge` (Task 10), and unchanged compile/retry/scoring structure:

```python
def process_job(job, tailored):
    """Process one job dict from the jobs table: fetch JD (Greenhouse API,
    falling back to fetch_jd_generic for anything else), generate and
    compile the resume, score it, judge it, and send Telegram. Mutates and
    returns `tailored` (the job-id-keyed tracker dict) plus one of
    "sent"/"failed"/"skipped_non_eng". Shared by main()'s batch loop and
    the --job-id one-off path (see the bottom of this file) -- unlike
    rebuild_one(), this works for a job with NO pre-existing tracker
    entry, which a fresh manual submission always starts as.
    """
    jid = str(job["id"])
    company = job.get("company_name") or "Unknown"
    title = job.get("title") or "Unknown"
    url = job.get("url") or ""

    log(f"\n--- Processing ID {jid}: {company} — {title} ---")

    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = None
    if gh_id and gh_slug:
        jd_data = fetch_greenhouse_job(gh_slug, gh_id)

    jd_unavailable = False
    if not jd_data:
        jd_data = fetch_jd_generic(url)
    if not jd_data or len(jd_data.get("content_text", "")) < MIN_USABLE_JD_CHARS:
        log(f"WARN: Could not fetch usable JD for {jid}; using title only")
        jd_unavailable = True
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }

    jd_text = jd_data.get("content_text", "")

    if not is_engineering_role(title, jd_text):
        log(f"SKIPPED (non-engineering): {title}")
        tailored[jid] = {
            "company": company, "title": title, "url": url,
            "pdf_path": None, "tex_path": None, "coverage_score": None,
            "status": "ignored",
            "error": "Non-engineering role; facts.md does not support claims. Skipped per truth lock.",
            "tailored_date": datetime.now().isoformat(),
        }
        save_tailored(tailored)
        update_job_status(jid, "ignored")
        return tailored, "skipped_non_eng"

    comp_slug = company_slug(company, url)
    title_slug = slugify(title)
    out_dir = OUTPUT_ROOT / comp_slug
    out_dir.mkdir(parents=True, exist_ok=True)
    base_name = f"mayur_athavale_resume_{comp_slug}_{title_slug}_{DATE_STR}"
    tex_path = out_dir / f"{base_name}.tex"
    pdf_path = out_dir / f"{base_name}.pdf"

    attempt = 0
    success = False
    final_error = None
    resume_text = ""
    jd_keywords = []

    while attempt < 2 and not success:
        attempt += 1
        tight = (attempt == 2)
        log(f"  Attempt {attempt} (tight={tight})...")

        tex_content, focus, jd_keywords = generate_resume(job, jd_data, tight=tight)
        with open(tex_path, "w", encoding="utf-8") as f:
            f.write(tex_content)

        ok, output = compile_tex(tex_path)
        if not ok:
            log(f"  Compile failed: {output[:500]}")
            final_error = output
            continue

        pages = get_page_count(pdf_path)
        log(f"  Pages: {pages}")
        if pages != 1:
            log(f"  PDF has {pages} pages; retrying with tighter content")
            final_error = f"Page overflow: {pages} pages"
            continue

        resume_text = tex_content
        success = True

    if not success:
        log(f"  FAILED after 2 attempts: {(final_error or '')[:200]}")
        tailored[jid] = {
            "company": company, "title": title, "url": url,
            "pdf_path": None, "tex_path": str(tex_path) if tex_path.exists() else None,
            "coverage_score": None, "status": "failed", "error": final_error,
            "tailored_date": datetime.now().isoformat(),
        }
        save_tailored(tailored)
        return tailored, "failed"

    tiered = score_coverage_tiered(jd_keywords, resume_text)
    score, covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    log(f"  Coverage score: {score} ({len(covered)}/{len(jd_keywords)} keywords)")

    judgment = llm_judge(jd_text, resume_text, title, company)

    tailored[jid] = {
        "company": company, "title": title, "url": url,
        "pdf_path": str(pdf_path), "tex_path": str(tex_path),
        "coverage_score": score, "status": "done", "error": None,
        "tailored_date": datetime.now().isoformat(),
        "keywords": jd_keywords, "covered_keywords": covered,
        "not_covered_keywords": not_covered, "not_truthful_keywords": not_truthful,
        "hedged_keywords": tiered["hedged"],
        "verdict": judgment["verdict"], "verdict_source": judgment["source"],
        "verdict_reason": judgment["reason"], "missing_keywords": judgment["missing_keywords"],
        "jd_unavailable": jd_unavailable,
    }
    save_tailored(tailored)

    log(f"  Sending Telegram notification...")
    tg_ok, tg_error = send_telegram(pdf_path, company, title, url, score, jid, judgment=judgment,
                                     hedged_keywords=tiered["hedged"], jd_unavailable=jd_unavailable)
    if not tg_ok:
        log(f"  Telegram failed: {tg_error}")
        tailored[jid]["telegram_error"] = tg_error
        save_tailored(tailored)
        return tailored, "failed"

    tailored[jid]["telegram_error"] = None
    save_tailored(tailored)
    update_job_status(jid, "shortlisted")
    return tailored, "sent"
```

- [ ] **Step 4: Replace `main()`'s loop body to call `process_job`**

Replace the entire per-job `for job in jobs[:to_process + 5]:` loop body in `main()` (everything between the `for` line and the rate-limit `time.sleep` block) with:

```python
    for job in jobs[:to_process + 5]:
        if processed >= to_process:
            break

        tailored, outcome = process_job(job, tailored)

        if outcome == "sent":
            processed += 1
        elif outcome == "failed":
            failed += 1
        elif outcome == "skipped_non_eng":
            skipped_non_eng += 1

        if outcome == "sent" and processed < to_process:
            log(f"  Rate limit: sleeping {RATE_LIMIT_SECONDS}s...")
            time.sleep(RATE_LIMIT_SECONDS)
```

- [ ] **Step 5: Add the `--job-id` CLI flag**

In the `if __name__ == "__main__":` block at the bottom of `scripts/tailor_resume.py`, add a new branch alongside the existing `--rebuild` one:

```python
if __name__ == "__main__":
    if len(sys.argv) > 1 and sys.argv[1] == "--rebuild":
        # ... existing --rebuild handling, unchanged ...
    elif len(sys.argv) > 1 and sys.argv[1] == "--job-id":
        if len(sys.argv) < 3:
            print("Usage: tailor_resume.py --job-id <job_id>", file=sys.stderr)
            sys.exit(1)
        _job_id = sys.argv[2]
        if not shutil.which("tectonic") or not shutil.which("pdfinfo"):
            print("ERROR: tectonic/pdfinfo not found", file=sys.stderr)
            sys.exit(1)
        _conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
        _conn.row_factory = sqlite3.Row
        _row = _conn.execute("SELECT * FROM jobs WHERE id=?", (int(_job_id),)).fetchone()
        _conn.close()
        if not _row:
            print(f"JOB_ID_NOT_FOUND: {_job_id}", file=sys.stderr)
            sys.exit(1)
        _tailored = load_tailored()
        _tailored, _outcome = process_job(dict(_row), _tailored)
        if _outcome == "failed":
            print(f"PROCESS_JOB_FAILED: {_tailored.get(_job_id, {}).get('error', 'unknown error')}", file=sys.stderr)
            sys.exit(1)
        print(f"PROCESS_JOB_{_outcome.upper()}")
    else:
        main()
```

- [ ] **Step 6: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS (all tests so far).

- [ ] **Step 7: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Refactor main() into process_job(); add --job-id one-off CLI path"
```

---

## Task 15: Update `send_telegram()` caption with verdict/hedged/missing

**Files:**
- Modify: `scripts/tailor_resume.py`
- Modify: `scripts/test_tailor_resume.py`

**Interfaces:**
- Modifies: `send_telegram(pdf_path, company, title, url, score, job_id, reply_to_message_id=None, judgment=None, hedged_keywords=None, jd_unavailable=False)` — new optional keyword args, existing callers (`rebuild_one`) keep working unchanged since they're optional with safe defaults.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_send_telegram_caption_includes_verdict_and_missing(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TG_TOKEN", "fake-token")
    monkeypatch.setattr(tr, "TG_CHAT", "fake-chat")

    captured = {}
    def fake_run(cmd, capture_output, text, timeout):
        captured["cmd"] = cmd
        class FakeResult:
            stdout = '{"ok": true}'
        return FakeResult()
    monkeypatch.setattr(tr.subprocess, "run", fake_run)

    pdf_path = tmp_path / "resume.pdf"
    pdf_path.write_bytes(b"%PDF-fake")

    judgment = {"verdict": "screen", "reason": "Strong match.", "source": "llm", "missing_keywords": ["kubernetes", "graphql"]}
    ok, err = tr.send_telegram(
        pdf_path, "Acme", "Backend Engineer", "https://example.com/job", 0.8, "42",
        judgment=judgment, hedged_keywords=["kafka"], jd_unavailable=False,
    )
    assert ok is True
    caption_arg_index = captured["cmd"].index("-F", captured["cmd"].index("document=@" + str(pdf_path))) 
    caption = next(v for v in captured["cmd"] if v.startswith("caption="))
    assert "SCREEN" in caption
    assert "Strong match." in caption
    assert "kubernetes" in caption and "graphql" in caption
    assert "Kafka" in caption or "kafka" in caption
    assert "#J42" in caption


def test_send_telegram_caption_flags_jd_unavailable(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TG_TOKEN", "fake-token")
    monkeypatch.setattr(tr, "TG_CHAT", "fake-chat")

    def fake_run(cmd, capture_output, text, timeout):
        class FakeResult:
            stdout = '{"ok": true}'
        return FakeResult()
    monkeypatch.setattr(tr.subprocess, "run", fake_run)

    pdf_path = tmp_path / "resume.pdf"
    pdf_path.write_bytes(b"%PDF-fake")

    ok, _ = tr.send_telegram(
        pdf_path, "Acme", "Backend Engineer", "https://example.com/job", 0.3, "43",
        judgment=None, hedged_keywords=None, jd_unavailable=True,
    )
    assert ok is True
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v -k send_telegram`
Expected: FAIL — `TypeError: send_telegram() got an unexpected keyword argument 'judgment'`.

- [ ] **Step 3: Implement**

Replace `send_telegram`'s signature and caption-building logic in `scripts/tailor_resume.py`:

```python
def send_telegram(pdf_path, company, title, url, score, job_id, reply_to_message_id=None,
                   judgment=None, hedged_keywords=None, jd_unavailable=False):
    """Send Telegram notification with PDF document."""
    if not TG_TOKEN or not TG_CHAT:
        return False, "Telegram credentials not configured"

    lines = [f"{company} — {title}"]

    if judgment:
        verdict_label = "SCREEN ✅" if judgment["verdict"] == "screen" else "REJECT-RISK ⚠️"
        source_note = "" if judgment["source"] == "llm" else " (rule-based, LLM unavailable)"
        lines.append(f"Verdict: {verdict_label}{source_note} — {judgment['reason']}")

    lines.append(f"Coverage: {score}/1.0")

    if hedged_keywords:
        lines.append(f"Hedged (adjacent/familiar): {', '.join(canonical_case(k) for k in hedged_keywords)}")

    if judgment and judgment.get("missing_keywords"):
        lines.append(f"Missing: {', '.join(judgment['missing_keywords'][:5])}")

    if jd_unavailable:
        lines.append("⚠️ JD text unavailable — coverage/verdict unreliable, resume generated from title only")

    lines.append(f"Apply: {url}")
    lines.append(f"#J{job_id}")
    caption = "\n".join(lines)

    cmd = [
        "curl", "-s", "-X", "POST",
        f"https://api.telegram.org/bot{TG_TOKEN}/sendDocument",
        "-F", f"chat_id={TG_CHAT}",
        "-F", f"document=@{pdf_path}",
        "-F", f"caption={caption}",
    ]
    if reply_to_message_id:
        cmd += ["-F", f"reply_to_message_id={reply_to_message_id}"]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=60)
        resp = json.loads(result.stdout)
        if resp.get("ok"):
            return True, None
        else:
            return False, f"Telegram API error: {resp.get('description', result.stdout)}"
    except Exception as e:
        return False, str(e)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: PASS — all tests, including the pre-existing `rebuild_one`-driven call to `send_telegram` (which passes no new kwargs, so `judgment=None`/`hedged_keywords=None`/`jd_unavailable=False` defaults keep its caption exactly as before).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add verdict/hedged/missing-keywords lines to Telegram caption"
```

---

## Task 16: Config wiring (`OPENCODE_API_KEY`) + deploy notes

**Files:**
- Modify: `.env` (local only, gitignored — not committed)
- Modify: `CLAUDE.md`

**Interfaces:** none (config/docs only).

- [ ] **Step 1: Add the key to the local `.env`**

Add a line to `/home/dev-mayur/jobwatch/.env` (already gitignored, confirmed via `git check-ignore .env` before editing):

```bash
export OPENCODE_API_KEY="sk-9EXVnUITSOj7TA6axwr7CPQqWxSyxxfPEPUzlIviomoZrdKyismKnW3W2xbkHf7N"
```

(Value copied from `creds.yml`'s `opencode-go.api_key` — same key already used for local `opencode` CLI usage.)

- [ ] **Step 2: Verify it's picked up**

Run: `cd /home/dev-mayur/jobwatch && source .env && python3 -c "import os; print('OK' if os.environ.get('OPENCODE_API_KEY') else 'MISSING')"`
Expected: prints `OK`.

- [ ] **Step 3: Add a deploy-time reminder to `CLAUDE.md`**

Add a bullet to `CLAUDE.md`'s "Recurring gotchas" section:

```markdown
- **`OPENCODE_API_KEY` must exist in the server's `/opt/jobwatch/.env`, not just the laptop's.** Added for JD-extraction/verdict LLM calls in `tailor_resume.py` — without it, every job silently falls back to rule-based scoring (no error, just a `(rule-based, LLM unavailable)` tag in the Telegram message), so it's easy to deploy and not notice it's missing. Check with the same `source .env && echo $OPENCODE_API_KEY` pattern used to verify `JOBWATCH_TG_TOKEN` today.
```

- [ ] **Step 4: Commit**

```bash
cd /home/dev-mayur/jobwatch
git add CLAUDE.md
git commit -m "Document OPENCODE_API_KEY deploy requirement in CLAUDE.md"
```

(`.env` itself is gitignored and never committed — this step only commits the doc note.)

---

## Task 17: Full verification — real Keka URL, full test suite, gofmt

**Files:** none created/modified — verification only.

- [ ] **Step 1: Run the full Go test suite**

Run: `cd /home/dev-mayur/jobwatch && go test ./...`
Expected: all packages PASS, including `internal/jobsubmit` (Task 1), `internal/tgsync` (Task 2), `internal/web` (Task 3).

- [ ] **Step 2: Confirm `gofmt` is clean**

Run: `cd /home/dev-mayur/jobwatch && gofmt -l .`
Expected: no output.

- [ ] **Step 3: Run the full Python test suite**

Run: `cd /home/dev-mayur/jobwatch && pytest scripts/test_tailor_resume.py -v`
Expected: all tests from Tasks 6-15 PASS.

- [ ] **Step 4: Build everything**

Run:
```bash
cd /home/dev-mayur/jobwatch
go build -o bin/jobwatch ./cmd/jobwatch
cd frontend && npm run build && cd ..
```
Expected: both build cleanly.

- [ ] **Step 5: Real end-to-end verification against the actual Keka URL**

This is the step that matters most per this project's existing convention (see the `feedback: verify_generated_output` memory — compile/render or a live API call before claiming done, never just "the code looks right"):

```bash
cd /home/dev-mayur/jobwatch
source .env
./bin/jobwatch serve -config config.yaml &
SERVER_PID=$!
sleep 1
curl -s -X POST http://127.0.0.1:8787/api/jobs/manual \
  -H "Content-Type: application/json" \
  -d '{"url":"https://valorem.keka.com/careers/jobdetails/124256"}'
echo
sleep 5
tail -50 logs/cron.log
```

Expected: the `curl` prints a `202` response with a job id; `logs/cron.log` shows `tailor_resume.py --job-id N` output ending in `PROCESS_JOB_SENT` (or a clearly-logged `PROCESS_JOB_FAILED` with a real reason — either is acceptable as "verified," a silent hang is not). Check the actual Telegram chat for a message with the new PDF attached, the verdict line, coverage score, and — critically — confirm whether `jd_unavailable` triggered (this URL may well be a JS-rendered SPA page per the spec's flagged limitation; if so, the message should clearly say so rather than silently pretend the JD was read). Kill the server after: `kill $SERVER_PID`.

- [ ] **Step 6: Report results**

Document in the final commit message or a follow-up message to Mayur: did the real Keka URL's JD extract successfully (free scrape, or LLM fallback), or did it hit the JS-rendered-SPA limitation flagged in the spec? This determines whether a follow-up (headless-browser fetch) is worth scoping later.

No commit for this task — it's verification only, confirming Tasks 1-16's commits together deliver a working feature end-to-end.

---

## Self-Review Notes

**Spec coverage check:**
- UI add-job-link input → Task 4. ✓
- Shared insert/dedupe logic (no duplication between Telegram + UI paths) → Tasks 1-2. ✓
- Immediate one-off tailoring trigger (not waiting for cron) → Task 3 (`tailor-one.sh` + detached exec), Task 14 (`--job-id` path). ✓
- Generic JD extraction with LLM fallback for non-Greenhouse sources → Tasks 7, 9. ✓
- LLM verdict (screen/reject-risk + missing keywords + reason) with rule-based fallback → Task 10. ✓
- Honesty tiers (direct/hedged-familiar/hedged-adjacent/fabrication-risk) + curated adjacency map in facts.md → Tasks 5, 6. ✓
- Resume content selection stays deterministic; only phrasing reworded, mechanically validated → Tasks 11 (skills reorder), 12 (hedged injection), 13 (bullet reword + safety check). ✓
- Telegram caption updated with verdict/hedged/missing/jd_unavailable → Task 15. ✓
- `OPENCODE_API_KEY` config + deploy note → Task 16. ✓
- Known JS-rendered-SPA limitation flagged, not silently ignored → Task 17 Step 5's explicit check. ✓

**Placeholder scan:** no TBD/TODO markers; every step shows real code. Task 11's Step 1 asks the implementer to read existing code before writing category-list-wrapping edits (the exact category names aren't duplicated in this plan since they're long pre-existing literals) — this is a real, actionable instruction with a concrete helper function and wrapping pattern given, not a vague "add appropriate handling."

**Type/signature consistency:** `process_job(job, tailored) -> (tailored, outcome)` used identically in Task 14's Steps 3-5. `score_coverage_tiered` return dict keys (`score`, `direct`, `hedged`, `missing`) used consistently in Tasks 6, 12, 14. `send_telegram`'s new kwargs (`judgment`, `hedged_keywords`, `jd_unavailable`) match between Task 14's call site and Task 15's signature. `jobsubmit.InsertManualJob`'s 5-value return (`id, alreadyExisted, company, title, err`) matches across Tasks 1, 2, 3.
