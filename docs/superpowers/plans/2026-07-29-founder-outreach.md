# Founder-Finder + Gmail Draft Outreach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** For a job in the target sector (crypto/web3/defi/fintech/ai) at a company with <=20 employees, automatically find a named founder and create a personalized, resume-attached Gmail draft — never auto-sent — triggerable automatically, via a Telegram reply, or from the dashboard.

**Architecture:** Extends `scripts/tailor_resume.py`'s existing per-job pipeline (Python, zero third-party deps — stdlib `urllib`/`sqlite3`/`email.mime` only, matching this file's existing convention) with a sector tag piggybacked onto the existing LLM verdict call, an Apollo.io lookup for employee count + founder email, one more LLM call for the email draft, and a raw-REST Gmail API call to create the draft. Go/dashboard and Telegram sides get thin trigger wiring that shells out to the same Python entry point, mirroring the existing `tailor-one.sh`/`resume-fix.sh` patterns exactly.

**Tech Stack:** Python 3 (stdlib only), Go (`internal/store`, `internal/web`, `internal/tgsync`), SQLite, bash wrapper scripts, React/TypeScript (frontend).

## Global Constraints

- No new pip/npm dependencies — Apollo and Gmail integrations use `urllib.request` + stdlib `email.mime`, matching `tailor_resume.py`'s existing zero-dependency convention (see spec).
- New jobs only — no backfill against existing DB rows (spec).
- Draft-only, never auto-send (spec).
- `outreach_status='failed'` must be retryable on the next automatic pass; `skipped_*` states are terminal for automatic retry but overridable by a manual trigger with an explicit founder-email override (spec's retry-semantics fix for the `tailored.json` known-gap class of bug).
- Silent skip (log only, no Telegram message) when a job doesn't qualify; Telegram notification only on a successful draft (spec).
- All new Python module-level secrets (`APOLLO_API_KEY`, `GMAIL_CLIENT_ID`, `GMAIL_CLIENT_SECRET`, `GMAIL_REFRESH_TOKEN`) read via `os.environ.get(..., "")`, same as existing `OPENCODE_API_KEY`/`TG_TOKEN` — never hardcoded, never required at import time.
- This worktree (`~/jobwatch/.claude/worktrees/reply-instructions`) has no `.env`/local DB — any step needing real secrets or a real DB run happens in the main repo root (`~/jobwatch`) instead, per `HANDOFF.md`.

---

### Task 1: DB schema — outreach columns on `jobs`

**Files:**
- Modify: `internal/store/store.go:75-121` (`migrate()`), `internal/store/store.go:255-269` (`JobRow`), `internal/store/store.go:318` (`ListJobs` SELECT), `internal/store/store.go:366-371` (`getJobQuerier`)
- Test: `internal/store/store_test.go`

**Interfaces:**
- Produces: `JobRow.OutreachStatus string`, `JobRow.FounderName string`, `JobRow.FounderEmail string`, `JobRow.OutreachDraftedAt string` — consumed by Task 12 (`internal/web/api.go`'s `toAPIJob`).

- [x] **Step 1: Write the failing test**

Add to `internal/store/store_test.go`:

```go
func TestJobRowHasOutreachFieldsDefaultingEmpty(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	tx, err := s.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	id, err := s.InsertJob(ctx, tx, sampleJob(), StatusNew)
	if err != nil {
		t.Fatalf("InsertJob: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	job, err := s.GetJob(ctx, id)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.OutreachStatus != "" || job.FounderName != "" || job.FounderEmail != "" || job.OutreachDraftedAt != "" {
		t.Errorf("outreach fields = %+v, want all empty by default", job)
	}

	rows, err := s.ListJobs(ctx, JobFilter{})
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(rows) != 1 || rows[0].OutreachStatus != "" {
		t.Errorf("ListJobs rows = %+v, want one row with empty OutreachStatus", rows)
	}
}
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions && go test ./internal/store/... -run TestJobRowHasOutreachFieldsDefaultingEmpty -v`
Expected: FAIL — `JobRow` has no field `OutreachStatus` (compile error).

- [x] **Step 3: Add the columns to the schema, migration, struct, and both query sites**

In `internal/store/store.go`, inside the `const schema = \`` block (around line 92, right after `manual_jd_text TEXT NOT NULL DEFAULT ''`), add:

```sql
	outreach_status TEXT NOT NULL DEFAULT '',
	founder_name TEXT NOT NULL DEFAULT '',
	founder_email TEXT NOT NULL DEFAULT '',
	outreach_drafted_at TEXT NOT NULL DEFAULT '',
```

Right after the existing `_, _ = s.db.Exec(\`ALTER TABLE jobs ADD COLUMN manual_jd_text ...\`)` line (~119), add:

```go
	_, _ = s.db.Exec(`ALTER TABLE jobs ADD COLUMN outreach_status TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE jobs ADD COLUMN founder_name TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE jobs ADD COLUMN founder_email TEXT NOT NULL DEFAULT ''`)
	_, _ = s.db.Exec(`ALTER TABLE jobs ADD COLUMN outreach_drafted_at TEXT NOT NULL DEFAULT ''`)
```

Update `JobRow` (around line 255):

```go
type JobRow struct {
	ID                int64
	Provider          string
	CompanySlug       string
	CompanyName       string
	ExternalID        string
	Title             string
	Location          string
	URL               string
	PostedAt          sql.NullString
	FirstSeenAt       string
	Status            string
	Notes             string
	ManualJDText      string
	OutreachStatus    string
	FounderName       string
	FounderEmail      string
	OutreachDraftedAt string
}
```

Update `ListJobs`'s SELECT (line 318) and `Scan` (line 333-334):

```go
	query := `SELECT id, provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, notes, manual_jd_text, outreach_status, founder_name, founder_email, outreach_drafted_at FROM jobs` + where
```

```go
		if err := rows.Scan(&j.ID, &j.Provider, &j.CompanySlug, &j.CompanyName, &j.ExternalID,
			&j.Title, &j.Location, &j.URL, &j.PostedAt, &j.FirstSeenAt, &j.Status, &j.Notes, &j.ManualJDText,
			&j.OutreachStatus, &j.FounderName, &j.FounderEmail, &j.OutreachDraftedAt); err != nil {
```

Update `getJobQuerier` (line 366-371):

```go
func getJobQuerier(ctx context.Context, q querier, id int64) (JobRow, error) {
	var j JobRow
	err := q.QueryRowContext(ctx,
		`SELECT id, provider, company_slug, company_name, external_id, title, location, url, posted_at, first_seen_at, status, notes, manual_jd_text, outreach_status, founder_name, founder_email, outreach_drafted_at FROM jobs WHERE id = ?`,
		id,
	).Scan(&j.ID, &j.Provider, &j.CompanySlug, &j.CompanyName, &j.ExternalID,
		&j.Title, &j.Location, &j.URL, &j.PostedAt, &j.FirstSeenAt, &j.Status, &j.Notes, &j.ManualJDText,
		&j.OutreachStatus, &j.FounderName, &j.FounderEmail, &j.OutreachDraftedAt)
	return j, err
}
```

- [x] **Step 4: Run test to verify it passes**

Run: `go test ./internal/store/... -v`
Expected: PASS, all existing store tests still pass (additive columns, `SELECT` lists explicit so nothing else breaks).

- [x] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add internal/store/store.go internal/store/store_test.go
git commit -m "Add outreach_status/founder_name/founder_email columns to jobs table"
```

---

### Task 2: Sector classification piggybacked on `llm_judge`

**Files:**
- Modify: `scripts/tailor_resume.py:245-283` (`llm_judge`), `scripts/tailor_resume.py:1536-1547` (`tailored[jid]` entry in `process_job`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `llm_judge(...)` return dict gains `"sector"` key: one of `"crypto"|"web3"|"defi"|"fintech"|"ai"|None`. Consumed by Task 9 (`run_outreach_step`'s caller in `process_job`).

- [x] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_llm_judge_extracts_sector_field_when_present(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit.", "sector": "fintech"}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] == "fintech"


def test_llm_judge_sector_none_when_absent_from_response(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit."}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] is None


def test_llm_judge_sector_none_for_rule_based_fallback(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda *a, **k: None)
    result = tr.llm_judge("JD text about Go and PostgreSQL", "resume text", "Backend Engineer", "Acme")
    assert result["source"] == "rule_based"
    assert result["sector"] is None


def test_llm_judge_rejects_invalid_sector_value(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.DEFAULT_OPENCODE_MODEL, timeout=20:
            '{"verdict": "screen", "missing_keywords": [], "reason": "Good fit.", "sector": "gaming"}',
    )
    result = tr.llm_judge("JD text", "resume text", "Backend Engineer", "Acme")
    assert result["sector"] is None
```

- [x] **Step 2: Run test to verify it fails**

Run: `cd /home/dev-mayur/jobwatch && python3 -m pytest scripts/test_tailor_resume.py -k sector -v`
Expected: FAIL — `KeyError: 'sector'`.

- [x] **Step 3: Implement**

In `scripts/tailor_resume.py`, update the module-level constant block (near line 58, after `RULE_BASED_REJECT_THRESHOLD`):

```python
VALID_SECTORS = {"crypto", "web3", "defi", "fintech", "ai"}
```

Replace `llm_judge`'s system prompt and parsing (lines 245-283):

```python
def llm_judge(jd_text, resume_text, title, company):
    """Judge how a busy recruiter (5-10 seconds per resume, 100+ resumes
    to screen) would react to this resume against this JD: screen it
    forward, or reject-risk. Also classifies the company's sector
    (crypto/web3/defi/fintech/ai, or null) as a free extra field on the
    same call -- used by the founder-outreach pipeline to decide whether
    to attempt an outreach draft, at no extra LLM cost. Always returns a
    usable result -- falls back to a rule-based verdict (score_coverage
    threshold) on any LLM failure, timeout, or malformed response, tagged
    via "source" so the Telegram message can show which one produced it.
    Rule-based fallback never classifies sector (no signal for it).
    """
    raw = call_opencode(
        system_prompt=(
            "You are a hiring manager screening resumes for a "
            f"{title} role at {company}. You see 100+ resumes and spend "
            "5-10 seconds on each. Given the job description and a "
            "candidate's resume text, decide: would you screen this "
            "resume forward for a closer look, or is it reject-risk? "
            "Also classify the company's sector based on the job "
            "description and company name: one of crypto, web3, defi, "
            "fintech, ai, or null if none of those clearly apply. "
            "Respond with ONLY valid JSON, no markdown fences, no "
            "commentary, in this exact shape: "
            '{"verdict": "screen"|"reject_risk", '
            '"missing_keywords": ["keyword1", "keyword2"], '
            '"reason": "one sentence explaining the verdict", '
            '"sector": "crypto"|"web3"|"defi"|"fintech"|"ai"|null}'
        ),
        user_content=f"JOB DESCRIPTION:\n{jd_text}\n\nRESUME:\n{resume_text}",
    )

    if raw:
        try:
            parsed = json.loads(raw.strip().strip("`").removeprefix("json").strip())
            if parsed.get("verdict") in ("screen", "reject_risk") and isinstance(parsed.get("missing_keywords"), list):
                sector = parsed.get("sector")
                if sector not in VALID_SECTORS:
                    sector = None
                return {
                    "verdict": parsed["verdict"],
                    "missing_keywords": parsed["missing_keywords"],
                    "reason": str(parsed.get("reason", "")),
                    "source": "llm",
                    "sector": sector,
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
        "sector": None,
    }
```

In `process_job` (line ~1536), add `"sector": judgment["sector"],` to the `tailored[jid] = {...}` dict literal, alongside the existing `"verdict": judgment["verdict"], ...` line.

- [x] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS — new sector tests pass, all pre-existing `llm_judge` tests still pass (they don't assert on `"sector"`, and `.get("sector")` defaults handle its absence).

- [x] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add sector classification to llm_judge's existing verdict call"
```

---

### Task 3: Extract `fetch_jd_text_for_job` helper

**Files:**
- Modify: `scripts/tailor_resume.py:1345-1389` (`process_job`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `fetch_jd_text_for_job(job) -> (jd_text: str, jd_unavailable: bool)`. Consumed by `process_job` (refactored to use it) and by Task 9's `--outreach` CLI path (which needs JD text for an already-tailored job without re-running the whole tailoring flow).

- [x] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_fetch_jd_text_for_job_uses_manual_jd_text_when_present():
    job = {
        "id": 1, "company_name": "Acme", "title": "Backend Engineer",
        "url": "https://example.com/job/1", "manual_jd_text": "We need Go and PostgreSQL experience.",
    }
    jd_text, jd_unavailable = tr.fetch_jd_text_for_job(job)
    assert jd_text == "We need Go and PostgreSQL experience."
    assert jd_unavailable is False


def test_fetch_jd_text_for_job_falls_back_to_title_only(monkeypatch):
    monkeypatch.setattr(tr, "fetch_jd_generic", lambda url: None)
    job = {"id": 2, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/job/2"}
    jd_text, jd_unavailable = tr.fetch_jd_text_for_job(job)
    assert jd_text == "Backend Engineer"
    assert jd_unavailable is True
```

- [x] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k fetch_jd_text_for_job -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'fetch_jd_text_for_job'`.

- [x] **Step 3: Extract the helper**

In `scripts/tailor_resume.py`, add this new function right before `process_job` (line ~1345):

```python
def fetch_jd_text_for_job(job):
    """Resolve JD text for a job: Greenhouse API first (free, no LLM),
    then manually-supplied JD text, then the generic HTML-scrape+LLM
    fallback, then title-only as a last resort. Returns (jd_text,
    jd_unavailable) -- jd_unavailable is True only for the title-only
    last resort, so callers can flag verdicts/drafts as unreliable.
    Shared by process_job (fresh tailoring) and the --outreach CLI path
    (which needs JD text for a job already tailored earlier).
    """
    company = job.get("company_name") or "Unknown"
    title = job.get("title") or "Unknown"
    url = job.get("url") or ""

    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = None
    if gh_id and gh_slug:
        jd_data = fetch_greenhouse_job(gh_slug, gh_id)

    manual_jd_text_used = False
    if not jd_data:
        manual_jd_text = (job.get("manual_jd_text") or "").strip()
        if manual_jd_text:
            jd_data = {
                "title": title, "company_name": company, "content_text": manual_jd_text,
                "content_html": "", "location": "", "absolute_url": url,
            }
            manual_jd_text_used = True

    jd_unavailable = False
    if not jd_data:
        jd_data = fetch_jd_generic(url)
    if not jd_data or (not manual_jd_text_used and len(jd_data.get("content_text", "")) < MIN_USABLE_JD_CHARS):
        jd_unavailable = True
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }

    return jd_data.get("content_text", ""), jd_unavailable
```

Replace `process_job`'s inline JD-fetch block (lines ~1362-1389, from `gh_slug = company_slug(company, url)` through `jd_text = jd_data.get("content_text", "")`) with:

```python
    jd_text, jd_unavailable = fetch_jd_text_for_job(job)
```

- [x] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS — new tests pass, and every existing `process_job`-dependent test (non-eng skip, experience-cap skip, JD-unavailable flagging) still passes unchanged, since the extracted function is byte-for-byte the same logic.

- [x] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Extract fetch_jd_text_for_job from process_job for reuse by outreach CLI path"
```

---

### Task 4: Apollo.io lookup — employee count + founder email

**Files:**
- Modify: `scripts/tailor_resume.py` (new constants near line 51, new functions after `call_opencode`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `apollo_lookup(company_name: str) -> dict | None`, shape `{"employee_count": int|None, "founder_name": str, "founder_email": str}`. Consumed by Task 9's `run_outreach_step`.
- Consumes: `call_opencode`'s existing `urlopen`/`Request` import pattern (no new imports needed — already imported at top of file).

**Verification note (do this before trusting the parsing in production):** this task's endpoint paths/field names (`/organizations/search`, `/mixed_people/search`, `/people/match`, `estimated_num_employees`, etc.) are written against Apollo's documented v1 API but have **not** been verified with a live call — same rigor gap the project's own convention (see `feedback: verify_generated_output` and this file's own `HANDOFF.md` habit of curling real endpoints before trusting them) requires closing before this ships. Step 6 below is a mandatory live check with a real `APOLLO_API_KEY` and a real small company name — adjust field names if the real response differs.

- [x] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_apollo_lookup_returns_none_without_company_name():
    assert tr.apollo_lookup("") is None


def test_apollo_lookup_returns_none_when_no_organization_match(monkeypatch):
    monkeypatch.setattr(tr, "_apollo_post", lambda path, payload, timeout=15: {"organizations": []})
    assert tr.apollo_lookup("Acme") is None


def test_apollo_lookup_returns_empty_founder_fields_when_no_people_found(monkeypatch):
    responses = iter([
        {"organizations": [{"id": "org1", "estimated_num_employees": 5}]},
        {"people": []},
    ])
    monkeypatch.setattr(tr, "_apollo_post", lambda path, payload, timeout=15: next(responses))
    result = tr.apollo_lookup("Acme")
    assert result == {"employee_count": 5, "founder_name": "", "founder_email": ""}


def test_apollo_lookup_returns_employee_count_and_founder_email(monkeypatch):
    responses = iter([
        {"organizations": [{"id": "org1", "estimated_num_employees": 12}]},
        {"people": [{"id": "p1", "name": "Jane Founder"}]},
        {"person": {"email": "jane@acme.xyz"}},
    ])
    monkeypatch.setattr(tr, "_apollo_post", lambda path, payload, timeout=15: next(responses))
    result = tr.apollo_lookup("Acme")
    assert result == {"employee_count": 12, "founder_name": "Jane Founder", "founder_email": "jane@acme.xyz"}


def test_apollo_lookup_treats_unlocked_placeholder_email_as_no_email(monkeypatch):
    responses = iter([
        {"organizations": [{"id": "org1", "estimated_num_employees": 8}]},
        {"people": [{"id": "p1", "name": "Jane Founder"}]},
        {"person": {"email": "email_not_unlocked@domain.com"}},
    ])
    monkeypatch.setattr(tr, "_apollo_post", lambda path, payload, timeout=15: next(responses))
    result = tr.apollo_lookup("Acme")
    assert result["founder_email"] == ""
```

- [x] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k apollo -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'apollo_lookup'`.

- [x] **Step 3: Implement**

Add near the top of `scripts/tailor_resume.py`, after the existing `OPENCODE_API_KEY`/`OPENCODE_BASE_URL` block (~line 58):

```python
# Apollo.io: used only for the founder-outreach feature's employee-count
# and named-founder-email lookup -- company NAME search only (no domain
# resolution attempted; Greenhouse/Ashby postings live on the ATS's own
# domain, not the company's, so a reliable domain isn't always derivable).
APOLLO_API_KEY = os.environ.get("APOLLO_API_KEY", "")
APOLLO_BASE_URL = "https://api.apollo.io/v1"
FOUNDER_TITLES = ["founder", "co-founder", "cofounder", "chief executive officer", "ceo"]
MAX_OUTREACH_EMPLOYEES = 20
```

Add these functions after `call_opencode` (after line 238, before `RULE_BASED_REJECT_THRESHOLD`):

```python
def _apollo_post(path, payload, timeout=15):
    """POST to one Apollo.io v1 endpoint. Returns the parsed JSON body, or
    None on any failure (missing key, timeout, non-200, malformed JSON) --
    callers always treat None as a miss, never crash.
    """
    if not APOLLO_API_KEY:
        return None
    body = json.dumps(payload).encode("utf-8")
    try:
        req = Request(
            f"{APOLLO_BASE_URL}{path}",
            data=body,
            method="POST",
            headers={
                "x-api-key": APOLLO_API_KEY,
                "Content-Type": "application/json",
                "Accept": "application/json",
                "User-Agent": "jobwatch-outreach/1.0",
            },
        )
        with urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except Exception as e:
        log(f"WARN: Apollo API call to {path} failed: {e}")
        return None


def apollo_lookup(company_name):
    """Look up a company's employee count and a named founder's email via
    Apollo.io: organization search by name -> people search within that
    org filtered to founder/CEO titles -> a match/reveal call for the
    top person's actual email (Apollo gates real emails behind this
    separate reveal step; search alone often returns a locked
    placeholder). Returns None on no company match; returns a dict with
    empty founder_name/founder_email (not None) when the company matches
    but no qualifying person is found, since employee_count is still
    useful to the caller in that case.
    """
    if not company_name:
        return None

    org_resp = _apollo_post("/organizations/search", {"q_organization_name": company_name, "page": 1, "per_page": 1})
    if not org_resp:
        return None
    orgs = org_resp.get("organizations") or []
    if not orgs:
        return None
    org = orgs[0]
    org_id = org.get("id")
    employee_count = org.get("estimated_num_employees")
    if not org_id:
        return None

    people_resp = _apollo_post("/mixed_people/search", {
        "organization_ids": [org_id], "person_titles": FOUNDER_TITLES, "page": 1, "per_page": 3,
    })
    people = (people_resp or {}).get("people") or []
    if not people:
        return {"employee_count": employee_count, "founder_name": "", "founder_email": ""}

    person = people[0]
    founder_name = person.get("name", "")
    person_id = person.get("id")

    founder_email = ""
    if person_id:
        match_resp = _apollo_post("/people/match", {"id": person_id, "reveal_personal_emails": True})
        matched = (match_resp or {}).get("person") or {}
        email = matched.get("email", "")
        if email and "not_unlocked" not in email:
            founder_email = email

    return {"employee_count": employee_count, "founder_name": founder_name, "founder_email": founder_email}
```

- [x] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add Apollo.io lookup for employee count and founder email"
```

- [x] **Step 6: Live verification (do this in the main repo root, once `APOLLO_API_KEY` exists in `.env`)**

```bash
cd /home/dev-mayur/jobwatch
source .env
python3 -c "
import sys
sys.path.insert(0, 'scripts')
import tailor_resume as tr
print(tr.apollo_lookup('<a real small startup name you know>'))
"
```

Confirm the printed dict has a plausible `employee_count` and, ideally, a real-looking `founder_email` (not empty, not `email_not_unlocked@...`). If the shape differs from what Step 3 assumed (e.g. a different key name, or `/people/match` needing `first_name`/`last_name`/`organization_name` instead of `id`), adjust `apollo_lookup`/`_apollo_post` accordingly and re-run this check before moving on — this is the same "verify against the real API before trusting it" step this project already applies to every new provider integration (see `HANDOFF.md`'s web3career example).

**Actual result (2026-07-29, live key):** request shapes were correct against real Apollo responses (`/organizations/search` returned `employee_count: 37` for "Portcast", matching real data), but every person-data endpoint — `/mixed_people/search`, `/people/search`, `/people/match`, `/mixed_companies/search` — returned `403 API_INACCESSIBLE`: *"not included in your Free plan and is not accessible, even with a master key."* Confirmed by probing all four directly, not just the one this task calls. **Apollo's free tier cannot return founder_name/founder_email at all**, only company data. `apollo_lookup` still degrades correctly (`employee_count` populated, founder fields empty — exactly the already-tested "no people found" case), so nothing is broken; automatic founder lookup is just inert until the Apollo plan is upgraded (Mayur's call, not pursuing right now). The plan's own `founder_email_override` mechanism (Global Constraints, Task 9, Task 12) is the intended workaround in the meantime — manual correction, not automatic discovery.

---

### Task 5: Outreach status DB read/write helpers

**Files:**
- Modify: `scripts/tailor_resume.py` (new functions near `update_job_status`, line ~1163)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `get_outreach_status(job_id) -> str`, `update_outreach_fields(job_id, status, founder_name="", founder_email="")`. Consumed by Task 9's `run_outreach_step`.

- [x] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_get_outreach_status_returns_empty_string_by_default(monkeypatch, tmp_path):
    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY, outreach_status TEXT NOT NULL DEFAULT '')")
    conn.execute("INSERT INTO jobs (id, outreach_status) VALUES (1, '')")
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)

    assert tr.get_outreach_status(1) == ""


def test_update_outreach_fields_sets_status_and_founder_info(monkeypatch, tmp_path):
    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute(
        "CREATE TABLE jobs (id INTEGER PRIMARY KEY, outreach_status TEXT NOT NULL DEFAULT '', "
        "founder_name TEXT NOT NULL DEFAULT '', founder_email TEXT NOT NULL DEFAULT '', "
        "outreach_drafted_at TEXT NOT NULL DEFAULT '')"
    )
    conn.execute("INSERT INTO jobs (id) VALUES (1)")
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)

    tr.update_outreach_fields(1, "drafted", "Jane Founder", "jane@acme.xyz")

    assert tr.get_outreach_status(1) == "drafted"
    conn = sqlite3.connect(db_path)
    row = conn.execute("SELECT founder_name, founder_email, outreach_drafted_at FROM jobs WHERE id = 1").fetchone()
    conn.close()
    assert row[0] == "Jane Founder"
    assert row[1] == "jane@acme.xyz"
    assert row[2] != ""


def test_update_outreach_fields_leaves_drafted_at_empty_for_non_drafted_status(monkeypatch, tmp_path):
    db_path = tmp_path / "test.db"
    conn = sqlite3.connect(db_path)
    conn.execute(
        "CREATE TABLE jobs (id INTEGER PRIMARY KEY, outreach_status TEXT NOT NULL DEFAULT '', "
        "founder_name TEXT NOT NULL DEFAULT '', founder_email TEXT NOT NULL DEFAULT '', "
        "outreach_drafted_at TEXT NOT NULL DEFAULT '')"
    )
    conn.execute("INSERT INTO jobs (id) VALUES (1)")
    conn.commit()
    conn.close()
    monkeypatch.setattr(tr, "DB_PATH", db_path)

    tr.update_outreach_fields(1, "skipped_size")

    conn = sqlite3.connect(db_path)
    row = conn.execute("SELECT outreach_drafted_at FROM jobs WHERE id = 1").fetchone()
    conn.close()
    assert row[0] == ""
```

- [x] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k outreach_fields -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'get_outreach_status'`.

- [x] **Step 3: Implement**

Add right after `update_job_status` in `scripts/tailor_resume.py` (line ~1176):

```python
def get_outreach_status(job_id):
    """Read a job's current outreach_status. Empty string if the job has
    never been through the outreach step (or has no such column yet on a
    very old DB -- shouldn't happen post-migration, but this never
    crashes on a missing row).
    """
    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    try:
        row = conn.execute("SELECT outreach_status FROM jobs WHERE id = ?", (int(job_id),)).fetchone()
        return row[0] if row else ""
    finally:
        conn.close()


def update_outreach_fields(job_id, status, founder_name="", founder_email=""):
    """Persist the outcome of one outreach attempt. outreach_drafted_at is
    only set for status="drafted" -- every other status (skipped_*,
    failed) leaves it empty, so the dashboard/Telegram can distinguish
    "never drafted" from "drafted at this timestamp" unambiguously.
    """
    drafted_at = datetime.now().isoformat() if status == "drafted" else ""
    conn = sqlite3.connect(DB_PATH)
    try:
        conn.execute(
            "UPDATE jobs SET outreach_status = ?, founder_name = ?, founder_email = ?, outreach_drafted_at = ? WHERE id = ?",
            (status, founder_name, founder_email, drafted_at, int(job_id)),
        )
        conn.commit()
    finally:
        conn.close()
```

- [x] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add outreach_status DB read/write helpers"
```

---

### Task 6: Draft outreach email via LLM

**Status (2026-07-29): deferred, not skipped permanently.** Tasks 1-5 merged to `master` as their own checkpoint; picking this up next in the "Gmail draft creation" continuation (tasks 6-8 together), using `founder_email_override` for the founder-email field per the Task 4 note above rather than blocking on an Apollo upgrade.

**Files:**
- Modify: `scripts/tailor_resume.py` (new constant + function after `apollo_lookup`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `draft_outreach_email(jd_text, resume_text, founder_name, title, company) -> {"subject": str, "body": str} | None`. Consumed by Task 9's `run_outreach_step`.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_draft_outreach_email_returns_subject_and_body(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.OUTREACH_EMAIL_MODEL, timeout=20:
            '{"subject": "Backend Engineer role", "body": "Hi Jane, ..."}',
    )
    result = tr.draft_outreach_email("JD text", "resume text", "Jane", "Backend Engineer", "Acme")
    assert result == {"subject": "Backend Engineer role", "body": "Hi Jane, ..."}


def test_draft_outreach_email_returns_none_on_llm_failure(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda *a, **k: None)
    assert tr.draft_outreach_email("JD text", "resume text", "Jane", "Backend Engineer", "Acme") is None


def test_draft_outreach_email_returns_none_on_malformed_json(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda *a, **k: "not json")
    assert tr.draft_outreach_email("JD text", "resume text", "Jane", "Backend Engineer", "Acme") is None


def test_draft_outreach_email_returns_none_on_empty_subject_or_body(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, model=tr.OUTREACH_EMAIL_MODEL, timeout=20:
            '{"subject": "", "body": "Hi Jane, ..."}',
    )
    assert tr.draft_outreach_email("JD text", "resume text", "Jane", "Backend Engineer", "Acme") is None
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k draft_outreach_email -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'draft_outreach_email'`.

- [ ] **Step 3: Implement**

Add near `DEFAULT_OPENCODE_MODEL` (line ~59):

```python
# Separate, independently-swappable model for outreach-email drafting --
# same OpenCode Go gateway/subscription as tailoring's LLM calls, but its
# own named constant so it can be pointed at a cheaper model later
# without touching the (much higher-volume) tailoring/verdict call.
OUTREACH_EMAIL_MODEL = DEFAULT_OPENCODE_MODEL
```

Add after `apollo_lookup`:

```python
def draft_outreach_email(jd_text, resume_text, founder_name, title, company):
    """Generate a short, personalized cold-outreach email from the
    candidate to a startup founder, referencing concrete JD/resume
    overlap. Returns {"subject": str, "body": str} on success, None on
    any LLM failure or malformed/empty response -- callers must skip
    (never fall back to a generic template; a non-personalized "draft"
    isn't worth creating, see spec's Error handling section).
    """
    raw = call_opencode(
        system_prompt=(
            "You write short, genuine-sounding cold outreach emails from "
            "a software engineer job candidate directly to a startup "
            f"founder. The candidate is applying for a {title} role at "
            f"{company}. Reference one or two concrete points from the "
            "job description and the candidate's resume that make them a "
            "good fit -- do not invent any experience not present in the "
            "resume text. Keep it under 150 words, no generic flattery, "
            "no 'I hope this email finds you well'. Respond with ONLY "
            "valid JSON, no markdown fences, no commentary, in this "
            'exact shape: {"subject": "...", "body": "..."}'
        ),
        user_content=(
            f"FOUNDER NAME: {founder_name or 'there'}\n\n"
            f"JOB DESCRIPTION:\n{jd_text}\n\nRESUME:\n{resume_text}"
        ),
        model=OUTREACH_EMAIL_MODEL,
        timeout=20,
    )
    if not raw:
        return None
    try:
        parsed = json.loads(raw.strip().strip("`").removeprefix("json").strip())
    except (json.JSONDecodeError, AttributeError):
        log("WARN: draft_outreach_email got malformed JSON from OpenCode")
        return None

    subject = str(parsed.get("subject", "")).strip()
    body = str(parsed.get("body", "")).strip()
    if not subject or not body:
        return None
    return {"subject": subject, "body": body}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add LLM-generated outreach email drafting"
```

---

### Task 7: Gmail API draft creation (raw REST, stdlib only)

**Files:**
- Modify: `scripts/tailor_resume.py` (new top-level imports, new constants, new functions)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `create_gmail_draft(to_email, subject, body_text, attachment_path=None) -> (ok: bool, error: str|None)`. Consumed by Task 9's `run_outreach_step`.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_gmail_access_token_returns_none_without_config(monkeypatch):
    monkeypatch.setattr(tr, "GMAIL_CLIENT_ID", "")
    monkeypatch.setattr(tr, "GMAIL_CLIENT_SECRET", "secret")
    monkeypatch.setattr(tr, "GMAIL_REFRESH_TOKEN", "refresh")
    assert tr._gmail_access_token() is None


def test_gmail_access_token_returns_token_on_success(monkeypatch):
    monkeypatch.setattr(tr, "GMAIL_CLIENT_ID", "id")
    monkeypatch.setattr(tr, "GMAIL_CLIENT_SECRET", "secret")
    monkeypatch.setattr(tr, "GMAIL_REFRESH_TOKEN", "refresh")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return json.dumps({"access_token": "tok123"}).encode("utf-8")

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=15: FakeResponse())
    assert tr._gmail_access_token() == "tok123"


def test_create_gmail_draft_returns_false_without_token(monkeypatch):
    monkeypatch.setattr(tr, "_gmail_access_token", lambda: None)
    ok, err = tr.create_gmail_draft("founder@acme.xyz", "Subject", "Body")
    assert ok is False
    assert err is not None


def test_create_gmail_draft_returns_true_on_success_with_attachment(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "_gmail_access_token", lambda: "tok123")

    class FakeResponse:
        def __enter__(self):
            return self
        def __exit__(self, *a):
            return False
        def read(self):
            return b'{"id": "draft1"}'

    monkeypatch.setattr(tr, "urlopen", lambda req, timeout=30: FakeResponse())
    pdf_path = tmp_path / "resume.pdf"
    pdf_path.write_bytes(b"%PDF-1.4 fake")

    ok, err = tr.create_gmail_draft("founder@acme.xyz", "Subject", "Body text", str(pdf_path))
    assert ok is True
    assert err is None


def test_create_gmail_draft_returns_false_on_api_error(monkeypatch):
    monkeypatch.setattr(tr, "_gmail_access_token", lambda: "tok123")

    def raise_error(*args, **kwargs):
        raise OSError("500 server error")

    monkeypatch.setattr(tr, "urlopen", raise_error)
    ok, err = tr.create_gmail_draft("founder@acme.xyz", "Subject", "Body text")
    assert ok is False
    assert "500" in err
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k gmail -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute '_gmail_access_token'`.

- [ ] **Step 3: Implement**

Add to the top-level imports in `scripts/tailor_resume.py` (near line 18, alongside the existing `from urllib.request import urlopen, Request`):

```python
import base64
from email.mime.multipart import MIMEMultipart
from email.mime.text import MIMEText
from email.mime.application import MIMEApplication
from urllib.parse import urlencode
```

Add near `APOLLO_API_KEY` (from Task 4):

```python
# Gmail API (gmail.compose scope only -- can create/edit drafts, cannot
# read or send mail). OAuth2 refresh token, minted once via the local
# scripts/gmail_auth_setup.py flow (see docs/superpowers/specs/
# 2026-07-27-founder-outreach-design.md's "Gmail OAuth setup" section).
GMAIL_CLIENT_ID = os.environ.get("GMAIL_CLIENT_ID", "")
GMAIL_CLIENT_SECRET = os.environ.get("GMAIL_CLIENT_SECRET", "")
GMAIL_REFRESH_TOKEN = os.environ.get("GMAIL_REFRESH_TOKEN", "")
GMAIL_TOKEN_URL = "https://oauth2.googleapis.com/token"
GMAIL_DRAFTS_URL = "https://gmail.googleapis.com/gmail/v1/users/me/drafts"
```

Add these functions after `create_gmail_draft`'s sibling helpers (right after the Apollo block from Task 4, or anywhere before `process_job`):

```python
def _gmail_access_token():
    """Exchange the long-lived refresh token for a short-lived access
    token. Returns the token string, or None on any failure (missing
    config, revoked token, network error) -- callers must treat this as
    a miss, never crash.
    """
    if not (GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET and GMAIL_REFRESH_TOKEN):
        return None
    body = urlencode({
        "client_id": GMAIL_CLIENT_ID,
        "client_secret": GMAIL_CLIENT_SECRET,
        "refresh_token": GMAIL_REFRESH_TOKEN,
        "grant_type": "refresh_token",
    }).encode("utf-8")
    try:
        req = Request(
            GMAIL_TOKEN_URL, data=body, method="POST",
            headers={"Content-Type": "application/x-www-form-urlencoded"},
        )
        with urlopen(req, timeout=15) as resp:
            data = json.loads(resp.read().decode("utf-8"))
        return data.get("access_token")
    except Exception as e:
        log(f"WARN: Gmail token refresh failed: {e}")
        return None


def create_gmail_draft(to_email, subject, body_text, attachment_path=None):
    """Create a Gmail draft (never sends) via the Gmail API. Returns
    (True, None) on success, (False, error_message) on any failure --
    callers must treat failure as retryable (outreach_status='failed'),
    never crash the caller's own flow.
    """
    access_token = _gmail_access_token()
    if not access_token:
        return False, "Gmail not configured or token refresh failed"

    msg = MIMEMultipart()
    msg["to"] = to_email
    msg["subject"] = subject
    msg.attach(MIMEText(body_text, "plain"))

    if attachment_path and Path(attachment_path).exists():
        with open(attachment_path, "rb") as f:
            part = MIMEApplication(f.read(), _subtype="pdf")
        part.add_header("Content-Disposition", "attachment", filename=Path(attachment_path).name)
        msg.attach(part)

    raw = base64.urlsafe_b64encode(msg.as_bytes()).decode("utf-8")

    try:
        req = Request(
            GMAIL_DRAFTS_URL,
            data=json.dumps({"message": {"raw": raw}}).encode("utf-8"),
            method="POST",
            headers={
                "Authorization": f"Bearer {access_token}",
                "Content-Type": "application/json",
            },
        )
        with urlopen(req, timeout=30) as resp:
            resp.read()
        return True, None
    except Exception as e:
        return False, str(e)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add Gmail API draft creation via raw REST + stdlib email.mime"
```

---

### Task 8: One-time Gmail OAuth setup script + docs

**Files:**
- Create: `scripts/gmail_auth_setup.py`
- Modify: `CLAUDE.md` (new env vars + setup steps)

**Interfaces:**
- Produces: a `refresh_token` string printed to stdout for manual pasting into `.env` as `GMAIL_REFRESH_TOKEN`. Not consumed programmatically by any other task — this is a standalone local tool Mayur runs once.

This task has no automated test (it's an interactive local script requiring a real browser + real Google Cloud OAuth credentials that don't exist yet per the spec's outstanding setup steps) — its own step 2 below is the verification, to be run once Mayur has done the Google Cloud Console steps.

- [ ] **Step 1: Write the script**

Create `scripts/gmail_auth_setup.py`:

```python
#!/usr/bin/env python3
"""
One-time local OAuth2 flow for jobwatch's Gmail draft-outreach feature.

Usage: python3 scripts/gmail_auth_setup.py

Prerequisites (see docs/superpowers/specs/2026-07-27-founder-outreach-design.md):
  1. A Google Cloud Console project with the Gmail API enabled.
  2. An OAuth 2.0 Client ID, type "Desktop app" -- gives client_id + client_secret.
  3. OAuth consent screen in Testing mode, your email added as a test user,
     scope gmail.compose only.

Set GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET as environment variables (or
edit them in below) before running. This script opens your browser once,
you approve access, and it prints a refresh_token to paste into .env as
GMAIL_REFRESH_TOKEN -- the unattended cron pipeline uses that token
forever after (until revoked), no repeated browser flow needed.
"""
import http.server
import json
import os
import sys
import threading
import urllib.parse
import webbrowser
from urllib.request import urlopen, Request

CLIENT_ID = os.environ.get("GMAIL_CLIENT_ID", "")
CLIENT_SECRET = os.environ.get("GMAIL_CLIENT_SECRET", "")
REDIRECT_PORT = 8765
REDIRECT_URI = f"http://localhost:{REDIRECT_PORT}"
SCOPE = "https://www.googleapis.com/auth/gmail.compose"

_auth_code = {"value": None}


class _CallbackHandler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        params = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        _auth_code["value"] = params.get("code", [None])[0]
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"Auth complete -- you can close this tab and return to the terminal.")

    def log_message(self, format, *args):
        pass  # silence the default request logging


def main():
    if not CLIENT_ID or not CLIENT_SECRET:
        print("ERROR: set GMAIL_CLIENT_ID and GMAIL_CLIENT_SECRET env vars first.", file=sys.stderr)
        sys.exit(1)

    auth_url = "https://accounts.google.com/o/oauth2/v2/auth?" + urllib.parse.urlencode({
        "client_id": CLIENT_ID,
        "redirect_uri": REDIRECT_URI,
        "response_type": "code",
        "scope": SCOPE,
        "access_type": "offline",
        "prompt": "consent",
    })

    server = http.server.HTTPServer(("localhost", REDIRECT_PORT), _CallbackHandler)
    thread = threading.Thread(target=server.handle_request)
    thread.start()

    print(f"Opening browser for consent: {auth_url}")
    webbrowser.open(auth_url)
    thread.join(timeout=120)

    code = _auth_code["value"]
    if not code:
        print("ERROR: did not receive an auth code within 120s.", file=sys.stderr)
        sys.exit(1)

    body = urllib.parse.urlencode({
        "client_id": CLIENT_ID,
        "client_secret": CLIENT_SECRET,
        "code": code,
        "redirect_uri": REDIRECT_URI,
        "grant_type": "authorization_code",
    }).encode("utf-8")
    req = Request(
        "https://oauth2.googleapis.com/token", data=body, method="POST",
        headers={"Content-Type": "application/x-www-form-urlencoded"},
    )
    with urlopen(req, timeout=15) as resp:
        data = json.loads(resp.read().decode("utf-8"))

    refresh_token = data.get("refresh_token")
    if not refresh_token:
        print(f"ERROR: no refresh_token in response: {data}", file=sys.stderr)
        sys.exit(1)

    print("\nSuccess. Add this to .env (both laptop and EC2):\n")
    print(f"export GMAIL_REFRESH_TOKEN={refresh_token}")


if __name__ == "__main__":
    main()
```

- [ ] **Step 2: Manual verification (run once you've done the Google Cloud Console steps 1-3 above)**

```bash
cd /home/dev-mayur/jobwatch
export GMAIL_CLIENT_ID=<from Cloud Console>
export GMAIL_CLIENT_SECRET=<from Cloud Console>
python3 scripts/gmail_auth_setup.py
```

Confirm a browser tab opens, you approve access, and a `GMAIL_REFRESH_TOKEN` line prints. Add it plus `GMAIL_CLIENT_ID`/`GMAIL_CLIENT_SECRET`/`APOLLO_API_KEY` to both the laptop's `.env` and the EC2 box's `.env` (same pattern as `JOBWATCH_TG_TOKEN`).

- [ ] **Step 3: Update `CLAUDE.md`**

Add to the "Recurring gotchas" section of `CLAUDE.md` (after the `OPENCODE_API_KEY` bullet):

```markdown
- **Founder-outreach feature needs `APOLLO_API_KEY`/`GMAIL_CLIENT_ID`/`GMAIL_CLIENT_SECRET`/`GMAIL_REFRESH_TOKEN` in the server's `/opt/jobwatch/.env`**, not just the laptop's — same class of gotcha as `OPENCODE_API_KEY` above. Without Apollo configured, outreach silently skips every job as `skipped_sector`/`skipped_size` misses (no crash); without Gmail configured, a qualifying job's outreach step fails silently into `outreach_status='failed'` (retried next cycle, never surfaced unless you check `jobs.outreach_status` or `logs/cron.log` directly). Run `scripts/gmail_auth_setup.py` once locally to mint `GMAIL_REFRESH_TOKEN` (see `docs/superpowers/specs/2026-07-27-founder-outreach-design.md`).
```

- [ ] **Step 4: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
chmod +x scripts/gmail_auth_setup.py
git add scripts/gmail_auth_setup.py CLAUDE.md
git commit -m "Add one-time Gmail OAuth setup script and env-var docs"
```

---

### Task 9: Wire outreach into `process_job` + `--outreach` CLI entry point

**Files:**
- Modify: `scripts/tailor_resume.py` (new `send_telegram_message`, new `run_outreach_step`, `process_job` wiring, CLI dispatch at bottom of file)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `get_outreach_status`/`update_outreach_fields` (Task 5), `apollo_lookup` (Task 4), `draft_outreach_email` (Task 6), `create_gmail_draft` (Task 7), `fetch_jd_text_for_job` (Task 3).
- Produces: `run_outreach_step(job, jid, sector, jd_text, resume_text, pdf_path, founder_email_override=None) -> str` (one of `"skipped_sector"|"skipped_size"|"skipped_no_founder"|"drafted"|"failed"`). Consumed by Task 10's `outreach-one.sh`.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_run_outreach_step_skips_when_sector_is_none(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "")
    calls = []
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: calls.append(a))
    result = tr.run_outreach_step({"company_name": "Acme", "title": "SWE"}, "1", None, "jd", "resume", "pdf")
    assert result == "skipped_sector"
    assert calls == [("1", "skipped_sector")]


def test_run_outreach_step_does_not_auto_retry_terminal_skip(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "skipped_size")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: (_ for _ in ()).throw(AssertionError("should not be called")))
    result = tr.run_outreach_step({"company_name": "Acme"}, "1", "fintech", "jd", "resume", "pdf")
    assert result == "skipped_size"


def test_run_outreach_step_retries_after_failed_status(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "failed")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: {"employee_count": 10, "founder_name": "Jane", "founder_email": "jane@acme.xyz"})
    monkeypatch.setattr(tr, "draft_outreach_email", lambda *a, **k: {"subject": "Hi", "body": "Body"})
    monkeypatch.setattr(tr, "create_gmail_draft", lambda *a, **k: (True, None))
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: None)
    monkeypatch.setattr(tr, "send_telegram_message", lambda text: (True, None))
    result = tr.run_outreach_step({"company_name": "Acme", "title": "SWE"}, "1", "fintech", "jd", "resume", "pdf")
    assert result == "drafted"


def test_run_outreach_step_skips_when_company_too_big(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: {"employee_count": 500, "founder_name": "Jane", "founder_email": "jane@acme.xyz"})
    calls = []
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: calls.append(a))
    result = tr.run_outreach_step({"company_name": "Acme"}, "1", "fintech", "jd", "resume", "pdf")
    assert result == "skipped_size"
    assert calls == [("1", "skipped_size")]


def test_run_outreach_step_skips_when_no_founder_email(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: {"employee_count": 5, "founder_name": "", "founder_email": ""})
    calls = []
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: calls.append(a))
    result = tr.run_outreach_step({"company_name": "Acme"}, "1", "fintech", "jd", "resume", "pdf")
    assert result == "skipped_no_founder"
    assert calls == [("1", "skipped_no_founder", "")]


def test_run_outreach_step_fails_when_draft_generation_fails(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: {"employee_count": 5, "founder_name": "Jane", "founder_email": "jane@acme.xyz"})
    monkeypatch.setattr(tr, "draft_outreach_email", lambda *a, **k: None)
    calls = []
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: calls.append(a))
    result = tr.run_outreach_step({"company_name": "Acme", "title": "SWE"}, "1", "fintech", "jd", "resume", "pdf")
    assert result == "failed"
    assert calls == [("1", "failed", "Jane", "jane@acme.xyz")]


def test_run_outreach_step_bypasses_gate_with_founder_email_override(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: (_ for _ in ()).throw(AssertionError("should not be called")))
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: (_ for _ in ()).throw(AssertionError("should not be called")))
    monkeypatch.setattr(tr, "draft_outreach_email", lambda *a, **k: {"subject": "Hi", "body": "Body"})
    monkeypatch.setattr(tr, "create_gmail_draft", lambda *a, **k: (True, None))
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: None)
    monkeypatch.setattr(tr, "send_telegram_message", lambda text: (True, None))
    result = tr.run_outreach_step(
        {"company_name": "Acme", "title": "SWE"}, "1", None, "jd", "resume", "pdf",
        founder_email_override="manual@acme.xyz",
    )
    assert result == "drafted"


def test_run_outreach_step_sends_telegram_notification_on_success(monkeypatch):
    monkeypatch.setattr(tr, "get_outreach_status", lambda jid: "")
    monkeypatch.setattr(tr, "apollo_lookup", lambda name: {"employee_count": 5, "founder_name": "Jane", "founder_email": "jane@acme.xyz"})
    monkeypatch.setattr(tr, "draft_outreach_email", lambda *a, **k: {"subject": "Hi", "body": "Body"})
    monkeypatch.setattr(tr, "create_gmail_draft", lambda *a, **k: (True, None))
    monkeypatch.setattr(tr, "update_outreach_fields", lambda *a, **k: None)
    notified = []
    monkeypatch.setattr(tr, "send_telegram_message", lambda text: notified.append(text) or (True, None))
    tr.run_outreach_step({"company_name": "Acme", "title": "SWE"}, "1", "fintech", "jd", "resume", "pdf")
    assert len(notified) == 1
    assert "Jane" in notified[0]
    assert "Acme" in notified[0]
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m pytest scripts/test_tailor_resume.py -k run_outreach_step -v`
Expected: FAIL — `AttributeError: module 'tailor_resume' has no attribute 'run_outreach_step'`.

- [ ] **Step 3: Implement**

Add `send_telegram_message` right after `send_telegram` (line ~1091):

```python
def send_telegram_message(text):
    """Send a plain-text Telegram message with no document attachment --
    used for outreach-draft notifications, which have no PDF of their own
    to send (the resume PDF already went out with the original tailoring
    notification). Returns (True, None) on success, (False, error) on
    failure, same shape as send_telegram.
    """
    if not TG_TOKEN or not TG_CHAT:
        return False, "Telegram credentials not configured"
    cmd = [
        "curl", "-s", "-X", "POST",
        f"https://api.telegram.org/bot{TG_TOKEN}/sendMessage",
        "-F", f"chat_id={TG_CHAT}",
        "-F", f"text={text}",
    ]
    try:
        result = subprocess.run(cmd, capture_output=True, text=True, timeout=30)
        resp = json.loads(result.stdout)
        if resp.get("ok"):
            return True, None
        return False, f"Telegram API error: {resp.get('description', result.stdout)}"
    except Exception as e:
        return False, str(e)


def run_outreach_step(job, jid, sector, jd_text, resume_text, pdf_path, founder_email_override=None):
    """Attempt the founder-outreach draft for one already-tailored job.
    Automatic path (founder_email_override=None): gated on outreach_status
    being retryable (''/'failed', never re-attempted once terminally
    skipped/drafted), then sector, then Apollo's employee-count/founder
    gate. Manual override path (founder_email_override set, from a
    Telegram reply or dashboard action): bypasses the status/sector/size
    gate entirely and drafts straight to the given address -- the human
    already made the qualifying judgment call.

    Returns one of "skipped_sector"/"skipped_size"/"skipped_no_founder"/
    "drafted"/"failed". Never raises -- any failure downstream of the
    gate (LLM draft generation, Gmail API) resolves to "failed", which is
    retryable on the next automatic pass (see Task 5/spec's retry fix).
    """
    if founder_email_override:
        founder_name = ""
        founder_email = founder_email_override
    else:
        current_status = get_outreach_status(jid)
        if current_status not in ("", "failed"):
            log(f"  Outreach status={current_status!r}, not auto-retrying")
            return current_status
        if not sector:
            update_outreach_fields(jid, "skipped_sector")
            return "skipped_sector"

        apollo = apollo_lookup(job.get("company_name") or "")
        if not apollo or apollo["employee_count"] is None or apollo["employee_count"] > MAX_OUTREACH_EMPLOYEES:
            update_outreach_fields(jid, "skipped_size")
            return "skipped_size"
        if not apollo["founder_email"]:
            update_outreach_fields(jid, "skipped_no_founder", apollo["founder_name"])
            return "skipped_no_founder"

        founder_name = apollo["founder_name"]
        founder_email = apollo["founder_email"]

    draft = draft_outreach_email(jd_text, resume_text, founder_name, job.get("title", ""), job.get("company_name", ""))
    if not draft:
        update_outreach_fields(jid, "failed", founder_name, founder_email)
        return "failed"

    ok, err = create_gmail_draft(founder_email, draft["subject"], draft["body"], pdf_path)
    if not ok:
        log(f"  Gmail draft failed: {err}")
        update_outreach_fields(jid, "failed", founder_name, founder_email)
        return "failed"

    update_outreach_fields(jid, "drafted", founder_name, founder_email)
    send_telegram_message(
        f"Draft ready — {founder_name or founder_email} @ {job.get('company_name')}, "
        f"{job.get('title')} — check Gmail Drafts. #J{jid}"
    )
    return "drafted"
```

Wire it into `process_job` — right after `update_job_status(jid, "shortlisted")` (line ~1561) and before `return tailored, "sent"`:

```python
    try:
        run_outreach_step(job, jid, judgment.get("sector"), jd_text, resume_text, str(pdf_path))
    except Exception as e:
        log(f"  WARN: outreach step raised unexpectedly: {e}")

    return tailored, "sent"
```

Add the CLI dispatch at the bottom of the file, in the `if __name__ == "__main__":` block, as a new `elif` branch right after the existing `elif len(sys.argv) > 1 and sys.argv[1] == "--job-id":` block (before the final `else: main()`):

```python
    elif len(sys.argv) > 1 and sys.argv[1] == "--outreach":
        if len(sys.argv) < 4 or sys.argv[2] != "--job-id":
            print("Usage: tailor_resume.py --outreach --job-id <job_id> [--founder-email EMAIL]", file=sys.stderr)
            sys.exit(1)
        _job_id = sys.argv[3]
        _founder_email = None
        if len(sys.argv) > 5 and sys.argv[4] == "--founder-email":
            _founder_email = sys.argv[5]

        _conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
        _conn.row_factory = sqlite3.Row
        _row = _conn.execute("SELECT * FROM jobs WHERE id=?", (int(_job_id),)).fetchone()
        _conn.close()
        if not _row:
            print(f"JOB_ID_NOT_FOUND: {_job_id}", file=sys.stderr)
            sys.exit(1)
        _job = dict(_row)

        _tailored = load_tailored()
        _entry = _tailored.get(_job_id)
        if not _entry or _entry.get("status") != "done" or not _entry.get("pdf_path"):
            print(f"OUTREACH_FAILED: job {_job_id} has no successfully tailored resume yet", file=sys.stderr)
            sys.exit(1)

        _resume_text = ""
        if _entry.get("tex_path") and Path(_entry["tex_path"]).exists():
            _resume_text = Path(_entry["tex_path"]).read_text(encoding="utf-8")
        _jd_text, _ = fetch_jd_text_for_job(_job)
        _sector = _entry.get("sector")

        _outcome = run_outreach_step(_job, _job_id, _sector, _jd_text, _resume_text, _entry["pdf_path"], founder_email_override=_founder_email)
        print(f"OUTREACH_{_outcome.upper()}")
        if _outcome == "failed":
            sys.exit(1)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `python3 -m pytest scripts/test_tailor_resume.py -v`
Expected: PASS — all new tests pass, and every pre-existing `process_job`/`main` test still passes (the outreach call is wrapped in `try/except`, so it can never change `process_job`'s existing "sent"/"failed"/"skipped_non_eng" return contract).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Wire outreach step into process_job and add --outreach CLI entry point"
```

---

### Task 10: Bash wrapper for manual outreach trigger

**Files:**
- Create: `scripts/outreach-one.sh`

**Interfaces:**
- Consumes: `tailor_resume.py --outreach --job-id N [--founder-email EMAIL]` (Task 9).
- Produces: an executable invoked as `outreach-one.sh <job_id> [founder_email]`. Consumed by Task 11 (tgsync's `OutreachScript`) and Task 12 (Go dashboard endpoint).

No automated test — this is a thin bash wrapper mirroring `scripts/tailor-one.sh` exactly; its correctness is exercised by Task 11/12's Go tests (which invoke a *fake* script) and by the manual end-to-end check in Task 14.

- [ ] **Step 1: Create the script**

Create `scripts/outreach-one.sh`:

```bash
#!/usr/bin/env bash
# jobwatch outreach-one — runs the founder-outreach step for exactly one
# already-tailored job, on demand.
#
# Invoked two ways: jobwatch's Go tg-sync when a user replies "outreach"/
# "outreach: <email>" to a job notification, and the dashboard's "Draft
# outreach" button (POST /api/jobs/{id}/outreach). Mirrors tailor-one.sh's
# structure exactly.
#
# Usage: outreach-one.sh <job_id> [founder_email]

set -euo pipefail

JOB_ID="${1:-}"
FOUNDER_EMAIL="${2:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$ROOT_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

ARGS=(--outreach --job-id "$JOB_ID")
if [ -n "$FOUNDER_EMAIL" ]; then
  ARGS+=(--founder-email "$FOUNDER_EMAIL")
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" "${ARGS[@]}"
```

- [ ] **Step 2: Verify it's syntactically valid and executable**

Run: `chmod +x scripts/outreach-one.sh && bash -n scripts/outreach-one.sh && echo OK`
Expected: `OK` (bash `-n` checks syntax without running it).

- [ ] **Step 3: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add scripts/outreach-one.sh
git commit -m "Add outreach-one.sh wrapper for manual outreach triggers"
```

---

### Task 11: Telegram reply command — "outreach" / "outreach: email"

**Files:**
- Modify: `internal/tgsync/tgsync.go` (new `OutreachScript` field, `parseOutreach`, `runOutreach`, dispatch in `processUpdate`), `cmd/jobwatch/main.go:172-198` (`runTgSync`)
- Test: `internal/tgsync/tgsync_test.go`

**Interfaces:**
- Consumes: `scripts/outreach-one.sh <job_id> [founder_email]` (Task 10).
- Produces: `Syncer.OutreachScript string` field; a `Syncer` with it set replies to an "outreach"/"outreach: email" reply on a job notification by shelling to that script.

- [ ] **Step 1: Write the failing test**

Add to `internal/tgsync/tgsync_test.go`:

```go
func outreachUpdate(text string) []notify.Update {
	return []notify.Update{
		{
			UpdateID: 700,
			Message: &notify.Message{
				MessageID: 1200,
				Chat:      notify.Chat{ID: 555},
				Text:      text,
				ReplyToMessage: &notify.Message{
					MessageID: 1199,
					Chat:      notify.Chat{ID: 555},
					Text:      "#J1",
				},
			},
		},
	}
}

func TestParseOutreachBareCommand(t *testing.T) {
	email, ok := parseOutreach("outreach")
	if !ok || email != "" {
		t.Errorf("parseOutreach(\"outreach\") = (%q, %v), want (\"\", true)", email, ok)
	}
}

func TestParseOutreachWithEmailOverride(t *testing.T) {
	email, ok := parseOutreach("outreach: jane@acme.xyz")
	if !ok || email != "jane@acme.xyz" {
		t.Errorf("parseOutreach(\"outreach: jane@acme.xyz\") = (%q, %v), want (\"jane@acme.xyz\", true)", email, ok)
	}
}

func TestParseOutreachNoMatch(t *testing.T) {
	if _, ok := parseOutreach("applied"); ok {
		t.Errorf("parseOutreach(\"applied\") matched, want no match")
	}
}

func TestRunOutreachNotConfigured(t *testing.T) {
	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: outreachUpdate("outreach")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555} // OutreachScript left empty

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != "Outreach isn't configured on this install." {
		t.Errorf("expected not-configured reply, got %+v", fake.replies)
	}
}

func TestRunOutreachInvokesScriptWithJobIDAndEmail(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "outreach.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: outreachUpdate("outreach: jane@acme.xyz")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, OutreachScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.replies) != 1 || fake.replies[0].Text != "Looking up founder / drafting outreach…" {
		t.Errorf("expected immediate ack reply, got %+v", fake.replies)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	if strings.TrimSpace(string(gotArgs)) != "1 jane@acme.xyz" {
		t.Errorf("script args = %q, want \"1 jane@acme.xyz\"", strings.TrimSpace(string(gotArgs)))
	}
}

func TestRunOutreachReportsScriptFailure(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "outreach.sh")
	script := "#!/bin/sh\necho 'OUTREACH_FAILED: no founder found' >&2\nexit 1\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: outreachUpdate("outreach")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, OutreachScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 2 {
		t.Fatalf("replies = %+v, want ack + failure message", fake.replies)
	}
	if !strings.Contains(fake.replies[1].Text, "Outreach failed for #J1") {
		t.Errorf("replies[1] = %q, want it to mention the failure", fake.replies[1].Text)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions && go test ./internal/tgsync/... -run TestParseOutreach -v`
Expected: FAIL — `undefined: parseOutreach`.

- [ ] **Step 3: Implement**

Add `OutreachScript` to the `Syncer` struct in `internal/tgsync/tgsync.go` (right after `FixScript`, line ~60):

```go
	// OutreachScript is the executable invoked when a user replies
	// "outreach" or "outreach: <email>" to a job notification:
	// `OutreachScript <job_id> [<founder_email>]`. Looks up a founder
	// (or uses the given email override) and creates a personalized
	// Gmail draft. Empty disables the feature (replies explaining it
	// isn't configured), same convention as FixScript.
	OutreachScript string
```

Add `regexp` var and `parseOutreach` near `fixOrUpdateRe`/`parseFixOrUpdate` (line ~256):

```go
// outreachRe matches a bare "outreach" reply or "outreach: <email>" with
// an explicit founder-email override -- optional whitespace around the
// colon, same convention as fixOrUpdateRe.
var outreachRe = regexp.MustCompile(`(?is)^outreach\s*(?::\s*(\S+))?\s*$`)

// parseOutreach recognizes an "outreach"/"outreach: <email>" reply. It
// reports the email override (empty for a bare "outreach", meaning "use
// the automatic Apollo lookup") and whether text matched this command at
// all.
func parseOutreach(text string) (email string, ok bool) {
	m := outreachRe.FindStringSubmatch(strings.TrimSpace(text))
	if m == nil {
		return "", false
	}
	return m[1], true
}
```

Add the dispatch in `processUpdate`, right after the `note:` block and before the generic `fields := strings.Fields(lower)` status-keyword fallback (line ~198):

```go
	if email, ok := parseOutreach(text); ok {
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			return false, err
		}
		return false, s.runOutreach(ctx, jobID, msg.MessageID, email)
	}
```

Add `runOutreach` right after `runFix` (line ~319):

```go
// runOutreach invokes OutreachScript to look up a founder (or use the
// given email override) and create a Gmail draft. Mirrors runFix's
// ack-then-background-script pattern: the actual "draft ready" success
// notification comes from tailor_resume.py's own Telegram send inside
// run_outreach_step, not from this reply -- this only acks immediately
// and reports a failure if the script errors.
func (s *Syncer) runOutreach(ctx context.Context, jobID int64, replyToMessageID int64, founderEmail string) error {
	if s.OutreachScript == "" {
		return s.TG.Reply(ctx, replyToMessageID, "Outreach isn't configured on this install.")
	}

	if err := s.TG.Reply(ctx, replyToMessageID, "Looking up founder / drafting outreach…"); err != nil {
		return err
	}

	outreachCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	args := []string{strconv.FormatInt(jobID, 10)}
	if founderEmail != "" {
		args = append(args, founderEmail)
	}
	cmd := exec.CommandContext(outreachCtx, s.OutreachScript, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("tg-sync: outreach script failed", "job_id", jobID, "error", err, "output", string(output))
		return s.TG.Reply(ctx, replyToMessageID, fmt.Sprintf("Outreach failed for #J%d: %s", jobID, lastLine(string(output))))
	}
	return nil
}
```

Update `internal/tgsync/tgsync.go`'s `New()` (line ~67) and `cmd/jobwatch/main.go`'s `runTgSync` (line ~172-198) to wire a new flag through:

```go
func New(st *store.Store, tg *notify.Telegram, chatID int64, fixScript string, outreachScript string) *Syncer {
	return &Syncer{Store: st, TG: tg, ChatID: chatID, FixScript: fixScript, OutreachScript: outreachScript, Pages: jobsubmit.HTTPPageFetcher{}}
}
```

In `cmd/jobwatch/main.go`, right after the existing `fixScript := fs.String(...)` line (~175):

```go
	outreachScript := fs.String("outreach-script", "scripts/outreach-one.sh",
		"path to the script invoked when a user replies \"outreach\" to a job notification (empty disables the feature)")
```

And update the `tgsync.New(...)` call (~line 198):

```go
	syncer := tgsync.New(st, tg, chatID, *fixScript, *outreachScript)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tgsync/... -v && go build ./...`
Expected: PASS, build succeeds (confirms `main.go`'s updated `New()` call compiles).

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add internal/tgsync/tgsync.go internal/tgsync/tgsync_test.go cmd/jobwatch/main.go
git commit -m "Add outreach Telegram reply command (outreach / outreach: email)"
```

---

### Task 12: Dashboard endpoint — `POST /api/jobs/{id}/outreach`

**Files:**
- Modify: `internal/web/api.go` (new `apiJob` fields, `toAPIJob`, new handler), `internal/web/server.go:41-46` (route registration)
- Test: `internal/web/api_test.go`

**Interfaces:**
- Consumes: `scripts/outreach-one.sh <job_id> [founder_email]` (Task 10), `store.JobRow`'s new fields (Task 1).
- Produces: `POST /api/jobs/{id}/outreach` — `202 {"id": <id>}` on success, `400` on invalid id, `404` if job doesn't exist. Consumed by Task 13's frontend.

- [ ] **Step 1: Write the failing test**

Add to `internal/web/api_test.go`:

```go
func TestHandleAPIJobsOutreachTriggersScript(t *testing.T) {
	srv, st := newTestServer(t)
	srv.logsDir = t.TempDir() // see comment in TestHandleAPIJobsManualInsertsAndTriggersOneOff
	id := insertTestJob(t, st)

	body := strings.NewReader(`{"founderEmail":"jane@acme.xyz"}`)
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/jobs/%d/outreach", id), body)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202, body: %s", w.Code, w.Body.String())
	}
}

func TestHandleAPIJobsOutreachRejectsInvalidID(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/notanumber/outreach", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestHandleAPIJobsOutreachRejects404ForMissingJob(t *testing.T) {
	srv, _ := newTestServer(t)

	req := httptest.NewRequest(http.MethodPost, "/api/jobs/999/outreach", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandleAPIJobsIncludesOutreachFields(t *testing.T) {
	srv, st := newTestServer(t)
	insertTestJob(t, st)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	w := httptest.NewRecorder()
	srv.Handler().ServeHTTP(w, req)

	var resp apiJobsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Jobs) != 1 || resp.Jobs[0].OutreachStatus != "" {
		t.Errorf("Jobs[0].OutreachStatus = %q, want empty string by default", resp.Jobs[0].OutreachStatus)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/web/... -run TestHandleAPIJobsOutreach -v`
Expected: FAIL — `404 page not found` (route doesn't exist yet).

- [ ] **Step 3: Implement**

Add fields to `apiJob` in `internal/web/api.go` (line ~17-27) and `toAPIJob`:

```go
type apiJob struct {
	ID                int64  `json:"id"`
	CompanySlug       string `json:"companySlug"`
	CompanyName       string `json:"companyName"`
	Title             string `json:"title"`
	Location          string `json:"location"`
	URL               string `json:"url"`
	PostedAt          string `json:"postedAt"`
	FirstSeenAt       string `json:"firstSeenAt"`
	Status            string `json:"status"`
	Notes             string `json:"notes"`
	OutreachStatus    string `json:"outreachStatus"`
	FounderName       string `json:"founderName"`
	FounderEmail      string `json:"founderEmail"`
	OutreachDraftedAt string `json:"outreachDraftedAt"`
}

func toAPIJob(j store.JobRow) apiJob {
	return apiJob{
		ID:                j.ID,
		CompanySlug:       j.CompanySlug,
		CompanyName:       j.CompanyName,
		Title:             j.Title,
		Location:          j.Location,
		URL:               j.URL,
		PostedAt:          j.PostedAt.String,
		FirstSeenAt:       j.FirstSeenAt,
		Status:            j.Status,
		Notes:             j.Notes,
		OutreachStatus:    j.OutreachStatus,
		FounderName:       j.FounderName,
		FounderEmail:      j.FounderEmail,
		OutreachDraftedAt: j.OutreachDraftedAt,
	}
}
```

Add `"database/sql"` to the import block (line ~4, alongside `"encoding/json"`).

Add the new handler at the end of `internal/web/api.go`, after `handleAPIJobsManual`:

```go
// outreachOneScript is the wrapper spawned for a manual outreach trigger
// (dashboard button or Telegram "outreach" reply), relative to the
// process's cwd -- same convention as tailorOneScript.
const outreachOneScript = "scripts/outreach-one.sh"

type apiOutreachRequest struct {
	FounderEmail string `json:"founderEmail"`
}

// handleAPIJobsOutreach triggers a one-off founder-outreach attempt for
// an existing job from the dashboard. Mirrors handleAPIJobsManual's
// detached-exec pattern -- the HTTP response doesn't wait for the
// lookup/draft to finish; the result surfaces via Telegram (see
// run_outreach_step's own notification) or a later dashboard refresh of
// outreachStatus.
func (s *Server) handleAPIJobsOutreach(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	var req apiOutreachRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req) // optional body; malformed/empty is fine, just no override
	}

	if _, err := s.store.GetJob(ctx, id); err != nil {
		if err == sql.ErrNoRows {
			http.Error(w, "job not found", http.StatusNotFound)
			return
		}
		httpError(w, "loading job", err)
		return
	}

	logFile, err := os.OpenFile(filepath.Join(s.logsDir, "cron.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		httpError(w, "opening cron.log", err)
		return
	}

	args := []string{outreachOneScript, strconv.FormatInt(id, 10)}
	if req.FounderEmail != "" {
		args = append(args, req.FounderEmail)
	}
	cmd := exec.Command("bash", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile

	if err := cmd.Start(); err != nil {
		logFile.Close()
		httpError(w, "starting outreach-one", err)
		return
	}

	go func() {
		defer logFile.Close()
		if err := cmd.Wait(); err != nil {
			slog.Error("outreach-one exited non-zero", "job_id", id, "error", err)
		}
	}()

	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]any{"id": id})
}
```

Register the route in `internal/web/server.go` (line ~44, after `PATCH /api/jobs/{id}`):

```go
	mux.HandleFunc("POST /api/jobs/{id}/outreach", s.handleAPIJobsOutreach)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/web/... -v && go build ./... && gofmt -l .`
Expected: PASS, build succeeds, `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add internal/web/api.go internal/web/server.go internal/web/api_test.go
git commit -m "Add POST /api/jobs/{id}/outreach dashboard trigger endpoint"
```

---

### Task 13: Frontend — "Draft outreach" action + outreach status display

**Files:**
- Modify: `frontend/src/api.ts` (new `Job` fields, `triggerOutreach`), `frontend/src/pages/Jobs.tsx` (new column + action)

**Interfaces:**
- Consumes: `POST /api/jobs/{id}/outreach` (Task 12), `Job.outreachStatus`/`founderName`/`founderEmail` fields on the existing `GET /api/jobs` response.

No new automated test — this codebase has no frontend test suite (confirmed: no `*.test.tsx`/`*.spec.tsx` files, verification is `tsc`/`oxlint`/`vite build` + manual click-through, same as the 2026-07-27 jobs-filter-upgrade task). Steps 2-3 below are that same verification.

- [ ] **Step 1: Implement**

In `frontend/src/api.ts`, add fields to `Job` (line ~1-11):

```typescript
export interface Job {
  id: number
  companySlug: string
  companyName: string
  title: string
  location: string
  url: string
  postedAt: string
  firstSeenAt: string
  status: string
  notes: string
  outreachStatus: string
  founderName: string
  founderEmail: string
  outreachDraftedAt: string
}
```

Add a new function after `submitManualJob`:

```typescript
export async function triggerOutreach(id: number, founderEmail?: string): Promise<void> {
  const res = await fetch(`/api/jobs/${id}/outreach`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(founderEmail ? { founderEmail } : {}),
  })
  if (!res.ok) throw new Error(`POST /api/jobs/${id}/outreach: ${res.status}`)
}
```

In `frontend/src/pages/Jobs.tsx`, add the import (line 2):

```typescript
import { fetchJobs, patchJob, submitManualJob, triggerOutreach, type JobsResponse } from '../api'
```

Add state near the other `useState` declarations (line ~26):

```typescript
  const [outreachOverride, setOutreachOverride] = useState<Record<number, string>>({})
  const [outreachStatusMsg, setOutreachStatusMsg] = useState<Record<number, string>>({})
```

Add a handler near `updateNotes` (line ~111):

```typescript
  async function handleTriggerOutreach(id: number) {
    setOutreachStatusMsg((m) => ({ ...m, [id]: 'Queued…' }))
    try {
      await triggerOutreach(id, outreachOverride[id]?.trim() || undefined)
      setOutreachStatusMsg((m) => ({ ...m, [id]: 'Queued — check Gmail Drafts / Telegram' }))
    } catch (err) {
      setOutreachStatusMsg((m) => ({ ...m, [id]: `Failed: ${err instanceof Error ? err.message : String(err)}` }))
    }
  }
```

Add a new `<th>Outreach</th>` to the table header (line ~211, after `<th>Notes</th>`), and a matching `<td>` in the row-rendering (after the notes `<td>`, line ~285):

```tsx
                <td className="outreach-cell">
                  {job.outreachStatus === 'drafted' ? (
                    <span className="outreach-drafted">
                      Drafted — {job.founderName || job.founderEmail}
                    </span>
                  ) : (
                    <>
                      <input
                        type="email"
                        placeholder="Override founder email (optional)"
                        value={outreachOverride[job.id] ?? ''}
                        onChange={(e) => setOutreachOverride((m) => ({ ...m, [job.id]: e.target.value }))}
                      />
                      <button type="button" onClick={() => handleTriggerOutreach(job.id)}>
                        Draft outreach
                      </button>
                    </>
                  )}
                  {outreachStatusMsg[job.id] && <div className="outreach-status-msg">{outreachStatusMsg[job.id]}</div>}
                </td>
```

Update the header row's `colSpan={6}` (empty-state row, line ~232) to `colSpan={7}` to match the new column count.

- [ ] **Step 2: Type-check and build**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions/frontend
npx tsc --noEmit
npx oxlint .
npm run build
```

Expected: all three clean/succeed, no type errors.

- [ ] **Step 3: Manual click-through** (once the Go backend from Task 12 is running locally)

Start the dashboard locally, open the Jobs page, confirm: the new "Outreach" column renders for every row, typing an email into the override box and clicking "Draft outreach" fires the request (check Network tab for `202`), and a row with `outreachStatus: "drafted"` (you can set this manually via `sqlite3 jobwatch.db "UPDATE jobs SET outreach_status='drafted', founder_name='Test Founder' WHERE id=1"` for a quick visual check) renders the "Drafted — ..." state instead of the button.

- [ ] **Step 4: Commit**

```bash
cd /home/dev-mayur/jobwatch/.claude/worktrees/reply-instructions
git add frontend/src/api.ts frontend/src/pages/Jobs.tsx
git commit -m "Add Draft outreach action and outreach status column to Jobs page"
```

---

### Task 14: End-to-end manual verification

No code changes — this is the final real-world check, matching this project's established convention (see `HANDOFF.md`'s "Verification habits" and `feedback: verify_generated_output`) of confirming against the real system, not just green tests, before calling a feature done.

- [ ] **Step 1: Full pipeline dry run against one real job**

In the main repo root (`/home/dev-mayur/jobwatch`, which has `.env` and the real DB):

```bash
cd /home/dev-mayur/jobwatch
go build -o bin/jobwatch ./cmd/jobwatch  # picks up Task 1's migration on next store.Open
source .env
python3 scripts/tailor_resume.py --job-id <a real job id for a small crypto/fintech/ai startup>
```

Confirm: the existing tailoring/Telegram flow still works exactly as before (no regression from Task 2/3's `llm_judge`/`process_job` changes), and check `jobwatch.db`'s `outreach_status` column for that job afterward:

```bash
sqlite3 jobwatch.db "SELECT id, outreach_status, founder_name, founder_email FROM jobs WHERE id=<job id>;"
```

- [ ] **Step 2: Confirm a real Gmail draft appears (once Task 8's OAuth setup is done)**

Check the Gmail account's Drafts folder directly — confirm a draft exists with the expected founder's email as recipient, a personalized subject/body, and the tailored resume PDF attached. This is the one part of the feature no unit test can substitute for (per project convention: compile/render or a live API call before claiming done).

- [ ] **Step 3: Confirm the Telegram reply path**

Reply "outreach" to that job's original tailoring notification in Telegram. Confirm the immediate ack ("Looking up founder / drafting outreach…") arrives, followed by either the "Draft ready" notification or a clear failure message.

- [ ] **Step 4: Confirm the dashboard path**

From the Jobs page, click "Draft outreach" (with an email override) on a different job. Confirm the request succeeds and, after the script finishes, the row's `outreach_status` updates on the next page refresh.
