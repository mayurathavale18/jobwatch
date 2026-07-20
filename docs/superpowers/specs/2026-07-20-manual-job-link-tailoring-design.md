# Manual job-link submission + LLM-assisted JD extraction/rating

Date: 2026-07-20
Status: approved, ready for implementation planning

## Problem

Right now the only way to add a job jobwatch can't poll directly (e.g. a
Keka posting like `https://valorem.keka.com/careers/jobdetails/124256`) is
forwarding a bare URL to the Telegram bot, which inserts it as a `manual`
job and lets it ride the 30-minute `tailor-resume` cron. Two gaps:

1. No way to submit from the dashboard UI — Telegram-only.
2. JD text fetch only works for Greenhouse (`fetch_greenhouse_job`, boards
   API). Every other source — including the manual-submit flow itself,
   Workday companies not explicitly wired up, and any arbitrary link like
   Keka — falls back to **title-only** text. Coverage scoring against a
   one-line title is close to meaningless, and there's evidence Mayur has
   been manually pasting JD text into scratch files (`resume/jd_*.txt`) as
   a workaround. The Telegram message today also only carries a raw
   coverage score, not a verdict or the actual missing keywords.

## Goals

- Add a job link from the dashboard UI, get the tailored resume + a real
  recruiter-lens verdict on Telegram within roughly 10-30 seconds, no cron
  wait.
- Extract full JD text from arbitrary posting URLs (not just Greenhouse),
  using free HTML scraping first, LLM fallback when the scrape is too
  thin.
- Add an actual recruiter/hiring-manager judgment call (screen vs
  reject-risk, missing keywords, one-line reason) to the Telegram message,
  using an LLM — the existing keyword-coverage math stays as a cheap
  rule-based cross-check, not a replacement.
- Make the generated resume itself more keyword-forward for a 5-10 second
  human scan, without weakening the existing truth-lock (never claim a
  skill absent from `facts.md`/`TRUTHFUL_SKILLS`) — while allowing
  clearly-hedged "familiarity with" / "exposure to" claims for tools
  that are in `facts.md`'s `familiar` tier or genuinely adjacent to a
  real production skill (see "Honesty tiers" below). Reword bullet
  *phrasing* per JD where it helps attention, without changing what
  experience is claimed.

## Non-goals

- Not letting an LLM *select or invent* resume content. It may reword an
  already-selected bullet's phrasing (see "Honesty tiers" and "Resume
  content changes" below), but never adds a metric, action, or outcome
  that isn't already in `facts.md` or the original bullet.
- Not solving JS-rendered/SPA job pages that return near-empty HTML on a
  plain GET. Flagged as a known limitation below, not solved by this spec.

## Honesty tiers for JD keyword claims

`facts.md` already defines three tiers per skill (`production` /
`used` / `familiar`, see its "Skills inventory" section), but
`tailor_resume.py`'s `TRUTHFUL_SKILLS` today treats them as one flat
"claimable" set — no distinction in how confidently something gets
worded. Revision: give JD-keyword handling four buckets instead of two.

1. **Direct-claim** (facts.md `production`/`used` tier, i.e. today's
   `TRUTHFUL_SKILLS`): stated plainly, no hedge — unchanged from today.
2. **Hedged-claim, in facts.md** (facts.md `familiar` tier — e.g. Kafka,
   GCP specifics, Kubernetes depth, LangChain, RabbitMQ, MongoDB,
   Firebase, tRPC, Next.js where marked `familiar`): may appear on the
   resume, but only with hedging language ("familiarity with", "working
   knowledge of", "exposure to") — never phrased as if hands-on/production.
3. **Hedged-claim, adjacent** (NEW — a JD keyword not in `facts.md` at
   all, but adjacent to a real `production`-tier skill): a small curated
   map, e.g. Terraform (production) → fair to claim exposure to Pulumi,
   Ansible, CloudFormation; Kubernetes (familiar) → Helm, ArgoCD. Curated
   by Mayur, mirrors how `TRUTHFUL_SKILLS`/`KEYWORD_CANDIDATES` are
   already hand-maintained in code rather than parsed from `facts.md` —
   same pattern, not a new parsing layer. Lives as a new `ADJACENCY_MAP`
   dict in `tailor_resume.py`, cross-referenced against a new "Adjacent
   tool exposure" section added to `facts.md` so both stay in sync the
   same way the Skills section duplication already requires (per
   HANDOFF's note on `master.tex` / `tailor_resume.py` staying matched).
4. **Everything else**: not added, stays in the LLM judge's/coverage's
   "missing" list — unchanged fabrication boundary.

`score_coverage` gains a third bucket alongside covered/not-covered:
`hedged` (buckets 2 and 3 above), so the Telegram message can show what
got added with a hedge, not just silently blend it into "covered."

## Architecture

```
UI (Jobs page: "Add job link" input)
  -> POST /api/jobs/manual {url}
  -> Go: InsertManualJob (shared helper, also used by tgsync)
  -> spawn detached: python3 scripts/tailor_resume.py --job-id N
  -> 202 {id, alreadyExisted} back to UI immediately

tailor_resume.py --job-id N:
  1. fetch_jd_generic(url): HTML GET, strip tags/script/style/nav/footer
     -> if resulting text < ~150 usable chars: one OpenCode Go LLM call
        to extract JD text from the raw HTML
     -> if still unusable: proceed with title-only, flag it in the
        Telegram message rather than silently pretending it worked
  2. generate_resume(): existing deterministic template, with tightened
     JD-keyword front-loading (see "Resume content changes")
  3. llm_judge(jd_text, resume_text): OpenCode Go call -> JSON
     {verdict: "screen"|"reject_risk", missing_keywords: [...], reason}
     -> on LLM failure/timeout, fall back to score_coverage threshold
        (< 0.4 => reject_risk) labeled as rule-based in the message
  4. score_coverage(): existing regex-based coverage score, kept as-is,
     sent alongside the LLM verdict (not replaced by it)
  5. send_telegram(): PDF + updated caption (see below)
```

## Components

### Go: shared manual-insert helper

`addManualJob`'s insert/dedupe logic currently lives only in
`internal/tgsync/tgsync.go`, coupled to a Telegram message reply. Extract
the store-facing part (slug derivation, `ExistsTx` dedupe check,
`InsertJob`) into a new small package, e.g. `internal/jobsubmit`, with:

```go
func InsertManualJob(ctx context.Context, st *store.Store, rawURL string) (id int64, alreadyExisted bool, err error)
```

Both `tgsync.addManualJob` (keeps its Telegram-reply wrapping) and the new
web handler call this. Avoids a second copy of the same dedupe/slug logic.

### Go: new endpoint

`internal/web/api.go` / `server.go`:

- `POST /api/jobs/manual` — body `{"url": "..."}`. Calls
  `jobsubmit.InsertManualJob`. On success (new insert), spawns
  `python3 scripts/tailor_resume.py --job-id <id>` detached (same pattern
  as `handleAPICronRun`: `exec.Command`, stdout/stderr to `logs/cron.log`,
  `cmd.Start()` + background `cmd.Wait()`, don't block the HTTP response
  on it). Returns `202 {"id": id, "alreadyExisted": false}`.
- If `alreadyExisted`, returns `200 {"id": id, "alreadyExisted": true}` —
  UI shows "already added" instead of re-triggering.

### Frontend

`frontend/src/pages/Jobs.tsx`: small form (URL text input + submit
button) near the top of the page. On submit: POST to
`/api/jobs/manual`, show a toast/inline message ("Added #J{id} —
resume incoming on Telegram" or "Already added that one"). No polling for
the tailoring result — it arrives via Telegram exactly like every other
job notification already does, so the UI doesn't need to track tailoring
state.

### Python: `scripts/tailor_resume.py`

- Refactor `main()`'s per-job processing block (JD fetch through Telegram
  send, roughly lines 837-986) into `process_job(job, tailored) ->
  (updated_tailored_entry, processed: bool)`, so the batch cron loop and
  the new single-job path share one implementation. This mirrors the
  existing relationship between `main()` and `rebuild_one()`, but
  `rebuild_one()` requires a pre-existing tracker entry — the new
  `--job-id` path needs to work for a job that has *no* tracker entry yet
  (fresh manual submission), so it reuses `process_job` instead.
- New CLI flag `--job-id N`: loads that one row directly (not gated by
  `status='new'` scan or `DAILY_BUDGET`/`MAX_PER_CYCLE`), runs
  `process_job` on it. Still respects the non-engineering truth-lock skip
  (`is_engineering_role`).
- New `fetch_jd_generic(url)`:
  - Plain `GET` with a browser-like `User-Agent`, same pattern as
    `fetch_greenhouse_job`'s request handling.
  - Strip `<script>`/`<style>`/nav/header/footer tags, convert block tags
    to newlines, decode entities, collapse whitespace — same approach as
    the (currently-unused, superseded) `tailor_helper.py:html_to_text`,
    cleaned up and adapted to REPO_ROOT-relative conventions.
  - If the result is under ~150 characters of real text (empty SPA shell,
    JS-rendered content, fetch blocked), fall back to one OpenCode Go
    call: send the raw HTML (truncated to a safe length) with a prompt
    asking it to extract the job description text (responsibilities,
    requirements, qualifications) as plain text.
  - If *that* still comes back too thin, proceed with title-only JD text
    (existing fallback behavior) but set a flag that changes the Telegram
    caption to say "JD text unavailable — coverage/verdict unreliable."
  - Greenhouse-sourced jobs keep using `fetch_greenhouse_job` (free, no
    LLM call) — `fetch_jd_generic` is only the fallback path for
    everything else, including `manual` provider jobs.
- New `llm_judge(jd_text, resume_text, title, company)`:
  - One OpenCode Go chat completion call. System prompt instructs it to
    act as a hiring manager/recruiter screening 100+ resumes with 5-10
    seconds per resume, and to return strict JSON:
    `{"verdict": "screen"|"reject_risk", "missing_keywords": [...],
    "reason": "<one sentence>"}`.
  - `missing_keywords` is informational only — it does not get
    auto-injected into the resume (that stays governed entirely by the
    existing `TRUTHFUL_SKILLS`-gated `inject_keyword_emphasis`, so the LLM
    can never cause a fabricated skill to land in the resume).
  - On any failure (timeout, malformed JSON, non-2xx): fall back to
    `score_coverage`'s existing score with a threshold (`< 0.4` ->
    `reject_risk`, else `screen`), and label the verdict in the message as
    rule-based so it's clear no LLM judged it.
- `send_telegram()` caption updated to:
  ```
  {company} — {title}
  Verdict: {SCREEN ✅ | REJECT-RISK ⚠️} — {reason}
  Coverage: {score}/1.0
  Hedged (adjacent/familiar): {comma-joined hedged keywords actually added, if any}
  Missing: {comma-joined missing_keywords, up to ~5}
  Apply: {url}
  #J{id}
  ```
  The new "Hedged" line exists so Mayur sees exactly what got a softened
  claim before applying — transparency check, not just a silent resume
  edit.
  (PDF attachment unchanged; `#J{id}` tag unchanged so existing reply
  commands — applied/skip/fix/note — keep working with no changes needed
  in `tgsync.go`.)

### Resume content changes

Generation already selects a JD-matched focus category
(`determine_focus`) and ranks bullets by JD keyword overlap
(`build_experience_bullets`'s `score()` function) — this is already
JD-specific, just not aggressively surfaced or worded. Selection stays
exactly as-is (deterministic, unchanged); what's new is a bounded LLM
rewording pass on top of it:

1. `build_skills_section`: within each category line, list JD-matched
   keywords first, not in the current alphabetical/as-authored order.
   Hedged-tier keywords (bucket 2/3 above) get appended with their hedge
   phrasing, distinct from direct-claim keywords.
2. New `llm_reword_bullet(bullet_text, direct_keywords, hedged_keywords)`:
   called once per selected bullet (the selection from
   `build_experience_bullets` is untouched — this only reworks phrasing
   of bullets already chosen). Inputs are the *pre-computed, approved*
   fair-game keyword lists for that bullet (direct-claim vs
   hedged-claim, per the tiers above) — the LLM never decides which
   keywords are fair game, only how to phrase the ones it's handed.
   System prompt constraints, enforced explicitly in the prompt text:
   - May reword sentence structure to naturally surface the given
     keywords.
   - Must preserve every number/metric from the original bullet
     verbatim (e.g. "1k RPS", "35%").
   - Must not introduce any tool, action, or outcome not in the
     original bullet or the approved keyword lists.
   - Hedged-tier keywords must appear with hedging language; direct-tier
     keywords may appear plainly.
3. **Post-generation safety check** (code, not the LLM's word):
   - Extract all numbers from the original bullet; confirm each appears
     unchanged in the reworded version. Mismatch → reject, fall back to
     the original deterministic bullet.
   - Scan the reworded bullet against the full `KEYWORD_CANDIDATES` set;
     any match not in that bullet's approved keyword list → reject,
     fall back to the original.
   - LLM call failure/timeout → same fallback, no blocking.

This keeps content *selection* (what experience to show, in what order)
fully deterministic and unchanged, while allowing *phrasing* to adapt
per JD — bounded by a mechanical check that can't be talked around by
the LLM, rather than trusting the prompt alone.

### facts.md change

New section, e.g. after "Skills inventory (honesty tiers)":

```
# Adjacent tool exposure (for JD-borderline claims)
> Format: <real production/used skill> -> <adjacent tools fair to claim
> "exposure to"/"familiarity with">. Only tools listed here as a target
> are eligible for a hedged claim when they show up in a JD — anything
> else not in facts.md at all stays out, no matter how JD-relevant.
- Terraform -> Pulumi, Ansible, CloudFormation
- Kubernetes (familiar) -> Helm, ArgoCD
- Kafka (familiar) -> Pulsar
[Mayur fills in the rest — this is a judgment call only he can make]
```

Mirrors `ADJACENCY_MAP` in `tailor_resume.py`, kept in sync manually —
same existing pattern as `TRUTHFUL_SKILLS`/`KEYWORD_CANDIDATES` already
being hand-transcribed from `facts.md` rather than parsed.

## Config

- New env var `OPENCODE_API_KEY`, sourced from `creds.yml`'s
  `opencode-go.api_key` (deployed the same way `JOBWATCH_TG_TOKEN` /
  `JOBWATCH_TG_CHAT` already are — set in the cron wrapper / systemd
  environment, not committed to the repo).
- Base URL: `https://opencode.ai/zen/go/v1` (OpenAI-compatible chat
  completions).
- Model: new `config.yaml` key, e.g. `tailoring.llm_model`, defaulting to
  `deepseek-v4-pro` — swappable without code changes.
- Request timeout on both LLM calls (extraction, judge): short (e.g. 20s)
  so a slow/hung LLM never blocks the whole pipeline — failures fall back
  as described above rather than hanging the one-off `--job-id` run or
  the batch cron.

## Known limitation (flagged, not solved here)

Some ATS pages (Keka and others) render the JD via client-side JS — a
plain HTTP GET can return a near-empty HTML shell with no JD text in it
at all. Neither the free scrape nor the LLM fallback can extract text
that was never present in the fetched HTML. This spec's mitigation is
graceful degradation (title-only + an explicit "JD text unavailable" flag
in the Telegram message) rather than a guaranteed fix — a headless
browser fetch would solve it but is out of scope here given the box is a
t3.micro already tight on resources (see CLAUDE.md's CPU-credit gotcha).
If this turns out to bite on real submissions, worth a follow-up spec.

## Testing

- `go test ./...` for the new `internal/jobsubmit` package and the
  updated `internal/web` handler (dedupe response, 202 vs 200, detached
  process spawn doesn't block the response).
- Manual end-to-end verification against the real URL from this
  conversation (`https://valorem.keka.com/careers/jobdetails/124256`):
  submit via UI, confirm a Telegram message actually arrives with a
  verdict, coverage score, and missing keywords — not just that the code
  compiles. Per existing project convention (see `feedback:
  verify_generated_output` — compile/render or a live API call before
  claiming done), this must be checked against the real OpenCode Go API
  and a real Telegram send, not mocked.
- `gofmt -l .` clean.
