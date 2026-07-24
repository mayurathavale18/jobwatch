# Telegram reply-instructions for resume edits (fix:/update:)

Date: 2026-07-24
Status: approved, ready for implementation planning

## Problem

Every job notification can already be actioned by replying with a status
keyword (`applied`, `skip`, `interview`, ...) or `note: <text>` to attach a
freeform note (`internal/tgsync/tgsync.go`). There's no way to tell the
tailoring pipeline "drop this bullet", "reword that", "add a point about
X" — the only content-changing reply is bare `fix`, which fully
regenerates the resume from `master.tex` via the existing rule-based
generator (`generate_resume()` in `scripts/tailor_resume.py`) with no way
to steer what it produces. Corrections currently only happen by editing
`master.tex` itself (global, not per-job) or living with whatever the
templated output produced.

## Goals

- `fix: {instructions}` and `update: {instructions}` Telegram replies (both
  work the same as today's `note:` reply — must be a reply to an existing
  job notification) apply a free-text edit instruction to that job's
  tailored resume, recompile, and resend.
- Instructions persist per job and are re-applied on every future
  fix/update for that job, so a later plain `fix` (no new instructions)
  doesn't silently discard an earlier correction.
- Instructions are applied via an LLM edit pass (existing `call_opencode`,
  same OpenCode key already used for JD extraction/verdict) since the
  requests are arbitrary natural language, not a fixed set of operations.
- Instructions are trusted verbatim — no truth-lock/fabrication filtering
  on this path (that's the user editing their own resume by hand, in
  effect). Truth-lock stays enforced only on the automated JD-keyword
  injection path (`inject_keyword_emphasis`), which is unrelated to this
  feature.
- LLM failure (no key / timeout / malformed response) must never silently
  ship an unedited resume as if the edit succeeded — the Telegram caption
  must say so explicitly, and the instruction stays queued for the next
  attempt.

## Non-goals

- No new "unfix"/"undo instruction" command — if an instruction becomes
  stale, the user sends a new instruction that supersedes it (e.g. "add
  the Kafka bullet back"). The LLM sees the full ordered instruction
  history each time and resolves conflicts by recency, same as it would
  reading a conversation.
- No structured operation parsing (add_skill/remove_bullet/etc.) — this
  was considered and rejected in favor of a single LLM edit pass; see
  "Approaches considered" below.
- No change to the existing status-keyword or `note:` reply behavior.

## fix: vs update: semantics

Both take an optional trailing instruction after the colon and both
persist that instruction; the difference is what they use as their
starting point for applying it:

- **`fix`** (bare, unchanged) / **`fix: {instructions}`** — full
  regeneration: re-runs `generate_resume()`'s rule-based build
  (`determine_focus`, `build_skills_section`, `build_experience_bullets`,
  `build_projects_section`) fresh against the current `master.tex` and JD
  data, exactly as today's bare `fix` does. This refreshes the cached
  "base" content. Use this when `master.tex` changed, or the page is
  overflowing and needs the tight-mode path.
- **`update: {instructions}`** — content-only edit: skips the rule-based
  regeneration and reuses the last cached base content for that job. Use
  this for "reword X" / "drop Y" / "add a point about Z" without
  re-triggering a full relevance-scored rebuild.

Both then apply the **complete accumulated instruction list** (not just
the newest one) to the base content in a single LLM pass. Re-deriving from
the same clean base plus the full instruction list every time — rather
than chaining edit-on-previously-edited-text — avoids compounding LLM
drift across repeated fixes.

## Data model

`resume/tailored.json`, per job entry, two new fields:

```json
{
  "instructions": [
    "drop the Kafka bullet",
    "mention Postgres more prominently"
  ],
  "base_tex_fields": {
    "skills_section": "\\techSkill{Languages}{...}\n...",
    "bullets": ["...", "..."],
    "projects": "\\resumeItem{...}\n..."
  }
}
```

- `instructions`: ordered list of raw instruction strings, appended to
  (never cleared/rewritten) every time a `fix:`/`update:` reply carries
  non-empty text. Bare `fix`/invalid `update` (no instructions) appends
  nothing.
- `base_tex_fields`: the `{skills_section, bullets, projects}` triple as
  produced by the rule-based generator, refreshed only on `fix`
  (full-regen) runs. `update` reads this; if absent (job never went
  through a `fix` cycle, e.g. instructions arrive before the job's first
  tailoring pass completes), `update` falls back to running the same
  rule-based build `fix` would, then proceeds as normal.

## Components

### `internal/tgsync/tgsync.go`

New prefix parsing, checked before the existing `note:` check and status
keyword lookup, after the existing job-lookup/tx setup:

- Regex `^(fix|update)\s*:\s*(.*)$` (case-insensitive) → `mode` (`"fix"` or
  `"update"`) + trimmed instruction text.
- Bare `"fix"` (today's exact-match case) stays: `mode="fix"`, empty
  instruction.
- Bare `"update"` (no colon) → reply `"update: needs instructions after
  the colon, e.g. \"update: reword the second bullet\"."`, no job/script
  invocation.
- Both `fix`/`fix:`/`update:` cases roll the existing job tx back (same
  as today's bare-fix handling, since the actual mutation happens inside
  `rebuild_one`/tailor_resume.py, not this transaction) and call an
  extended `runFix(ctx, jobID, msg.MessageID, mode, instruction)`.

`FixScript` invocation becomes:
`FixScript <job_id> <telegram_message_id> <mode> [instruction]`
(`instruction` omitted entirely, not empty-quoted, when there is none —
keeps the common bare-`fix` case's argv identical to today's).

Ack message sent immediately (before the script runs) can stay generic
("Fixing layout, resending shortly…" / for update: "Applying edits,
resending shortly…") — cosmetic, decided at implementation time.

### `scripts/resume-fix.sh`

Signature extended: `resume-fix.sh <job_id> <reply_to> <mode> [instruction]`.
Passes through as:
`tailor_resume.py --rebuild <job_id> <reply_to> --mode <mode> [--instruction "<instruction>"]`

### `scripts/tailor_resume.py`

- `rebuild_one(job_id, reply_to_message_id=None, mode="fix", instruction=None)`:
  1. Load `tailored.json` entry.
  2. If `instruction` non-empty, append to `entry["instructions"]`
     (creating the list if absent) and save immediately — so the
     instruction is durably recorded even if the rest of the run fails.
  3. If `mode == "fix"` or `entry.get("base_tex_fields")` is absent: run
     `generate_resume()`'s rule-based build to get a fresh
     `{skills_section, bullets, projects}` triple; store it into
     `entry["base_tex_fields"]`.
  4. Else (`mode == "update"` with cached base): read the triple from
     `entry["base_tex_fields"]`.
  5. If `entry["instructions"]` is non-empty, call
     `apply_instructions(skills_section, bullets, projects,
     entry["instructions"])`. On success, use the returned triple for
     LaTeX splicing; on failure, use the **original** triple unchanged and
     set a local `instructions_applied = False` flag.
  6. Continue into the existing splice/compile/page-count/retry loop
     unchanged, using whichever triple resulted from step 5.
  7. `send_telegram(...)` gets a new optional flag; when
     `instructions_applied is False` and `entry["instructions"]` is
     non-empty, the caption appends: `"⚠️ Edit instructions not applied —
     LLM unavailable, reply fix/update again to retry"`.

- **`apply_instructions(skills_section, bullets, projects, instructions)`**
  (new function, same shape/fallback discipline as `llm_judge`):
  - Builds a system prompt: apply every instruction in the given order
    verbatim (later instructions may supersede earlier ones — resolve
    naturally as a human editor would); trust the user, no fabrication
    filtering; preserve LaTeX structure (`\techSkill{...}{...}` lines,
    plain bullet/project text — no raw special characters that would
    break `tectonic` compilation); return **strict JSON only**:
    `{"skills_section": "...", "bullets": ["...", ...], "projects": "..."}`.
  - Calls `call_opencode(system_prompt, user_content)` where
    `user_content` serializes the current triple + the instruction list.
  - Parses the response as JSON; validates all three keys present and
    `bullets` is a non-empty list of strings.
  - Returns `(triple, True)` on success; `((skills_section, bullets,
    projects), False)` (original values, unchanged) on any failure
    (`call_opencode` returned `None`, non-JSON, missing keys) — logged at
    `WARN` like `llm_judge`'s malformed-JSON case.

### `internal/notify` / `send_telegram` caption

No structural change beyond the one new conditional warning line
described above — same pattern as the existing `judgment`/`hedged_keywords`
conditional lines.

## Error handling

- LLM down/malformed at edit time → resume still generates and sends
  (unedited base), caption flags it explicitly, instruction list is
  untouched (nothing consumed) so the next `fix`/`update` retries
  automatically.
- Compile failure / page overflow → existing 2-attempt retry loop
  (`tight=True` on 2nd attempt) is unchanged; `apply_instructions` runs
  once per `rebuild_one` call (not once per attempt) — the edited triple
  is computed before the compile loop, then both attempts (normal +
  tight) splice from that same edited triple, since the instructions
  edit content, not layout density.
- Bad job tag / job not found on a `fix:`/`update:` reply → identical to
  today's `fix` handling (existing `ExtractJobID`/`GetJobTx` error paths,
  unchanged).

## Approaches considered

1. **Structured operations parsed from instruction text** (`add_skill`,
   `remove_bullet(n)`, `reword_bullet(n)`) — rejected: only handles
   instructions matching a recognized pattern; "mention Postgres more
   prominently" or "make this bullet punchier" don't reduce to a fixed
   operation set, and the whole point of this feature is natural-language
   flexibility.
2. **One LLM edit pass over the assembled triple, instructions applied
   fresh from a cached clean base each time** (chosen) — handles arbitrary
   phrasing, reuses the existing `call_opencode`/JSON-response pattern
   already proven out for `llm_judge`, and re-deriving from a clean base
   avoids drift from chaining edits onto already-edited text.
3. **Chain edits onto the previous edit's output** (incremental, no cached
   base) — rejected: repeated fixes over weeks would compound LLM
   rewrites on top of LLM rewrites with no stable reference point,
   increasing drift/hallucination risk each round.

## Testing

- **Go** (`internal/tgsync/tgsync_test.go`): new cases for `fix:`/`update:`
  regex parsing (with/without spaces around the colon), bare `fix`/bare
  `update`, case-insensitivity, and the argv passed to the fake
  `FixScript`/`telegramClient`.
- **Python** (`scripts/test_tailor_resume.py`): `apply_instructions` unit
  tests mirroring `llm_judge`'s existing pattern — success (monkeypatched
  `call_opencode` returns valid JSON), LLM-unavailable fallback (`None`),
  malformed-JSON fallback. Plus a `rebuild_one` test asserting `mode="update"`
  reuses `entry["base_tex_fields"]` without calling the rule-based
  generator, and a test asserting `mode="fix"` refreshes it.
