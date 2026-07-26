# Eligibility Filter Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop non-engineering and out-of-scope-seniority jobs (staff/principal/director/lead titles, and roles whose JD body demands more than 3 years of experience) from reaching the dashboard or getting tailored resumes, and fix a real word-boundary bug in the existing keyword filter along the way.

**Architecture:** Two independent filter layers get the same class of fix. Layer 1 (`internal/poller/filter.go`, Go, ingestion-time, title-only) switches its keyword matching from plain substring to `\b`-bounded regex, and gains a new `lead` exclude keyword. Layer 2 (`scripts/tailor_resume.py`, Python, tailoring-time, has JD body text) gets the same word-boundary fix for `is_engineering_role`, plus a brand-new years-of-experience cap check (`exceeds_experience_cap`) wired into `process_job`.

**Tech Stack:** Go (`regexp` stdlib), Python (`re` stdlib), existing `go test` / `pytest` suites.

## Global Constraints

- Experience cap is 3 years: any JD body stating a minimum requirement above 3 years causes a skip. Copied verbatim from the user's own statement: "up to 3, is fine, we can manage and justify with current skills but nobody will even look the resume for 4-5+ yrs."
- All keyword matching (Go exclude/include, Python non-eng/eng-terms) must be `\b`-bounded, not plain substring — fixes a live bug where `"intern"` matches inside `"international"`.
- `go test ./...` and `gofmt -l .` (empty) must pass. Python: `pytest scripts/test_tailor_resume.py -v` must pass.
- After merge to `master`, changes must be verified live against the deployed EC2 instance (`16.113.24.110`), not just CI green — per `CLAUDE.md`'s "Verify after every deploy" rule.

---

### Task 1: Go word-boundary keyword matching + `lead` exclude keyword

**Files:**
- Modify: `internal/poller/filter.go`
- Modify: `config.yaml` (exclude_keywords list)
- Test: `internal/poller/filter_test.go`

**Interfaces:**
- Produces: `matchesAny(haystack string, needles []string) bool` — new function, used by `Passes` for title include/exclude matching. `containsAny` keeps its existing signature and behavior, now used only for location matching.
- No change to `Passes(job providers.Job, f config.Filters) bool`'s signature — only its internal matching behavior changes.

- [ ] **Step 1: Write the failing tests**

Replace the full contents of `internal/poller/filter_test.go` with:

```go
package poller

import (
	"testing"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
)

func TestPasses(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords:  []string{"backend", "software engineer", "sde", "platform", "golang", "python"},
		ExcludeKeywords:  []string{"staff", "principal", "director", "manager", "intern", "10+ years", "lead"},
		LocationsInclude: []string{"india", "hyderabad", "bengaluru", "bangalore", "remote"},
	}

	tests := []struct {
		name  string
		title string
		loc   string
		want  bool
	}{
		{"matches backend + bengaluru", "Backend Engineer", "Bengaluru, India", true},
		{"matches golang + remote", "Golang Developer", "Remote - India", true},
		{"excluded by staff", "Staff Backend Engineer", "Bengaluru", false},
		{"excluded by manager", "Engineering Manager, Platform", "Remote", false},
		{"no include keyword match", "Product Designer", "Bengaluru", false},
		{"location not allowed", "Backend Engineer", "London, UK", false},
		{"case insensitive include", "BACKEND ENGINEER", "INDIA", true},
		{"case insensitive exclude", "STAFF Engineer", "India", false},
		{"excluded by lead as whole word", "Team Lead, Backend Engineer", "Remote", false},
		{"not excluded: intern substring inside international", "International Backend Engineer", "Remote", true},
		{"not excluded: lead substring inside leadership", "Leadership Program Backend Engineer", "Remote", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := providers.Job{Title: tt.title, Location: tt.loc}
			got := Passes(job, filters)
			if got != tt.want {
				t.Errorf("Passes(title=%q, loc=%q) = %v, want %v", tt.title, tt.loc, got, tt.want)
			}
		})
	}
}

func TestPassesEmptyLocationFilterAllowsAll(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords:  []string{"backend"},
		LocationsInclude: []string{},
	}
	job := providers.Job{Title: "Backend Engineer", Location: "Antarctica"}
	if !Passes(job, filters) {
		t.Error("expected job to pass when locations_include is empty")
	}
}

func TestPassesEmptyIncludeAllowsAllTitles(t *testing.T) {
	filters := config.Filters{
		IncludeKeywords: []string{},
		ExcludeKeywords: []string{"manager"},
	}
	job := providers.Job{Title: "Random Title With No Keywords", Location: ""}
	if !Passes(job, filters) {
		t.Error("expected job to pass when include_keywords is empty and no exclude match")
	}
}
```

- [ ] **Step 2: Run tests to verify the new cases fail**

Run: `go test ./internal/poller/... -run TestPasses -v`
Expected: FAIL on `"excluded by lead as whole word"` (current `exclude_keywords` in the test doesn't yet cause a title-check issue — actually since `filter.go` is unchanged and still does plain substring matching, `"not excluded: intern substring inside international"` and `"not excluded: lead substring inside leadership"` FAIL because current substring logic wrongly excludes both).

- [ ] **Step 3: Implement word-boundary matching in filter.go**

Replace the full contents of `internal/poller/filter.go` with:

```go
package poller

import (
	"regexp"
	"strings"

	"jobwatch/internal/config"
	"jobwatch/internal/providers"
)

// Passes reports whether a job matches the configured filters:
// title must contain at least one include keyword (if any are configured),
// must not contain any exclude keyword, and location must match at least
// one entry in locations_include (if any are configured). Title keyword
// matching is case-insensitive and whole-word/whole-phrase (a keyword like
// "intern" must not match inside an unrelated longer word like
// "international" -- location matching stays plain substring since city/
// region names don't have that failure mode).
func Passes(job providers.Job, f config.Filters) bool {
	title := strings.ToLower(job.Title)

	if len(f.ExcludeKeywords) > 0 && matchesAny(title, f.ExcludeKeywords) {
		return false
	}

	if len(f.IncludeKeywords) > 0 && !matchesAny(title, f.IncludeKeywords) {
		return false
	}

	if len(f.LocationsInclude) > 0 {
		location := strings.ToLower(job.Location)
		if !containsAny(location, f.LocationsInclude) {
			return false
		}
	}

	return true
}

// matchesAny reports whether haystack contains any needle as a whole word
// or phrase, \b-bounded so a short needle doesn't match inside an
// unrelated longer word. Needles are literal text -- regex metacharacters
// (e.g. the "+" in "10+ years") are escaped, not treated as patterns.
func matchesAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		pattern := `\b` + regexp.QuoteMeta(strings.ToLower(n)) + `\b`
		if regexp.MustCompile(pattern).MatchString(haystack) {
			return true
		}
	}
	return false
}

// containsAny reports whether haystack contains any needle as a plain
// substring. Used for location matching only.
func containsAny(haystack string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(haystack, strings.ToLower(n)) {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/poller/... -v`
Expected: PASS (all `TestPasses` subtests, `TestPassesEmptyLocationFilterAllowsAll`, `TestPassesEmptyIncludeAllowsAllTitles`)

- [ ] **Step 5: Add `lead` to config.yaml's exclude_keywords**

In `config.yaml`, find:

```yaml
  exclude_keywords: ["staff", "principal", "director", "manager", "intern", "10+ years"]
```

Replace with:

```yaml
  exclude_keywords: ["staff", "principal", "director", "manager", "intern", "10+ years", "lead"]
```

- [ ] **Step 6: Run full Go test suite and gofmt check**

Run: `go test ./... && gofmt -l .`
Expected: all tests PASS, `gofmt -l .` prints nothing

- [ ] **Step 7: Commit**

```bash
git add internal/poller/filter.go internal/poller/filter_test.go config.yaml
git commit -m "Fix substring false-positives in title filter; add lead to exclude list"
```

---

### Task 2: Python word-boundary matching for `is_engineering_role`

**Files:**
- Modify: `scripts/tailor_resume.py` (the `is_engineering_role` function, ~line 1098)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: nothing new.
- Produces: `is_engineering_role(title, jd_text) -> bool` — same signature as before, behavior fixed. New private helper `_contains_word(text, phrase) -> bool`.

**Note on scope:** unlike Task 1 (where `"intern"` matching inside `"international"` is a live, demonstrable bug in `config.yaml`'s Go-layer exclude list), Python's `non_eng` list doesn't contain `"intern"` at all -- title-level intern blocking happens only in the Go layer. This task is a defensive/consistency refactor: same word-boundary pattern as Task 1, applied here so future additions to `non_eng`/`eng_terms` don't reintroduce the same class of bug, and so the existing `"hr "` trailing-space workaround (a hand-rolled partial fix for the same problem) can be replaced with the general fix. The tests below are regression guards that already pass today and must keep passing after the refactor -- there's no fabricated red step here since there's no currently-broken behavior to demonstrate at this layer.

- [ ] **Step 1: Write the regression-guard tests**

Add to `scripts/test_tailor_resume.py` (after `test_process_job_skips_non_engineering_role`, ~line 490):

```python
def test_is_engineering_role_hr_hack_no_longer_needed():
    # "hr " (with a trailing space) was a hand-rolled workaround to avoid
    # matching inside unrelated words like "Chrome" -- confirm the
    # word-boundary version still correctly allows such titles.
    assert tr.is_engineering_role("Chrome Extension Engineer", "") is True


def test_is_engineering_role_still_blocks_hr_titles():
    assert tr.is_engineering_role("HR Business Partner", "") is False


def test_is_engineering_role_still_blocks_legal_titles():
    assert tr.is_engineering_role("Corporate Counsel", "") is False
```

- [ ] **Step 2: Run tests against the current code to confirm the baseline**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -k "engineering_role" -v`
Expected: PASS (all three) -- this confirms the current substring-based implementation already handles these specific cases correctly, so Step 3's refactor must not regress them.

- [ ] **Step 3: Implement word-boundary matching**

In `scripts/tailor_resume.py`, replace the `is_engineering_role` function (~line 1098-1120):

```python
def is_engineering_role(title, jd_text):
    """Skip clearly non-engineering roles that violate truth lock."""
    text = (title + " " + jd_text).lower()
    non_eng = [
        "account executive", "sales executive", "business development",
        "cloud billing associate", "billing operations", "billing analyst",
        "lead, cloud billing operations", "senior lead, cloud billing",
        "recruiter", "hr ", "human resources", "marketing", "finance manager",
        "accountant", "bookkeeper", "legal", "counsel", "paralegal",
        "office manager", "administrative", "executive assistant"
    ]
    for term in non_eng:
        if term in text:
            return False
    # Must contain an engineering keyword
    eng_terms = [
        "engineer", "engineering", "developer", "sde", "swe", "software",
        "data engineer", "ai engineer", "ml engineer", "backend", "frontend",
        "fullstack", "full stack", "devops", "sre", "site reliability",
        "infrastructure", "platform", "solutions engineer", "solutions architect",
        "forward deployed", "technical solutions", "systems engineer"
    ]
    return any(term in text for term in eng_terms)
```

with:

```python
def _contains_word(text, phrase):
    """Whole-word/whole-phrase, case-sensitive-as-given substring check.
    Callers pass already-lowercased text and phrases. \\b-bounded so a
    short phrase (e.g. "hr") doesn't match inside an unrelated longer
    word (e.g. "chr" is nonsense, but this also stops "legal" nested
    matches and similar false positives across the whole list).
    """
    return re.search(r'\b' + re.escape(phrase) + r'\b', text) is not None


def is_engineering_role(title, jd_text):
    """Skip clearly non-engineering roles that violate truth lock."""
    text = (title + " " + jd_text).lower()
    non_eng = [
        "account executive", "sales executive", "business development",
        "cloud billing associate", "billing operations", "billing analyst",
        "lead, cloud billing operations", "senior lead, cloud billing",
        "recruiter", "hr", "human resources", "marketing", "finance manager",
        "accountant", "bookkeeper", "legal", "counsel", "paralegal",
        "office manager", "administrative", "executive assistant"
    ]
    for term in non_eng:
        if _contains_word(text, term):
            return False
    # Must contain an engineering keyword
    eng_terms = [
        "engineer", "engineering", "developer", "sde", "swe", "software",
        "data engineer", "ai engineer", "ml engineer", "backend", "frontend",
        "fullstack", "full stack", "devops", "sre", "site reliability",
        "infrastructure", "platform", "solutions engineer", "solutions architect",
        "forward deployed", "technical solutions", "systems engineer"
    ]
    return any(_contains_word(text, term) for term in eng_terms)
```

Note the `"hr "` trailing-space workaround is gone -- `_contains_word` makes it unnecessary and it would have double-blocked correctly formed text anyway (word-boundary already stops "hr" from matching inside "chr" or similar).

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -k "engineering_role" -v`
Expected: PASS for both new tests and the existing `test_process_job_skips_non_engineering_role`.

- [ ] **Step 5: Run full Python test suite**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -v`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Fix word-boundary matching in is_engineering_role"
```

---

### Task 3: Years-of-experience cap check

**Files:**
- Modify: `scripts/tailor_resume.py` (new functions near `is_engineering_role`; new branch in `process_job` at ~line 1356-1367)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `is_engineering_role` (Task 2, already in file), `process_job`'s existing local variables `jid, company, title, url, jd_text, tailored`.
- Produces: `max_required_years(jd_text) -> int | None`, `exceeds_experience_cap(jd_text, cap=MAX_YEARS_CAP) -> bool`, module constant `MAX_YEARS_CAP = 3`. `process_job` gains a third possible outcome string: `"skipped_over_experience"` (alongside existing `"sent"`/`"failed"`/`"skipped_non_eng"`).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py` (after the Task 2 tests):

```python
def test_max_required_years_plus_pattern():
    assert tr.max_required_years("5+ years of experience required") == 5


def test_max_required_years_range_pattern_uses_lower_bound():
    assert tr.max_required_years("3-5 years of experience") == 3


def test_max_required_years_no_requirement_stated():
    assert tr.max_required_years("Great team, fast-paced environment.") is None


def test_max_required_years_picks_highest_stated_minimum():
    text = "2+ years with Python. 6+ years of overall professional experience."
    assert tr.max_required_years(text) == 6


def test_exceeds_experience_cap_true_above_cap():
    assert tr.exceeds_experience_cap("Requires 5+ years of experience.") is True


def test_exceeds_experience_cap_false_at_cap():
    assert tr.exceeds_experience_cap("Requires 3+ years of experience.") is False


def test_exceeds_experience_cap_false_when_unstated():
    assert tr.exceeds_experience_cap("Backend Engineer role.") is False


def test_process_job_skips_over_experience_role(monkeypatch, tmp_path):
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tmp_path / "tailored.json")
    monkeypatch.setattr(tr, "fetch_greenhouse_job", lambda slug, gh_id: None)
    monkeypatch.setattr(
        tr, "fetch_jd_generic",
        lambda url: {
            "title": "", "company_name": "",
            "content_text": (
                "Backend Engineer role. We are looking for a strong software "
                "engineer to join our platform team. Requires 6+ years of "
                "professional experience building distributed systems at scale."
            ),
            "content_html": "", "location": "", "absolute_url": url,
        },
    )
    job = {"id": 3, "company_name": "Acme", "title": "Backend Engineer", "url": "https://example.com/3"}
    tailored, outcome = tr.process_job(job, {})
    assert outcome == "skipped_over_experience"
    assert tailored["3"]["status"] == "ignored"
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -k "required_years or experience_cap or over_experience" -v`
Expected: FAIL with `AttributeError: module 'tailor_resume' has no attribute 'max_required_years'` (and similarly for `exceeds_experience_cap`).

- [ ] **Step 3: Implement the years-cap functions and wire into process_job**

In `scripts/tailor_resume.py`, immediately after the `is_engineering_role` function (end of Task 2's replacement block), add:

```python
MAX_YEARS_CAP = 3

YEARS_EXPERIENCE_RE = re.compile(
    r"(\d{1,2})\s*\+?\s*(?:-\s*\d{1,2}\s*)?\+?\s*years?\s*(?:\S+\s+){0,3}exp(?:erience)?\b",
    re.IGNORECASE,
)

def max_required_years(jd_text):
    """Return the highest stated minimum years-of-experience requirement
    found in jd_text (e.g. "5+ years of experience" -> 5, "3-5 years of
    experience" -> 3, the lower bound of a range), or None if the text
    states no such requirement.
    """
    matches = YEARS_EXPERIENCE_RE.findall(jd_text or "")
    if not matches:
        return None
    return max(int(m) for m in matches)

def exceeds_experience_cap(jd_text, cap=MAX_YEARS_CAP):
    """True if jd_text states a minimum years-of-experience requirement
    greater than cap. False (fail-open) if no requirement is stated,
    matching how thin/unavailable JDs already fail open elsewhere in
    process_job.
    """
    years = max_required_years(jd_text)
    return years is not None and years > cap
```

Then in `process_job`, find the existing block (~line 1356-1367):

```python
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
```

Add immediately after it (still before `comp_slug = company_slug(company, url)`):

```python

    if exceeds_experience_cap(jd_text):
        required = max_required_years(jd_text)
        log(f"SKIPPED (over experience cap): {title} (requires {required}+ years)")
        tailored[jid] = {
            "company": company, "title": title, "url": url,
            "pdf_path": None, "tex_path": None, "coverage_score": None,
            "status": "ignored",
            "error": f"Requires {required}+ years experience (cap: {MAX_YEARS_CAP}); skipped per truth lock.",
            "tailored_date": datetime.now().isoformat(),
        }
        save_tailored(tailored)
        update_job_status(jid, "ignored")
        return tailored, "skipped_over_experience"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -k "required_years or experience_cap or over_experience" -v`
Expected: PASS

- [ ] **Step 5: Run full Python test suite**

Run: `cd scripts && python3 -m pytest test_tailor_resume.py -v`
Expected: all PASS (including the pre-existing `test_process_job_skips_non_engineering_role` and `test_process_job_marks_failed_when_compile_fails`, unaffected)

- [ ] **Step 6: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add years-of-experience cap check to skip over-scope senior roles"
```

---

### Task 4: Push, auto-deploy, and verify on EC2

**Files:** none (deploy + verification only)

**Interfaces:** none — this task validates Tasks 1-3's already-committed changes against the live server.

- [ ] **Step 1: Run the full local test suite one more time**

Run: `go test ./... && gofmt -l . && cd scripts && python3 -m pytest test_tailor_resume.py -v`
Expected: everything PASS, `gofmt -l .` empty

- [ ] **Step 2: Push to master**

```bash
git push origin master
```

This triggers `.github/workflows/deploy.yml`: builds the frontend, rsyncs `internal cmd scripts config.yaml go.mod go.sum` to the EC2 box, rebuilds the Go binary as the `jobwatch` user, and restarts the `jobwatch` service.

- [ ] **Step 3: Watch the GitHub Actions run to completion**

Run: `gh run watch --exit-status $(gh run list --workflow=deploy.yml --limit 1 --json databaseId --jq '.[0].databaseId')`
Expected: run concludes with success.

- [ ] **Step 4: Verify config.yaml and filter.go actually landed on the box**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "grep exclude_keywords /opt/jobwatch/config.yaml"
```
Expected output includes `"lead"` in the list.

```bash
diff <(md5sum internal/poller/filter.go | cut -d' ' -f1) <(ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo md5sum /opt/jobwatch/internal/poller/filter.go" | cut -d' ' -f1)
```
Expected: no diff output (hashes match).

- [ ] **Step 5: Verify the service is healthy and the new binary is running**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo systemctl status jobwatch --no-pager | head -5"
```
Expected: `active (running)`.

- [ ] **Step 6: Verify the new filter logic behaves correctly on the live box**

```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo -u jobwatch bash -lc '/opt/jobwatch/scripts/poll-wrapper.sh'"
```
Expected: poll runs cleanly (this also force-rebuilds the Go binary from the freshly-synced source, per `poll-wrapper.sh`'s existing behavior, so this step doubles as a rebuild-from-source confirmation).

- [ ] **Step 7: Spot-check the dashboard**

Open `https://jobwatch.mayurathavale.com` (Basic Auth) and confirm the job list still renders and no jobs with "Lead" or "International"-style titles behave unexpectedly (a "Lead ..." title should no longer appear as new; an "International ..." title should still appear if otherwise eligible).
