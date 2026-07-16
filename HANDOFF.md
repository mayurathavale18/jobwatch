# jobwatch — session handoff

Last updated: 2026-07-16, end of session. Read this first in the next session — it has exact next steps, not just a summary.

## Where things stand

Hermes (a separate agent, ran via opencode on kimi/qwen models) built the resume-tailoring + cron + Telegram pipeline on top of jobwatch (the Go job-tracker I originally built). Hermes kept hitting provider rate/quota limits and left several things broken. The user handed the whole pipeline to me. Full history of what Hermes did/said is in `hermes_logs.txt` (repo root, gitignored, 2180 lines) — read it if you need to understand why a script looks the way it does.

Three follow-up features were requested, in this confirmed priority order:

1. **Cron status tab in dashboard — DONE**, committed and pushed (commit `d29a046`).
2. **Gmail-mining to expand keyword vocab + fix resume content gaps — CONFIRMED PLAN, NOT YET IMPLEMENTED.** This is the next task. Full details below.
3. **Migrate dashboard to React+Vite — NOT STARTED.** Confirmed deploy model: Vite builds static assets, Go embeds them via `embed.FS` (same pattern as today's `html/template`), single binary stays the deploy unit, no separate Node process at runtime. Do this last, after #2, since #2 doesn't touch the frontend.

## Task #2 — exact next steps (user said "yes" to all 4, do these)

Already done this session, don't redo:
- Searched Gmail for application-confirmation emails (not rejections — user's instruction: treat all ~200-300 applications from the last ~2 months as failed since no interview calls came in, no need to check for actual rejection emails).
- Query used: `(subject:"application" OR subject:"applying" OR subject:"thank you for your interest" OR subject:"received your application") newer_than:2m -in:spam -in:trash` — 201 threads total, fully paginated through.
- Cross-referenced companies against jobwatch.db (jobs table) — matched 3: job 82 (Stripe, Backend Engineer AI Security), job 90 (Stripe, Backend Engineer Payments and Risk), job 947 (Cloudflare, Platforms & Productivity — 404'd on refetch, posting closed).
- Fetched real JD text for job 82 and job 90 directly from Greenhouse's API (`https://boards-api.greenhouse.io/v1/boards/stripe/jobs/{gh_jid}`, gh_jid 7826765 and 7232592). Saved to `resume/jd_82_gmail_mined.txt` and `resume/jd_90_gmail_mined.txt` — read these, don't refetch.

**Findings** (from the 201 emails' job titles + the 2 real JD texts):
- "Full Stack" is one of the most common title patterns (Barclays, PepsiCo, Broadridge, Khatabook, Freshworks, Tredence, Rippling, Databricks, Stripe, Zeta) but **the current 7 experience bullets in `resume/master.tex` are 100% backend/AI-focused — zero frontend content**, despite `resume/facts.md`'s Frontend section having real, truthful production experience (React 18, Vite, NX, the Webpack→Vite module-federation migration: "build times down 70%, dev build+local startup down 80% with HMR").
- Golang/DevOps/Data Engineer demand is strong and already well-covered — no gap.
- Real gaps that recur often but aren't truthfully claimable per facts.md: C++ (Coveo, DigiCert — 2 applications), and AI-security/prompt-injection-specific work (the actual Stripe JD in `jd_82_gmail_mined.txt` is literally about defending against prompt injection/jailbreaks — adjacent to but not the same as Mayur's agent work).

**Concrete changes to make** (all confirmed by user):

1. In `scripts/tailor_resume.py`'s `KEYWORD_CANDIDATES` set: add `full stack`, `fullstack`, `frontend`, `prompt engineering` (recognized + truthful — add to the `truthful_skills` list inside `score_coverage()` too), and `payments`, `risk`, `fraud`, `c++`, `ai security`, `prompt injection`, `jailbreak` (recognized-only — do NOT add to `truthful_skills`, these are real gaps, not fabrications).
2. In `build_experience_bullets()`: add one new bullet, sourced verbatim from facts.md's Webpack→Vite migration achievement (check facts.md's Frontend section for exact wording — don't paraphrase, truth-lock requires it match what's already verified). Tag it with `"focus": ["frontend", "full_stack"]`.
3. In `determine_focus()`: add a new branch detecting `"full stack"`, `"fullstack"`, `"frontend"` in the JD text/title → append `"frontend"` (or a new `"full_stack"` tag) to the focus list. Currently there is no frontend/full-stack focus category at all — every JD falls through to backend-only bullet selection regardless of how frontend-heavy the posting is.
4. Report-only, no code change: tell the user C++ and AI-security/prompt-injection are real, recurring gaps in their skill set relative to market demand, for them to decide whether to invest in.

After making these changes: rebuild a resume via `python3 scripts/tailor_resume.py --rebuild <job_id>` for a full-stack-flavored job (or a synthetic test) and visually verify the new bullet actually gets selected and the layout still fits one page — same verify-by-actually-running discipline as the rest of this session (see feedback memory: don't trust code review alone for this pipeline, three separate real bugs were only caught by actually compiling and rendering output).

## Everything else that's live right now (context, not action items)

- Real crontab is installed (`crontab -l` to check): poll every 15 min, tg-sync every 5 min (07:00–24:00 IST), tailor-resume every 30 min, dashboard-watchdog every 10 min, daily-summary 23:50 IST, weekly-backup Sunday 02:00 IST.
- `master.tex` layout: geometry fixed (was ~19pt taller than the physical page — see the comment right above `\addtolength{\textheight}{1.5in}`), and all three section-boundary `\vspace` are now `4pt plus 1fill` (evenly-distributed stretchable glue = the "space-evenly" idea) instead of the old one-sided `\vfill`.
- tg-sync's "fix" reply handling is consolidated into the Go binary (`internal/tgsync/tgsync.go`) as the single Telegram `getUpdates` consumer — do not reintroduce a second independent poller anywhere (that was the original bug: two consumers race for the same bot's updates, whichever polls first silently eats it).
- Dashboard has two pages now: `/` (jobs) and `/cron` (job health, reads `logs/*.status.json` written by `scripts/lib/status.sh`, sourced by every wrapper script).
- `.env` has real Telegram credentials (bot token + chat id) already verified working multiple times this session — safe to `source .env` and use directly.
