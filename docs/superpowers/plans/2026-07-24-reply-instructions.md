# Reply-Instructions (fix:/update:) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Telegram replies `fix: {instructions}` and `update: {instructions}` steer a job's tailored-resume content via free-text edit instructions, persisted per job and re-applied on every future rebuild.

**Architecture:** `tgsync.go` parses the reply into `(mode, instruction)` and appends both to the existing `FixScript` invocation's argv. `resume-fix.sh` passes them through as `--mode`/`--instruction` flags to `tailor_resume.py --rebuild`. `rebuild_one()` persists instructions into `tailored.json`, builds a `{skills_section, bullets, projects}` triple (fresh for `fix`, cached for `update`), runs it through a new single LLM edit pass (`apply_instructions`, same `call_opencode` used elsewhere), then splices and compiles exactly as today.

**Tech Stack:** Go (`internal/tgsync`), Bash (`scripts/resume-fix.sh`), Python (`scripts/tailor_resume.py`).

## Global Constraints

- Reuse `call_opencode` for the LLM edit pass — do not add a second LLM client or new API key.
- Instructions are trusted verbatim — no truth-lock/fabrication filtering on this path (per spec; truth-lock stays only on the automated JD-keyword-injection path).
- LLM failure must never silently ship an unedited resume as if the edit succeeded — the Telegram caption must flag it, and the instruction must remain queued (not dropped) for the next attempt.
- `fix`/`update` are both reply-to-notification commands, same mechanism as the existing `note:` reply — they must not work on a non-reply message.
- Existing status-keyword and `note:` reply behavior must not change.
- `go test ./...` and `gofmt -l .` must stay clean; `pytest scripts/test_tailor_resume.py -v` must stay clean.

Spec: `docs/superpowers/specs/2026-07-24-reply-instructions-design.md`

---

## File Structure

- **Modify `internal/tgsync/tgsync.go`**: new `parseFixOrUpdate(text string) (mode, instruction string, ok bool)` pure function; `runFix` gains `mode`/`instruction` params and appends them to the `FixScript` argv; `processUpdate` routes `fix`/`fix:`/`update`/`update:` through the new parser instead of the old exact-match `lower == "fix"` check.
- **Modify `internal/tgsync/tgsync_test.go`**: new unit tests for `parseFixOrUpdate`; new/updated `Run()`-level tests for `fix:`/`update:` argv passthrough and the bare-`update` rejection.
- **Modify `scripts/resume-fix.sh`**: accepts `<job_id> <reply_to> <mode> [instruction]`, forwards as `tailor_resume.py --rebuild <job_id> <reply_to> --mode <mode> [--instruction <instruction>]`.
- **Modify `scripts/tailor_resume.py`**:
  - Split `generate_resume()` into `build_resume_fields()` (rule-based triple, no splice) + `splice_resume_fields()` (pure string surgery, no generation) + `generate_resume()` kept as a thin wrapper calling both (unchanged external behavior/signature).
  - New `apply_instructions(skills_section, bullets, projects, instructions)` — one `call_opencode` JSON edit pass, falls back to the original triple + `ok=False` on any failure.
  - New `_parse_rebuild_cli_args(argv)` — pure argv parser for the `--rebuild` CLI path, replacing the inline `sys.argv` indexing.
  - `rebuild_one()` gains `mode="fix"`, `instruction=None` params: persists the instruction, builds fresh or reuses `entry["base_tex_fields"]`, runs `apply_instructions` when any instructions are pending, persists the fresh base fields only on a successful fresh build.
  - `send_telegram()` gains `instructions_pending=False` kwarg, appends a caption warning line when true.
  - `__main__`'s `--rebuild` branch uses `_parse_rebuild_cli_args` and passes `mode`/`instruction` through to `rebuild_one`.
- **Modify `scripts/test_tailor_resume.py`**: new tests for `apply_instructions` (success / LLM-down / malformed-JSON), a `build_resume_fields`+`splice_resume_fields` vs `generate_resume` equivalence test, `_parse_rebuild_cli_args` tests, and a `rebuild_one` test proving `mode="update"` reuses `base_tex_fields` without rebuilding.

---

### Task 1: `parseFixOrUpdate` — reply text parser (Go)

**Files:**
- Modify: `internal/tgsync/tgsync.go`
- Test: `internal/tgsync/tgsync_test.go`

**Interfaces:**
- Produces: `func parseFixOrUpdate(text string) (mode string, instruction string, ok bool)` — `text` is the already-`strings.TrimSpace`d reply text (original case, not lowercased). Returns `ok=false` if the text isn't a `fix`/`update` command at all. Returns `mode` normalized to lowercase `"fix"`/`"update"`; `instruction` is `""` for bare `fix`/bare `update` (bare `update` is the caller's job to reject — this function only reports the parse, not validity).

- [ ] **Step 1: Write the failing tests**

Add to `internal/tgsync/tgsync_test.go` (near `TestParseStatusKeywordAllSynonyms`):

```go
func TestParseFixOrUpdateBareCommands(t *testing.T) {
	mode, instruction, ok := parseFixOrUpdate("fix")
	if !ok || mode != "fix" || instruction != "" {
		t.Errorf("parseFixOrUpdate(fix) = (%q, %q, %v), want (fix, \"\", true)", mode, instruction, ok)
	}

	mode, instruction, ok = parseFixOrUpdate("UPDATE")
	if !ok || mode != "update" || instruction != "" {
		t.Errorf("parseFixOrUpdate(UPDATE) = (%q, %q, %v), want (update, \"\", true)", mode, instruction, ok)
	}
}

func TestParseFixOrUpdateWithInstructions(t *testing.T) {
	cases := []struct {
		text         string
		wantMode     string
		wantInstruct string
	}{
		{"fix: reword the top bullet", "fix", "reword the top bullet"},
		{"fix : reword the top bullet", "fix", "reword the top bullet"},
		{"UPDATE: Mention Postgres more", "update", "Mention Postgres more"},
		{"update:   drop the Kafka bullet  ", "update", "drop the Kafka bullet"},
		{"fix:", "fix", ""},
	}
	for _, c := range cases {
		mode, instruction, ok := parseFixOrUpdate(c.text)
		if !ok {
			t.Fatalf("parseFixOrUpdate(%q) ok=false, want true", c.text)
		}
		if mode != c.wantMode {
			t.Errorf("parseFixOrUpdate(%q) mode = %q, want %q", c.text, mode, c.wantMode)
		}
		if instruction != c.wantInstruct {
			t.Errorf("parseFixOrUpdate(%q) instruction = %q, want %q", c.text, instruction, c.wantInstruct)
		}
	}
}

func TestParseFixOrUpdateNoMatch(t *testing.T) {
	for _, text := range []string{"applied", "note: something", "fixing this myself", "updated the doc", ""} {
		if _, _, ok := parseFixOrUpdate(text); ok {
			t.Errorf("parseFixOrUpdate(%q) ok=true, want false", text)
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tgsync/... -run TestParseFixOrUpdate -v`
Expected: FAIL with `undefined: parseFixOrUpdate`

- [ ] **Step 3: Implement `parseFixOrUpdate`**

In `internal/tgsync/tgsync.go`, add `"regexp"` to the import block (alphabetical, after `"os/exec"`):

```go
	"os/exec"
	"regexp"
	"strconv"
```

Add near `ParseStatusKeyword` (after its closing brace):

```go
// fixOrUpdateRe matches a "fix:"/"update:" reply carrying free-text edit
// instructions -- optional whitespace is allowed on either side of the
// colon since Mayur's own usage includes "fix : {instructions}".
var fixOrUpdateRe = regexp.MustCompile(`(?i)^(fix|update)\s*:\s*(.*)$`)

// parseFixOrUpdate recognizes a "fix"/"fix: ..."/"update"/"update: ..."
// reply. It reports the lowercased mode, the free-text instruction
// (verbatim casing, empty for bare fix/update), and whether text matched
// this command family at all. A bare "update" (ok=true, instruction=="")
// is intentionally still reported as a match -- it's the caller's job to
// reject it, since only the caller knows how to reply back asking for
// instructions.
func parseFixOrUpdate(text string) (mode string, instruction string, ok bool) {
	lower := strings.ToLower(text)
	if lower == "fix" {
		return "fix", "", true
	}
	if lower == "update" {
		return "update", "", true
	}
	if m := fixOrUpdateRe.FindStringSubmatchIndex(text); m != nil {
		mode = strings.ToLower(text[m[2]:m[3]])
		instruction = strings.TrimSpace(text[m[4]:m[5]])
		return mode, instruction, true
	}
	return "", "", false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tgsync/... -run TestParseFixOrUpdate -v`
Expected: PASS (all 3 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/tgsync/tgsync.go internal/tgsync/tgsync_test.go
git commit -m "Add parseFixOrUpdate for fix:/update: reply parsing"
```

---

### Task 2: Wire `fix:`/`update:` into `processUpdate` + `runFix` (Go)

**Files:**
- Modify: `internal/tgsync/tgsync.go`
- Test: `internal/tgsync/tgsync_test.go`

**Interfaces:**
- Consumes: `parseFixOrUpdate` from Task 1.
- Produces: `runFix(ctx, jobID, replyToMessageID, mode, instruction)` — `FixScript` is now invoked with argv `<job_id> <reply_to> <mode> [instruction]` (4 args when instruction is non-empty, 3 otherwise). `updateNeedsInstructionMessage` constant, used by later manual QA/reference.

- [ ] **Step 1: Write the failing tests**

Update the existing `TestRunFixInvokesScriptWithJobAndReplyIDs` in `internal/tgsync/tgsync_test.go` — change the expected argv (mode is now always appended):

```go
	if strings.TrimSpace(string(gotArgs)) != "1 1100 fix" {
		t.Errorf("script args = %q, want \"1 1100 fix\" (job id, reply-to message id, mode)", strings.TrimSpace(string(gotArgs)))
	}
```

Add new tests after `TestRunFixCaseInsensitive`:

```go
func TestRunFixWithInstructionPassesItToScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("fix: reword the top bullet")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	want := "1 1100 fix reword the top bullet"
	if strings.TrimSpace(string(gotArgs)) != want {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(gotArgs)), want)
	}
}

func TestRunUpdateWithInstructionPassesItToScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	argsFile := filepath.Join(dir, "args.txt")
	script := "#!/bin/sh\necho \"$@\" > " + argsFile + "\nexit 0\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("update: drop the Kafka bullet")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fake.replies) != 1 || fake.replies[0].Text != "Applying edits, resending shortly…" {
		t.Errorf("expected update-specific ack reply, got %+v", fake.replies)
	}

	gotArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("script was not invoked: %v", err)
	}
	want := "1 1100 update drop the Kafka bullet"
	if strings.TrimSpace(string(gotArgs)) != want {
		t.Errorf("script args = %q, want %q", strings.TrimSpace(string(gotArgs)), want)
	}
}

func TestRunBareUpdateRepliesNeedsInstructionsWithoutInvokingScript(t *testing.T) {
	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fix.sh")
	script := "#!/bin/sh\nexit 1\n" // would surface as a test failure if ever invoked
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("writing fake script: %v", err)
	}

	ctx := context.Background()
	st := newTestStoreWithJob(t)
	fake := &fakeTelegram{all: fixUpdate("update")}
	syncer := &Syncer{Store: st, TG: fake, ChatID: 555, FixScript: scriptPath}

	if _, err := syncer.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(fake.replies) != 1 || fake.replies[0].Text != updateNeedsInstructionMessage {
		t.Errorf("expected needs-instructions reply, got %+v", fake.replies)
	}

	job, err := st.GetJob(ctx, 1)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != store.StatusNew {
		t.Errorf("job status = %q, want unchanged %q", job.Status, store.StatusNew)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tgsync/... -v`
Expected: FAIL — `TestRunFixInvokesScriptWithJobAndReplyIDs` now expects `"1 1100 fix"` but the old code still sends `"1 1100"`; the three new tests fail to compile/run (`runFix` signature mismatch, `updateNeedsInstructionMessage` undefined).

- [ ] **Step 3: Implement**

In `internal/tgsync/tgsync.go`, replace the `text`/`lower` command-dispatch block inside `processUpdate` (currently the `if lower == "fix" { ... }` / `if strings.HasPrefix(lower, "note:") { ... }` section):

```go
	text := strings.TrimSpace(msg.Text)
	lower := strings.ToLower(text)

	if mode, instruction, ok := parseFixOrUpdate(text); ok {
		if mode == "update" && instruction == "" {
			return false, s.TG.Reply(ctx, msg.MessageID, updateNeedsInstructionMessage)
		}
		if err := tx.Rollback(); err != nil && err != sql.ErrTxDone {
			return false, err
		}
		return false, s.runFix(ctx, jobID, msg.MessageID, mode, instruction)
	}

	if strings.HasPrefix(lower, "note:") {
```

Add the new constant next to `helpMessage`:

```go
const updateNeedsInstructionMessage = `update: needs instructions after the colon, e.g. "update: reword the second bullet".`
```

Replace `runFix` in full:

```go
// runFix invokes FixScript to apply a fix/update instruction and
// regenerate + resend a job's tailored resume PDF. The script itself
// sends the corrected PDF as its own Telegram message (threaded as a
// reply to replyToMessageID), so runFix only needs to ack immediately
// (regeneration + LaTeX compile takes a few seconds) and report failure
// if the script errors.
//
// mode is "fix" (full rule-based regen, today's original behavior) or
// "update" (content-only edit reusing the job's cached base content --
// see tailor_resume.py's rebuild_one()). instruction is the free-text
// edit request from a "fix: ..."/"update: ..." reply, or "" for a bare
// "fix" -- omitted from FixScript's argv entirely when empty, so bare
// "fix" keeps invoking FixScript exactly as it always has.
func (s *Syncer) runFix(ctx context.Context, jobID int64, replyToMessageID int64, mode string, instruction string) error {
	if s.FixScript == "" {
		return s.TG.Reply(ctx, replyToMessageID, "Resume-fix isn't configured on this install.")
	}

	ack := "Fixing layout, resending shortly…"
	if mode == "update" {
		ack = "Applying edits, resending shortly…"
	}
	if err := s.TG.Reply(ctx, replyToMessageID, ack); err != nil {
		return err
	}

	fixCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()

	args := []string{
		strconv.FormatInt(jobID, 10),
		strconv.FormatInt(replyToMessageID, 10),
		mode,
	}
	if instruction != "" {
		args = append(args, instruction)
	}
	cmd := exec.CommandContext(fixCtx, s.FixScript, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("tg-sync: fix script failed", "job_id", jobID, "mode", mode, "error", err, "output", string(output))
		return s.TG.Reply(ctx, replyToMessageID, fmt.Sprintf("Fix failed for #J%d: %s", jobID, lastLine(string(output))))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tgsync/... -v`
Expected: PASS (all tests, including the pre-existing suite)

- [ ] **Step 5: Run full Go suite + formatting check**

Run: `go test ./... && gofmt -l .`
Expected: all packages pass; `gofmt -l .` prints nothing

- [ ] **Step 6: Commit**

```bash
git add internal/tgsync/tgsync.go internal/tgsync/tgsync_test.go
git commit -m "Route fix:/update: replies through runFix with mode+instruction"
```

---

### Task 3: Refactor `generate_resume` into `build_resume_fields` + `splice_resume_fields` (Python)

**Files:**
- Modify: `scripts/tailor_resume.py:843-922` (the current `generate_resume` function)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Produces: `build_resume_fields(job, jd_data, tight=False) -> (skills_section, bullets, projects, focus, jd_keywords)`; `splice_resume_fields(master, skills_section, bullets, projects) -> str`; `generate_resume(job, jd_data, tight=False) -> (master, focus, jd_keywords)` (unchanged signature/return, now a thin wrapper — every existing caller, `process_job` and the CLI `--job-id` path, keeps working unmodified).

This is a pure extraction (no behavior change) — verified by an equivalence test rather than new business-logic tests.

- [ ] **Step 1: Write the failing test**

Add to `scripts/test_tailor_resume.py`:

```python
def test_build_and_splice_equals_generate_resume():
    job = {"id": 1, "title": "Backend Engineer", "company_name": "Stripe", "url": "https://stripe.com/jobs/1"}
    jd_data = {
        "title": "Backend Engineer", "company_name": "Stripe",
        "content_text": "We need Go, Kafka, PostgreSQL, and AWS experience.",
        "content_html": "", "location": "", "absolute_url": "https://stripe.com/jobs/1",
    }

    want_master, want_focus, want_keywords = tr.generate_resume(job, jd_data, tight=False)

    skills_section, bullets, projects, focus, jd_keywords = tr.build_resume_fields(job, jd_data, tight=False)
    got_master = tr.splice_resume_fields(tr.load_master_tex(), skills_section, bullets, projects)

    assert got_master == want_master
    assert focus == want_focus
    assert jd_keywords == want_keywords
```

- [ ] **Step 2: Run test to verify it fails**

Run: `pytest scripts/test_tailor_resume.py::test_build_and_splice_equals_generate_resume -v`
Expected: FAIL with `AttributeError: module 'tailor_resume' has no attribute 'build_resume_fields'`

- [ ] **Step 3: Implement the split**

In `scripts/tailor_resume.py`, replace the entire `generate_resume` function (lines 843-922) with:

```python
def build_resume_fields(job, jd_data, tight=False):
    """Build the skills/experience/projects content for a tailored resume,
    without splicing it into master.tex yet. Split out of generate_resume()
    so rebuild_one() can cache this triple (mode="update" reuses it without
    re-running the rule-based build) and run instruction-driven LLM edits
    on it before splicing.
    """
    jd_text = jd_data.get("content_text", "")
    title = jd_data.get("title") or job["title"]

    focus = determine_focus(jd_text, title)
    jd_keywords = extract_keywords(jd_text)

    skills_section, skill_order = build_skills_section(focus, jd_keywords)
    if tight and len(skill_order) > 6:
        # Reduce to 6 categories: drop Frontend usually
        keep = [c for c in skill_order if c != "Frontend"][:6]
        # Rebuild
        categories = {
            "Languages": "Go, Python, TypeScript, JavaScript, SQL, Bash",
            "Backend": "REST APIs, Microservices, FastAPI, NestJS, Node.js, API Gateways, KrakenD, Event-driven Architecture, SQS, SNS, DynamoDB Streams, Temporal Workflows",
            "Databases": "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB",
            "Infrastructure": "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions",
            "AI/LLM": "LangGraph, LangChain, RAG Pipelines, OpenSearch, KNN, BM25, Multi-Agent Orchestration",
            "Frontend": "React, Next.js 14, TypeScript, GraphQL, tRPC",
            "Concepts": "System Design, Distributed Systems, Concurrency, Multi-Tenant Architecture, Secure Coding, SOLID, Resiliency Patterns",
        }
        jd_text_lower = ", ".join(jd_keywords).lower()
        if "java" in jd_text_lower:
            categories["Languages"] = "Go, Python, TypeScript, JavaScript, Java, SQL, Bash"
        if "spark" in jd_text_lower:
            categories["Databases"] = "PostgreSQL, MySQL, Redis, DynamoDB, OpenSearch, MongoDB, Spark, S3 Tables"
        if "observability" in jd_text_lower or "monitoring" in jd_text_lower:
            categories["Infrastructure"] = "AWS, ECS, EC2, RDS, SQS, SNS, DynamoDB, Lambda, Secrets Manager, CloudWatch, Terraform, Docker, Kubernetes, GitHub Actions, CloudWatch Metrics, Structured Logging"
        skills_lines = [f"\\techSkill{{{cat}}}{{{categories[cat]}}}" for cat in keep]
        skills_section = "\n".join(skills_lines)

    bullets = build_experience_bullets(focus, jd_keywords, tight=tight)
    projects = build_projects_section(focus, jd_keywords)

    # Close truthful coverage gaps: inject up to MAX_INJECTED_KEYWORDS
    # missing-but-truthful JD keywords into the top bullet, and put any
    # overflow on an "Additional Relevant Skills" line -- skipped in tight
    # mode, since one page takes priority over closing the last few points
    # of coverage (see inject_keyword_emphasis's docstring).
    bullets, extra_skills_line = inject_keyword_emphasis(bullets, skills_section, jd_keywords)
    if extra_skills_line and not tight:
        skills_section = skills_section + "\n" + extra_skills_line

    return skills_section, bullets, projects, focus, jd_keywords


def splice_resume_fields(master, skills_section, bullets, projects):
    """Splice a {skills_section, bullets, projects} triple into master.tex's
    text, returning the complete document. Pure string surgery -- no
    generation logic -- so rebuild_one() can call this with either a fresh
    rule-based triple or an LLM-edited one.
    """
    # Replace skills section. Lookahead anchors on \section{Experience} only
    # (not the preceding \vspace, whose glue value is a master.tex layout
    # concern and shouldn't be duplicated/hardcoded here).
    skills_pattern = r"(\\section\{Technical Skills\}\n)(.*?)(?=\n\\vspace.*?\n\\section\{Experience\})"
    # Use a replacement *function*, not a string: re.sub() interprets
    # backslash escapes (\t, \n, ...) inside a string repl argument, so a
    # literal "\techSkill{...}" in skills_section silently became a tab
    # character followed by plain-text "echSkill{...}" -- a real, reproduced
    # corruption bug (every Skills line lost its \techSkill command). A
    # replacement function receives the string as-is, with no reprocessing.
    master = re.sub(skills_pattern, lambda m: m.group(1) + skills_section + "\n", master, flags=re.S)

    # Replace the entire experience item list content. Must search for
    # \resumeItemListStart starting *after* \section{Experience}: the bare
    # substring "\resumeItemListStart" also appears earlier, inside the
    # \newcommand{\resumeItemListStart}{...} macro *definition* -- searching
    # from the top of the document matched that definition instead of its
    # usage, and spliced the bullets into the middle of the macro itself,
    # corrupting it (produced a "Paragraph ended before \new@command was
    # complete" compile error).
    bullets_tex = "\n\n".join(f"\\resumeItem{{{b}}}" for b in bullets)
    exp_section_start = master.find("\\section{Experience}")
    exp_start = master.find("\\resumeItemListStart", exp_section_start)
    exp_end = master.find("\\resumeItemListEnd", exp_start) + len("\\resumeItemListEnd")
    new_exp = "\\resumeItemListStart\n\n" + bullets_tex + "\n\n\\resumeItemListEnd"
    master = master[:exp_start] + new_exp + master[exp_end:]

    # Replace projects section
    proj_start = master.find("\\section{Projects \\& Writing}")
    proj_list_start = master.find("\\resumeItemListStart", proj_start)
    proj_list_end = master.find("\\resumeItemListEnd", proj_list_start) + len("\\resumeItemListEnd")
    new_proj = "\\section{Projects \\& Writing}\n\\resumeItemListStart\n\n" + projects + "\n\n\\resumeItemListEnd"
    master = master[:proj_start] + new_proj + master[proj_list_end:]

    return master


def generate_resume(job, jd_data, tight=False):
    """Generate tailored LaTeX resume as string."""
    master = load_master_tex()
    skills_section, bullets, projects, focus, jd_keywords = build_resume_fields(job, jd_data, tight=tight)
    master = splice_resume_fields(master, skills_section, bullets, projects)
    return master, focus, jd_keywords
```

- [ ] **Step 4: Run test to verify it passes**

Run: `pytest scripts/test_tailor_resume.py::test_build_and_splice_equals_generate_resume -v`
Expected: PASS

- [ ] **Step 5: Run full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all existing tests still PASS (proves the refactor didn't change behavior)

- [ ] **Step 6: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Split generate_resume into build_resume_fields + splice_resume_fields"
```

---

### Task 4: `apply_instructions` LLM edit pass (Python)

**Files:**
- Modify: `scripts/tailor_resume.py`
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `call_opencode(system_prompt, user_content, model=..., timeout=...)` (existing, `scripts/tailor_resume.py:193`).
- Produces: `apply_instructions(skills_section, bullets, projects, instructions) -> ((skills_section, bullets, projects), ok)` — `instructions` is a list of strings; return value's first element is always a valid triple (edited on success, the original unchanged on failure); `ok` is `False` on any `call_opencode` failure or malformed JSON response.

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py` (near the `llm_judge` tests):

```python
def test_apply_instructions_returns_edited_triple_on_success(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, **kwargs: json.dumps({
            "skills_section": "\\techSkill{Languages}{Go, Python}",
            "bullets": ["Built a thing.", "Shipped another thing."],
            "projects": "\\resumeItem{A project.}",
        }),
    )
    (skills, bullets, projects), ok = tr.apply_instructions(
        "\\techSkill{Languages}{Go}", ["Built a thing."], "\\resumeItem{A project.}",
        ["mention Python too"],
    )
    assert ok is True
    assert skills == "\\techSkill{Languages}{Go, Python}"
    assert bullets == ["Built a thing.", "Shipped another thing."]
    assert projects == "\\resumeItem{A project.}"


def test_apply_instructions_falls_back_on_llm_unavailable(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda system_prompt, user_content, **kwargs: None)
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")


def test_apply_instructions_falls_back_on_malformed_json(monkeypatch):
    monkeypatch.setattr(tr, "call_opencode", lambda system_prompt, user_content, **kwargs: "not json")
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")


def test_apply_instructions_falls_back_on_missing_keys(monkeypatch):
    monkeypatch.setattr(
        tr, "call_opencode",
        lambda system_prompt, user_content, **kwargs: json.dumps({"skills_section": "x"}),
    )
    (skills, bullets, projects), ok = tr.apply_instructions(
        "orig skills", ["orig bullet"], "orig projects", ["some instruction"],
    )
    assert ok is False
    assert (skills, bullets, projects) == ("orig skills", ["orig bullet"], "orig projects")
```

(`json` is already imported at the top of `test_tailor_resume.py`'s target module `tailor_resume.py`; add `import json` to the top of `test_tailor_resume.py` itself if not already present.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `pytest scripts/test_tailor_resume.py -k apply_instructions -v`
Expected: FAIL with `AttributeError: module 'tailor_resume' has no attribute 'apply_instructions'`

- [ ] **Step 3: Implement `apply_instructions`**

Add to `scripts/tailor_resume.py`, directly after `splice_resume_fields`:

```python
def apply_instructions(skills_section, bullets, projects, instructions):
    """Apply the accumulated fix:/update: reply instructions to an assembled
    resume triple via one LLM pass. Instructions are trusted verbatim --
    unlike the automated JD-keyword-injection path, there's no truth-lock
    filtering here, since these are the user's own explicit edit requests
    to their own resume. Falls back to the original, unedited triple (and
    reports ok=False) on any LLM failure so a bad/unavailable LLM call
    never silently ships an unedited resume as if the edit succeeded --
    callers must surface that to the user instead of hiding it.
    """
    system_prompt = (
        "You edit a LaTeX resume's Technical Skills, Experience bullets, and "
        "Projects sections per a list of user instructions, applied in order "
        "(a later instruction may supersede an earlier one -- resolve exactly "
        "as a human editor reading the same instructions in order would). "
        "Apply every instruction verbatim and trust the user -- do not refuse "
        "or soften a request based on truthfulness. Preserve LaTeX macro "
        "structure: skills_section must stay a newline-separated list of "
        "\\techSkill{Category}{comma, separated, items} lines; bullets must "
        "stay plain text (no LaTeX commands, no backslashes) since the caller "
        "wraps each one in \\resumeItem{...}; projects must stay plain "
        "\\resumeItem{...}-ready text. "
        "Respond with strict JSON only, no markdown fences, no commentary: "
        '{"skills_section": "...", "bullets": ["...", ...], "projects": "..."}'
    )
    user_content = json.dumps({
        "skills_section": skills_section,
        "bullets": bullets,
        "projects": projects,
        "instructions": instructions,
    })

    raw = call_opencode(system_prompt, user_content, timeout=30)
    if raw is None:
        log("WARN: apply_instructions: call_opencode returned no content, keeping unedited resume")
        return (skills_section, bullets, projects), False

    try:
        data = json.loads(raw)
        new_skills = data["skills_section"]
        new_bullets = data["bullets"]
        new_projects = data["projects"]
        if not isinstance(new_skills, str) or not isinstance(new_projects, str):
            raise ValueError("skills_section/projects must be strings")
        if not isinstance(new_bullets, list) or not new_bullets or not all(isinstance(b, str) for b in new_bullets):
            raise ValueError("bullets must be a non-empty list of strings")
    except (json.JSONDecodeError, KeyError, TypeError, ValueError) as e:
        log(f"WARN: apply_instructions: malformed LLM response ({e}), keeping unedited resume")
        return (skills_section, bullets, projects), False

    return (new_skills, new_bullets, new_projects), True
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pytest scripts/test_tailor_resume.py -k apply_instructions -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Add apply_instructions LLM edit pass for fix:/update: instructions"
```

---

### Task 5: Wire instructions through `rebuild_one` + `send_telegram` (Python)

**Files:**
- Modify: `scripts/tailor_resume.py:955-1001` (`send_telegram`), `:1063-1167` (`rebuild_one`)
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `build_resume_fields`, `splice_resume_fields`, `apply_instructions` (Tasks 3-4), `load_tailored`/`save_tailored` (existing).
- Produces: `rebuild_one(job_id, reply_to_message_id=None, mode="fix", instruction=None) -> (ok, error)` (extended signature, old 2-arg call sites keep working via defaults); `send_telegram(..., instructions_pending=False)` (extended, old call sites keep working via default).

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py` (add `import sqlite3` to the file's
imports if not already present). `rebuild_one` opens `DB_PATH` with
`mode=ro`, which raises `sqlite3.OperationalError` if the file doesn't
exist at all (rather than falling back gracefully) — so tests need a real,
minimal sqlite file, not just a nonexistent path:

```python
def _make_empty_jobs_db(path):
    """A real sqlite file with an empty jobs table -- rebuild_one() opens
    DB_PATH with mode=ro, which raises OperationalError on a genuinely
    missing file rather than falling back, so tests need a real file here."""
    conn = sqlite3.connect(path)
    conn.execute("CREATE TABLE jobs (id INTEGER PRIMARY KEY)")
    conn.commit()
    conn.close()


def test_rebuild_one_update_mode_reuses_cached_base_fields(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    cached_fields = {
        "skills_section": "\\techSkill{Languages}{Go}",
        "bullets": ["Cached bullet."],
        "projects": "\\resumeItem{Cached project.}",
    }
    tr.save_tailored({
        "1": {
            "company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1",
            "status": "done", "base_tex_fields": cached_fields, "instructions": [],
        }
    })

    build_calls = []
    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: build_calls.append(1) or (
            "fresh skills", ["fresh bullet"], "fresh projects", "focus", ["kw"]
        ),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: f"MASTER::{s}::{b}::{p}")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: (True, None))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="update", instruction=None)

    assert ok is True
    assert err is None
    assert build_calls == []  # cached base fields were reused, not rebuilt

    tailored = tr.load_tailored()
    assert "MASTER::\\techSkill{Languages}{Go}::['Cached bullet.']::\\resumeItem{Cached project.}" == open(
        tailored["1"]["tex_path"], encoding="utf-8"
    ).read()


def test_rebuild_one_persists_and_applies_instruction(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: ("base skills", ["base bullet"], "base projects", "focus", ["kw"]),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)

    applied_with = {}

    def fake_apply_instructions(skills, bullets, projects, instructions):
        applied_with["instructions"] = list(instructions)
        return (skills, bullets, projects), True

    monkeypatch.setattr(tr, "apply_instructions", fake_apply_instructions)

    sent_kwargs = {}

    def fake_send_telegram(*a, **kw):
        sent_kwargs.update(kw)
        return True, None

    monkeypatch.setattr(tr, "send_telegram", fake_send_telegram)

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction="drop the Kafka bullet")

    assert ok is True
    assert applied_with["instructions"] == ["drop the Kafka bullet"]
    assert sent_kwargs["instructions_pending"] is False

    tailored = tr.load_tailored()
    assert tailored["1"]["instructions"] == ["drop the Kafka bullet"]
    assert tailored["1"]["base_tex_fields"] == {
        "skills_section": "base skills", "bullets": ["base bullet"], "projects": "base projects",
    }


def test_rebuild_one_flags_instructions_pending_when_llm_unavailable(monkeypatch, tmp_path):
    tailored_path = tmp_path / "tailored.json"
    monkeypatch.setattr(tr, "TAILORED_JSON_PATH", tailored_path)
    db_path = tmp_path / "test.db"
    _make_empty_jobs_db(db_path)
    monkeypatch.setattr(tr, "DB_PATH", db_path)
    monkeypatch.setattr(tr, "OUTPUT_ROOT", tmp_path / "out")

    tr.save_tailored({
        "1": {"company": "Stripe", "title": "Backend Engineer", "url": "https://stripe.com/jobs/1", "status": "done"},
    })

    monkeypatch.setattr(
        tr, "build_resume_fields",
        lambda job, jd_data, tight=False: ("base skills", ["base bullet"], "base projects", "focus", ["kw"]),
    )
    monkeypatch.setattr(tr, "splice_resume_fields", lambda master, s, b, p: "MASTER")
    monkeypatch.setattr(tr, "compile_tex", lambda tex_path: (True, ""))
    monkeypatch.setattr(tr, "get_page_count", lambda pdf_path: 1)
    monkeypatch.setattr(tr, "score_coverage", lambda keywords, resume_text: (1.0, [], [], []))
    monkeypatch.setattr(tr, "update_job_status", lambda job_id, status: None)
    monkeypatch.setattr(tr, "apply_instructions", lambda skills, bullets, projects, instructions: ((skills, bullets, projects), False))

    sent_kwargs = {}
    monkeypatch.setattr(tr, "send_telegram", lambda *a, **kw: sent_kwargs.update(kw) or (True, None))

    ok, err = tr.rebuild_one("1", reply_to_message_id="100", mode="fix", instruction="reword the top bullet")

    assert ok is True
    assert sent_kwargs["instructions_pending"] is True
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `pytest scripts/test_tailor_resume.py -k rebuild_one -v`
Expected: FAIL — `rebuild_one()` doesn't accept `mode`/`instruction` kwargs yet, and doesn't call `apply_instructions`/pass `instructions_pending`.

- [ ] **Step 3: Implement**

In `scripts/tailor_resume.py`, update `send_telegram`'s signature (currently `:955-956`) and body:

```python
def send_telegram(pdf_path, company, title, url, score, job_id, reply_to_message_id=None,
                   judgment=None, hedged_keywords=None, jd_unavailable=False,
                   instructions_pending=False):
```

and insert this block right before the existing `if jd_unavailable:` line:

```python
    if instructions_pending:
        lines.append("\u26a0\ufe0f Edit instructions not applied \u2014 LLM unavailable, reply fix/update again to retry")

```

(keeping the existing `if jd_unavailable:` block immediately after, unchanged).

Now replace `rebuild_one` in full (currently lines 1063-1167):

```python
def rebuild_one(job_id, reply_to_message_id=None, mode="fix", instruction=None):
    """Regenerate one job's tailored resume, recompile, verify, and resend.
    This is the 'fix'/'update' Telegram reply commands' implementation.

    mode="fix" always rebuilds the {skills_section, bullets, projects}
    triple fresh from the rule-based generator (build_resume_fields),
    exactly like the original bare-"fix" behavior, and refreshes the
    cached base for future "update" calls. mode="update" reuses the last
    cached base triple (entry["base_tex_fields"]) instead of rebuilding it
    -- falling back to a fresh build if no cached base exists yet (a job
    that's never been through a "fix" cycle).

    Both modes then apply the job's full accumulated instruction list
    (tailored.json's entry["instructions"], appended to on every non-empty
    fix:/update: reply) via apply_instructions() before splicing -- always
    from the same clean base, not chained onto a previous edit's output,
    to avoid compounding LLM drift across repeated fixes.
    """
    tailored = load_tailored()
    entry = tailored.get(job_id)
    if not entry:
        log(f"ERROR: job {job_id} not found in tracker")
        return False, f"Job {job_id} not found in tracker"

    if instruction:
        updated_instructions = list(entry.get("instructions") or [])
        updated_instructions.append(instruction)
        entry = {**entry, "instructions": updated_instructions}
        tailored[job_id] = entry
        save_tailored(tailored)

    company = entry.get("company", "Unknown")
    title = entry.get("title", "Unknown")
    url = entry.get("url", "")

    conn = sqlite3.connect(f"file:{DB_PATH}?mode=ro", uri=True)
    conn.row_factory = sqlite3.Row
    row = conn.execute("SELECT * FROM jobs WHERE id=?", (job_id,)).fetchone()
    conn.close()
    job = dict(row) if row else {"id": job_id, "company_name": company, "title": title, "url": url}

    gh_slug = company_slug(company, url)
    gh_id = extract_gh_job_id(url)
    jd_data = fetch_greenhouse_job(gh_slug, gh_id) if (gh_id and gh_slug) else None
    if not jd_data:
        jd_data = {
            "title": title, "company_name": company, "content_text": title,
            "content_html": "", "location": "", "absolute_url": url,
        }

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
    instructions_applied = True
    fresh_base_fields = None  # set only when this attempt built fields fresh (not reused from cache)

    while attempt < 2 and not success:
        attempt += 1
        tight = (attempt == 2)
        log(f"  Rebuild attempt {attempt} (tight={tight}) for job {job_id}...")

        use_cache = mode == "update" and bool(entry.get("base_tex_fields"))
        if use_cache:
            cached = entry["base_tex_fields"]
            skills_section, bullets, projects = cached["skills_section"], cached["bullets"], cached["projects"]
            jd_keywords = extract_keywords(jd_data.get("content_text", ""))
        else:
            skills_section, bullets, projects, _focus, jd_keywords = build_resume_fields(job, jd_data, tight=tight)
            fresh_base_fields = {"skills_section": skills_section, "bullets": bullets, "projects": projects}

        instructions = entry.get("instructions") or []
        if instructions:
            (skills_section, bullets, projects), instructions_applied = apply_instructions(
                skills_section, bullets, projects, instructions
            )

        master = load_master_tex()
        tex_content = splice_resume_fields(master, skills_section, bullets, projects)
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
            final_error = f"Page overflow: {pages} pages"
            continue

        resume_text = tex_content
        success = True

    if success and fresh_base_fields is not None:
        entry = {**entry, "base_tex_fields": fresh_base_fields}
        tailored[job_id] = entry
        save_tailored(tailored)

    if not success:
        log(f"  REBUILD FAILED for job {job_id}: {(final_error or '')[:200]}")
        tailored[job_id] = {**entry, "status": "failed", "error": final_error,
                             "tailored_date": datetime.now().isoformat()}
        save_tailored(tailored)
        return False, final_error

    score, covered, not_covered, not_truthful = score_coverage(jd_keywords, resume_text)
    log(f"  Coverage score: {score} ({len(covered)}/{len(jd_keywords)} keywords)")

    tailored[job_id] = {
        **entry,
        "pdf_path": str(pdf_path),
        "tex_path": str(tex_path),
        "coverage_score": score,
        "status": "done",
        "error": None,
        "tailored_date": datetime.now().isoformat(),
        "keywords": jd_keywords,
        "covered_keywords": covered,
        "not_covered_keywords": not_covered,
        "not_truthful_keywords": not_truthful,
    }
    save_tailored(tailored)

    instructions_pending = bool(entry.get("instructions")) and not instructions_applied
    tg_ok, tg_error = send_telegram(pdf_path, company, title, url, score, job_id,
                                     reply_to_message_id=reply_to_message_id,
                                     instructions_pending=instructions_pending)
    tailored[job_id]["telegram_error"] = None if tg_ok else tg_error
    save_tailored(tailored)
    if tg_ok:
        update_job_status(job_id, "shortlisted")
    return tg_ok, (None if tg_ok else tg_error)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pytest scripts/test_tailor_resume.py -k rebuild_one -v`
Expected: PASS (all 3 new tests)

- [ ] **Step 5: Run full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all tests PASS

- [ ] **Step 6: Commit**

```bash
git add scripts/tailor_resume.py scripts/test_tailor_resume.py
git commit -m "Persist and apply fix:/update: instructions in rebuild_one"
```

---

### Task 6: CLI argv parsing + `resume-fix.sh` passthrough

**Files:**
- Modify: `scripts/tailor_resume.py` (the `if __name__ == "__main__":` block, currently lines 1375-1388)
- Modify: `scripts/resume-fix.sh`
- Test: `scripts/test_tailor_resume.py`

**Interfaces:**
- Consumes: `rebuild_one(job_id, reply_to_message_id, mode, instruction)` from Task 5.
- Produces: `_parse_rebuild_cli_args(argv) -> (job_id, reply_to, mode, instruction)`, a pure function so the CLI parsing is unit-testable without subprocessing.

- [ ] **Step 1: Write the failing tests**

Add to `scripts/test_tailor_resume.py`:

```python
def test_parse_rebuild_cli_args_job_and_reply_only():
    assert tr._parse_rebuild_cli_args(["5", "1100"]) == ("5", "1100", "fix", None)


def test_parse_rebuild_cli_args_with_mode_and_instruction():
    got = tr._parse_rebuild_cli_args(["5", "1100", "--mode", "update", "--instruction", "reword bullet 2"])
    assert got == ("5", "1100", "update", "reword bullet 2")


def test_parse_rebuild_cli_args_mode_only():
    got = tr._parse_rebuild_cli_args(["5", "1100", "--mode", "fix"])
    assert got == ("5", "1100", "fix", None)


def test_parse_rebuild_cli_args_too_few_args_raises():
    with pytest.raises(ValueError):
        tr._parse_rebuild_cli_args(["5"])
```

Add `import pytest` to the top of `scripts/test_tailor_resume.py` if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `pytest scripts/test_tailor_resume.py -k parse_rebuild_cli_args -v`
Expected: FAIL with `AttributeError: module 'tailor_resume' has no attribute '_parse_rebuild_cli_args'`

- [ ] **Step 3: Implement**

Add to `scripts/tailor_resume.py`, directly above the `if __name__ == "__main__":` block:

```python
def _parse_rebuild_cli_args(argv):
    """Parse the argv following '--rebuild' into (job_id, reply_to, mode,
    instruction). argv is sys.argv[2:] -- everything after the '--rebuild'
    token itself. Raises ValueError with a usage message on too few args.
    """
    if len(argv) < 2:
        raise ValueError("--rebuild <job_id> <reply_to_message_id> [--mode fix|update] [--instruction TEXT]")
    job_id = argv[0]
    reply_to = argv[1] if argv[1] else None
    mode = "fix"
    instruction = None
    i = 2
    while i < len(argv):
        if argv[i] == "--mode" and i + 1 < len(argv):
            mode = argv[i + 1]
            i += 2
        elif argv[i] == "--instruction" and i + 1 < len(argv):
            instruction = argv[i + 1]
            i += 2
        else:
            i += 1
    return job_id, reply_to, mode, instruction
```

Replace the `--rebuild` branch inside `if __name__ == "__main__":` (currently):

```python
    if len(sys.argv) > 1 and sys.argv[1] == "--rebuild":
        if len(sys.argv) < 3:
            print("Usage: tailor_resume.py --rebuild <job_id> [reply_to_message_id]", file=sys.stderr)
            sys.exit(1)
        _job_id = sys.argv[2]
        _reply_to = sys.argv[3] if len(sys.argv) > 3 else None
        _ok, _err = rebuild_one(_job_id, reply_to_message_id=_reply_to)
        if not _ok:
            print(f"REBUILD_FAILED: {_err}", file=sys.stderr)
            sys.exit(1)
        print("REBUILD_OK")
```

with:

```python
    if len(sys.argv) > 1 and sys.argv[1] == "--rebuild":
        try:
            _job_id, _reply_to, _mode, _instruction = _parse_rebuild_cli_args(sys.argv[2:])
        except ValueError as e:
            print(f"Usage: tailor_resume.py {e}", file=sys.stderr)
            sys.exit(1)
        _ok, _err = rebuild_one(_job_id, reply_to_message_id=_reply_to, mode=_mode, instruction=_instruction)
        if not _ok:
            print(f"REBUILD_FAILED: {_err}", file=sys.stderr)
            sys.exit(1)
        print("REBUILD_OK")
```

Now update `scripts/resume-fix.sh` in full:

```bash
#!/usr/bin/env bash
# jobwatch resume fix/update — regenerates (or edits) a tailored resume,
# recompiles, verifies, and resends.
#
# Called by jobwatch's Go tg-sync when a user replies "fix"/"fix: ..." or
# "update: ..." to a job notification (single Telegram consumer — see
# tgsync.go). mode "fix" fully regenerates from the current master.tex
# (rebuild_one's original behavior); mode "update" reuses the job's cached
# base content and applies edit instructions on top of it. Both apply the
# job's full accumulated instruction history via tailor_resume.py's
# apply_instructions(), not just the newest instruction.
#
# Usage: resume-fix.sh <job_id> <telegram_message_id> <mode> [instruction]

set -euo pipefail

JOB_ID="${1:-}"
REPLY_TO="${2:-}"
MODE="${3:-fix}"
INSTRUCTION="${4:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

ARGS=(--rebuild "$JOB_ID" "$REPLY_TO" --mode "$MODE")
if [ -n "$INSTRUCTION" ]; then
  ARGS+=(--instruction "$INSTRUCTION")
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" "${ARGS[@]}"
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `pytest scripts/test_tailor_resume.py -k parse_rebuild_cli_args -v`
Expected: PASS (all 4 tests)

- [ ] **Step 5: Manual smoke test of the bash passthrough**

Run: `bash -n scripts/resume-fix.sh` (syntax check only — no `.env`/DB required)
Expected: no output, exit code 0

- [ ] **Step 6: Run full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all tests PASS

- [ ] **Step 7: Commit**

```bash
git add scripts/tailor_resume.py scripts/resume-fix.sh scripts/test_tailor_resume.py
git commit -m "Pass fix:/update: mode+instruction through resume-fix.sh CLI"
```

---

### Task 7: Full-suite verification

**Files:** none (verification only)

- [ ] **Step 1: Run the full Go suite + formatting check**

Run: `go test ./... && gofmt -l .`
Expected: all packages PASS; `gofmt -l .` prints nothing

- [ ] **Step 2: Run the full Python suite**

Run: `pytest scripts/test_tailor_resume.py -v`
Expected: all tests PASS

- [ ] **Step 3: Manual end-to-end smoke test (requires a real job + OPENCODE_API_KEY)**

Pick an existing job id from `resume/tailored.json` that has a `"status": "done"` entry. Run:

```bash
python3 scripts/tailor_resume.py --rebuild <job_id> "" --mode fix --instruction "reword the top bullet to sound punchier"
```

Expected: `REBUILD_OK` printed; `resume/tailored.json`'s entry for `<job_id>` now has `"instructions": ["reword the top bullet to sound punchier"]` and a `"base_tex_fields"` object; the regenerated `.tex`/`.pdf` reflect the reworded bullet (open the PDF and check). If `OPENCODE_API_KEY` isn't set in the local shell, expect the Telegram caption (or stdout, since no real Telegram config locally) path to reflect `instructions_pending=True` instead — confirms the fallback path works, not just the happy path.

- [ ] **Step 4: Deploy per CLAUDE.md's "Deploying a change" section**

Sync `internal/tgsync/tgsync.go` (Go — picked up by the next `poll-wrapper.sh` tick's `go build`) and `scripts/resume-fix.sh` + `scripts/tailor_resume.py` (single-file rsync, no rebuild needed) to the EC2 box, then verify with a real Telegram `fix: ...`/`update: ...` reply against a live job notification.

---

## Self-Review Notes

- **Spec coverage:** every spec section has a task — semantics (Tasks 1-2), persistence (Task 5), truth-lock trust (Task 4's system prompt), edit mechanism (Task 4), error handling/instructions_pending (Tasks 4-5), data model (Task 5), components (Tasks 1-2 Go, 3-6 Python), testing (every task's Steps 1-2/4).
- **No placeholders:** every step has literal code, not a description of code.
- **Type consistency checked:** `apply_instructions` returns `((skills_section, bullets, projects), ok)` consistently across Tasks 4-5; `rebuild_one`'s new params (`mode`, `instruction`) match `runFix`'s Go-side argv order and `_parse_rebuild_cli_args`'s output order across Tasks 2, 5, 6; `send_telegram`'s `instructions_pending` kwarg name matches between Task 5's implementation and its call sites.
