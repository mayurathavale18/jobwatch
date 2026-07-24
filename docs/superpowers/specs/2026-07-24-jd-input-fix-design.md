# Fix mobile silent-fail on manual job-link submit + JD text/file ingestion

Date: 2026-07-24
Status: approved, ready for implementation planning

## Problem

The dashboard's "add job link" input (`frontend/src/pages/Jobs.tsx`) silently
fails on mobile with no visible error: tapping "Add & Tailor" does nothing —
no spinner, no message, no page change. On desktop it works fine.

Root cause: the input is `<input type="url" required>`. When the pasted
value fails the browser's native URL-constraint check — a stray trailing
newline/whitespace from a mobile clipboard paste, a missing scheme, or any
other native-validation quirk — the browser cancels the form's `submit`
event *before* React's `onSubmit` handler (`handleSubmitManualJob`) ever
runs. Some mobile browsers/layouts never render the native validation
bubble at all (or it renders off-screen in this component's flex layout),
so the failure is completely invisible. The existing `try/catch` around
`submitManualJob` in `handleSubmitManualJob` never even gets a chance to
run, since the JS handler itself is never invoked.

Separately: every job submitted through this path (and every job jobwatch
polls from a source it can't `fetch_jd_generic`-scrape cleanly — Workday,
Naukri, and others already documented as blocking automated access) only
gets real JD text if the scrape+LLM-fallback in `tailor_resume.py` happens
to work. When it doesn't, tailoring falls back to title-only, which
produces near-meaningless coverage scoring. There's no way today to just
hand the tool the JD text directly.

## Goals

- The manual job-link submit must never fail silently. Every outcome
  (success, validation failure, network/server failure) surfaces through
  the existing `manualStatus` line, on both desktop and mobile.
- Add an optional way to supply JD text directly at submission time —
  paste into a textarea, or upload a `.txt`/`.md` file (read client-side,
  dropped into the same textarea) — that tailor_resume.py uses verbatim
  instead of attempting its own scrape, for jobs where scraping is
  unreliable anyway.
- The job URL stays required (job identity/dedupe/the "Apply" link in
  Telegram all depend on it) — the JD text is a supplement to the URL
  flow, not a replacement for it.

## Non-goals

- No PDF ingestion — `.txt`/`.md` only, read as plain text client-side.
  No new server-side parsing dependency.
- No URL-less job submission. A job always has a URL; JD text is optional
  supplementary content on top of it.
- No change to how polled (non-manual) jobs fetch JD text — this only
  affects the manual-submit path (dashboard + Telegram bare-URL forward,
  both of which already share `jobsubmit.InsertManualJob`).

## Design

### 1. Mobile silent-fail fix (`frontend/src/pages/Jobs.tsx`)

- Add `noValidate` to the `<form className="add-job">` so the browser
  never silently intercepts submission via native constraint validation.
- In `handleSubmitManualJob`, before calling `submitManualJob`:
  - `trim()` the value (defends against clipboard-introduced leading/
    trailing whitespace or a trailing newline — a known mobile
    copy-from-job-posting behavior).
  - Validate the http(s) prefix client-side (mirrors the existing check
    in `internal/web/api.go`'s `handleAPIJobsManual`) and set
    `manualStatus` to a clear message (e.g. `"Enter a valid http(s) URL."`)
    on failure, returning early — same visible-failure path the network
    error case already uses.
- Keep `type="url"` on the input (still gets the right mobile keyboard
  layout), but validation authority moves entirely to the JS handler.

### 2. JD text/file input (`frontend/src/pages/Jobs.tsx`)

- New optional block under the existing URL input, inside the same
  `<form className="add-job">`:
  - A `<textarea>` for pasting JD text directly ("Paste JD text
    (optional) — used instead of scraping the link").
  - A `<input type="file" accept=".txt,.md">` next to it. `onChange`
    reads the file via `FileReader.readAsText` and sets the textarea's
    value to the result (overwrites, doesn't append — one JD text source
    at a time).
- `handleSubmitManualJob` passes the textarea's (trimmed) value through
  to `submitManualJob` as a new optional second argument.

### 3. API + backend plumbing

- `frontend/src/api.ts`: `submitManualJob(url: string, jdText?: string)`
  includes `jdText` in the POST body when non-empty.
- `internal/web/api.go`: `apiManualJobRequest` gains `JDText string
  \`json:"jdText"\`` ; `handleAPIJobsManual` trims it and passes it through
  to `jobsubmit.InsertManualJob`.
- `internal/jobsubmit/jobsubmit.go`: `InsertManualJob` gains a `jdText
  string` parameter, stored into a new `manual_jd_text` column on the
  `jobs` row at insert time. The Telegram bare-URL-forward path
  (`internal/tgsync/tgsync.go`'s `addManualJob`) keeps calling
  `InsertManualJob` with `jdText=""` — Telegram has no way to attach JD
  text today, out of scope for this change.
- `internal/store/store.go`: `migrate()` currently only has `CREATE TABLE
  IF NOT EXISTS`, no precedent yet for adding a column to an existing
  table. Add: `ALTER TABLE jobs ADD COLUMN IF NOT EXISTS manual_jd_text
  TEXT NOT NULL DEFAULT ''` (bundled SQLite is v1.53.0 / modernc.org,
  well past the 3.35.0 baseline that added `IF NOT EXISTS` support for
  `ADD COLUMN`, so no manual duplicate-column-error handling is needed).

### 4. Consumption in `tailor_resume.py`

- `process_job()`: today, for a `manual` provider job, it always calls
  `fetch_jd_generic(url)` (with its own scrape + LLM-fallback + title-only
  fallback chain). Add one check before that: if
  `job.get("manual_jd_text")` is non-empty, build `jd_data` directly from
  it (`content_text = job["manual_jd_text"]`) and skip `fetch_jd_generic`
  entirely — no scrape attempt, no LLM-fallback call, just the
  user-supplied text used verbatim.
- `rebuild_one()` re-reads the job row from the DB on every call
  (`SELECT * FROM jobs WHERE id=?`), so it picks up `manual_jd_text` the
  same way with no extra plumbing — a `fix`/`update` reply on a job
  submitted with JD text keeps using that text, not a fresh scrape.
- No change to `fetch_jd_generic` itself, and no change to the Greenhouse
  path (`fetch_greenhouse_job` still takes priority when the URL parses as
  a Greenhouse job — user-supplied text only kicks in for the "everything
  else" branch, i.e. exactly the sources this feature exists for).

## Testing

- **Go** (`internal/web`, `internal/jobsubmit`): request/response
  round-trip test for `jdText` flowing from `apiManualJobRequest` through
  to the inserted `jobs` row; empty-`jdText` case still inserts identically
  to today (backward compatible).
- **Go** (`internal/store`): migration test — opening a fresh DB creates
  the column; opening an *existing* pre-migration DB (one created before
  this change) successfully adds the column without erroring, and
  existing rows read back `manual_jd_text = ''`.
- **Python** (`scripts/test_tailor_resume.py`): `process_job` test
  asserting that a job dict with a non-empty `manual_jd_text` skips
  `fetch_jd_generic` entirely (fail-if-called stub, same pattern already
  used for `test_fetch_jd_generic_skips_llm_when_scrape_is_already_usable`)
  and uses the supplied text as `content_text`.
- **Frontend**: no existing test harness for this page (no Vitest/RTL
  setup in `frontend/`) — verified manually via `webapp-testing`/browser
  check: trim+validation behavior, file-read-into-textarea behavior, and
  that `noValidate` doesn't regress the desktop happy path.
