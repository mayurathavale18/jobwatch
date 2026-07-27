# Founder-finder + Gmail draft outreach

Date: 2026-07-27
Status: approved, ready for implementation planning

## Problem

For startup jobs (0-20 people) in crypto/web3/DeFi/fintech/AI, Mayur wants
jobwatch to find the company's founders and draft a personalized outreach
email in his own Gmail — draft only, never auto-sent, matching the
project's existing human-in-the-loop pattern (nothing auto-applies or
auto-sends today either).

This directly supersedes a 2026-07-19 decision ("role-based emails only,
no scraping" — named-person lookup was flagged as ban-risk). Re-raised
with Mayur this session: he wants named-founder addressing, is open to
either scraping a company's own site or a paid lookup API, and explicitly
does **not** want a manually-maintained company-size allowlist — company
size (0-20 people) must be determined automatically.

No part of today's schema carries employee count, founder name/email, or
sector/industry tagging. The curated `config.yaml` company list is mostly
large companies (Stripe, Coinbase, Binance, ...) — qualifying startups
will mostly come through the aggregator sources (RemoteOK, WWR,
web3career), where the company set is dynamic, not pre-taggable.

## Goals

- For a job that (a) is in the target sector and (b) is at a company with
  <=20 employees, automatically find a named founder (name + email) and
  draft a personalized, JD-and-résumé-aware outreach email as a Gmail
  draft, with the tailored resume PDF attached — no auto-send.
- Single combined data source for both signals (employee count + founder
  name/email) rather than juggling multiple APIs.
- Reuse the LLM call `tailor_resume.py` already makes per job (JD
  extraction/verdict) rather than adding new LLM round-trips wherever
  reasonably possible.
- Support the automatic pipeline path AND two manual trigger paths
  (Telegram command, dashboard button), the latter two accepting an
  explicit founder-email override so a miss on the automatic lookup isn't
  a dead end.
- New jobs only — no backfill against the ~4300 existing DB rows.

## Non-goals

- Not scraping LinkedIn or any social network for founder identity — the
  ban-risk that killed the 2026-07-19 approach applies specifically to
  LinkedIn/social scraping, not to a paid enrichment API's own dataset.
- Not auto-sending anything, ever. Gmail Drafts only.
- Not building a company-size manual allowlist (explicitly ruled out).
- Not solving sector/size detection for the ~4300 already-tracked jobs.

## Architecture

```
tailor_resume.py process_job(job, tailored), after existing verdict step:

  1. llm_judge() JSON schema gains a `sector` field
     ("crypto"|"web3"|"defi"|"fintech"|"ai"|null) — one more field on the
     existing OpenCode Go call, no new LLM round-trip for this step.

  2. if sector is not null and outreach_status in ('', 'failed'):
       apollo_lookup(company_name) -> (employee_count, founder_name, founder_email)
         - Apollo.io Organization Search + People Search by title
           (Founder/Co-Founder/CEO), matched by company NAME only (no
           domain resolution step — Greenhouse/Ashby postings live on the
           ATS's own domain, not the company's, so domain lookup isn't
           reliably derivable; name search is the agreed tradeoff).
         - New APOLLO_API_KEY in .env (same pattern as WEB3CAREER_API_TOKEN).

  3. gate: employee_count is not None and employee_count <= 20
           and founder_email is not None
     -> pass: continue to draft generation
     -> fail: set outreach_status accordingly (skipped_size /
        skipped_no_founder), log only, no Telegram message, return

  4. draft_outreach_email(jd_text, resume_text, founder_name, title, company)
     -> one more OpenCode Go call, returns {subject, body}

  5. create_gmail_draft(to=founder_email, subject, body, attach=resume_pdf_path)
     -> Gmail API, gmail.compose scope, OAuth2 refresh token

  6. on success: outreach_status='drafted', founder_name/email + timestamp
     persisted, Telegram notification sent ("Draft ready — {founder_name}
     @ {company}, {title} — check Gmail Drafts").
     on failure at any step 2/4/5: outreach_status='failed' (retryable —
     see Data model), logged, no Telegram noise for a transient failure.
```

Manual trigger paths run the same `process_job` outreach steps directly
(skipping the sector/size gate when an explicit founder email is
supplied), via a new CLI entry point — see "Components" below.

## Data model

Additive `ALTER TABLE jobs ADD COLUMN ...` in `internal/store/store.go`'s
`migrate()`, same idempotent pattern already used for `manual_jd_text`:

- `outreach_status TEXT NOT NULL DEFAULT ''` — one of `''`,
  `skipped_sector`, `skipped_size`, `skipped_no_founder`, `drafted`,
  `failed`.
- `founder_name TEXT NOT NULL DEFAULT ''`
- `founder_email TEXT NOT NULL DEFAULT ''`
- `outreach_drafted_at TEXT NOT NULL DEFAULT ''` (RFC3339, empty if never
  drafted)

**Retry semantics (deliberately different from `tailored.json`'s known
bug — see HANDOFF known-gap #1):** eligibility for the automatic step is
`outreach_status IN ('', 'failed')`, so a transient Apollo/Gmail API error
doesn't permanently block a job the way any `tailored.json` entry
(including `"status": "failed"`) permanently blocks resume retries today.
`skipped_sector`/`skipped_size`/`skipped_no_founder` are terminal for
*automatic* retry (the underlying fact won't change on its own) but are
overridable by a manual trigger that supplies its own founder email.

## Components

### Python: `scripts/tailor_resume.py`

- `llm_judge()`: extend the returned JSON schema with `sector`
  (nullable). No new call.
- New `apollo_lookup(company_name) -> ApolloResult | None`: two Apollo API
  calls (org search, people search), short timeout (~15s, matching the
  existing LLM call timeout convention), any failure/timeout returns
  `None` (treated as a miss, not a crash).
- New `draft_outreach_email(jd_text, resume_text, founder_name, title,
  company) -> {subject, body}`: one OpenCode Go call, same client/timeout
  pattern as `llm_judge`.
- New `create_gmail_draft(to, subject, body, resume_pdf_path)`: Gmail API
  client (`google-api-python-client` + `google-auth`), refreshes access
  token from `GMAIL_REFRESH_TOKEN` each call (no local token-cache file
  needed — refresh tokens don't expire under normal use in Testing mode).
  Builds a MIME message with the PDF as an attachment, calls
  `users.drafts.create`.
- `process_job()`: after the existing tailoring block, add the outreach
  steps described in Architecture above, gated on `outreach_status`.
- New CLI flag `--outreach --job-id N [--founder-email EMAIL]`: loads job
  N directly, runs only the outreach portion of `process_job` (JD text
  and tailored resume PDF must already exist for that job — this is a
  post-tailoring step, not a replacement for the batch/`--job-id`
  tailoring path). If `--founder-email` is given, skips the sector/size
  gate and Apollo lookup entirely, goes straight to draft generation with
  the supplied email.
- Telegram command handling (wherever `tgsync`/reply commands are
  parsed): new `outreach <job_id> [<email>]` command, shells to
  `python3 scripts/tailor_resume.py --outreach --job-id <id> [--founder-email <email>]`
  synchronously, replies with the result (drafted / failed reason).

### Go: dashboard trigger

- `internal/web/api.go`: `POST /api/jobs/{id}/outreach`, optional body
  `{"founderEmail": "..."}`. Spawns
  `python3 scripts/tailor_resume.py --outreach --job-id <id> [--founder-email ...]`
  the same way `handleAPICronRun` already spawns detached subprocesses
  (`exec.Command`, output to `logs/cron.log`, `cmd.Start()` +
  background `cmd.Wait()`, non-blocking response). Returns `202
  {"id": id}` immediately — result surfaces via the existing Telegram
  notification or a subsequent dashboard refresh of `outreach_status`.
- `internal/store/store.go`: `JobRow` gains the four new fields;
  `ListJobs`/`filterWhere` unchanged (no new filter dimension requested).

### Frontend

`frontend/src/pages/Jobs.tsx`: a "Draft outreach" action per job row
(button or menu item), shown regardless of current `outreach_status` (so
it doubles as the manual-override trigger). On click with no override,
POSTs with no body; a small inline "override founder email" input can be
expanded for the override case. Shows a toast ("Outreach queued") — no
polling, result arrives via Telegram like every other async job outcome
in this codebase.

## Gmail OAuth setup (one-time, outstanding from last session)

Still needs, in order (unchanged from HANDOFF.md, not yet done):

1. Google Cloud Console project (free), Gmail API enabled.
2. OAuth 2.0 Client ID, type **Desktop app** → `client_id` + `client_secret`.
3. OAuth consent screen in **Testing** mode, Mayur's email as test user,
   scope `gmail.compose` only.
4. New `scripts/gmail_auth_setup.py` (one-time local script): opens a
   browser, captures the OAuth code, exchanges for a refresh token,
   prints it for pasting into `.env`.

New env vars (both laptop and EC2 `.env`, same pattern as existing
secrets): `APOLLO_API_KEY`, `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`,
`GMAIL_REFRESH_TOKEN`.

## Error handling

- Apollo timeout/error/no-match → `None`, treated as a miss
  (`skipped_no_founder`), never crashes `process_job`.
- Gmail API error (expired/revoked token, malformed MIME, etc.) →
  `outreach_status='failed'`, retryable on next cron pass, logged with
  the actual error (so a revoked refresh token is visible in logs rather
  than silently retried forever).
- LLM draft-generation failure/timeout → same `failed` treatment; no
  fallback template (unlike `llm_judge`'s rule-based fallback) since a
  generic non-personalized email isn't worth sending as a "draft" — skip
  and retry next cycle instead.

## Testing

- Unit tests (Python): sector-gate logic, `outreach_status` transition
  rules (in particular: `failed` is retryable, `skipped_*` is not, for
  the automatic path), Apollo response parsing against a few captured
  sample payloads (mocked HTTP, not live calls in the test suite).
- `go test ./...` for the new endpoint (dedupe/response shape, detached
  spawn doesn't block) and the `JobRow`/store column additions.
- Manual end-to-end verification once Gmail OAuth is set up: run against
  one real qualifying job, confirm an actual draft appears in Gmail
  Drafts with the correct attachment — not just that `create_gmail_draft`
  returns 200. Per project convention (`feedback:
  verify_generated_output`), this must be checked against the real Gmail
  API and a real Apollo lookup, not mocked, before calling it done.
- `gofmt -l .` clean.

## Known limitations (flagged, not solved here)

- Apollo's free/low tier has lookup volume and data-freshness limits — a
  fast-growing startup's employee count may be stale by the time it's
  queried. Accepted tradeoff versus a manual allowlist per Mayur's
  explicit ask for automation.
- Company-name-only matching (no domain resolution) means very common
  company names could resolve to the wrong organization in Apollo. Not
  mitigated here — if this turns out to bite in practice, worth a
  follow-up spec adding domain derivation.
