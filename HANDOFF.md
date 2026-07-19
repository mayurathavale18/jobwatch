# jobwatch — session handoff

Last updated: 2026-07-19, end of session. Read this first — it replaces the 2026-07-16 handoff entirely, which is now stale. Also read `CLAUDE.md` for the command-level quick reference (SSH, deploy commands, recurring gotchas); this file is the narrative "what happened and why" context.

## Where things stand

jobwatch is fully deployed and running 24x7 on AWS — it no longer depends on Mayur's laptop being on. This was the biggest change this session (previous handoff had AWS deploy listed as "deferred"). Full details below, but the short version: EC2 instance, Elastic IP, real domain with TLS, all six cron jobs live, verified end-to-end multiple times including mid-session bug fixes.

## Production deployment

- **EC2**: `i-074bc79b46ee82bde`, `t3.micro`, `ap-south-2` (Hyderabad), AWS profile `portfolio`. Elastic IP `16.113.24.110` (`eipalloc-0d14093253e61dbb1`).
- **Domain**: `https://jobwatch.mayurathavale.com` — A record → the Elastic IP, real Let's Encrypt cert via certbot (expires 2026-10-16, auto-renews), nginx reverse-proxies to `127.0.0.1:8787` with HTTP Basic Auth (user `mayur`) in front — the dashboard itself has no auth of its own, so this is load-bearing.
- **Repo location on the box**: `/opt/jobwatch`, owned by system user `jobwatch` (not `ubuntu`). Deployed via `rsync`, not `git clone` — the repo is private and there's no deploy key on the box. See `CLAUDE.md` for the exact rsync commands.
- **Cron** (`sudo -u jobwatch crontab -l` on the box): poll every 15 min, tg-sync every 5 min (07:00–24:00 IST), tailor-resume every 30 min, dashboard-watchdog every 10 min, daily-summary 23:50 IST, weekly-backup Sunday 02:00 IST. Times are IST — the box's timezone was explicitly set to `Asia/Kolkata` (fresh Ubuntu AMIs default to UTC).
- **The laptop's own crontab was removed** as part of the cutover (backed up first, not restored) — the server is now the single source of truth. Do not re-add laptop cron jobs; that reintroduces the dual-Telegram-consumer bug (see `CLAUDE.md`).

## What's live in the pipeline right now

**Providers** (`internal/providers/`): Greenhouse, Lever, Ashby, Workday (Stripe, Razorpay, Databricks, Elastic, Cloudflare, Wells Fargo), plus two new search aggregators added this session — **RemoteOK** (public JSON API) and **We Work Remotely** (public RSS). Both verified against live data. Naukri, Work at a Startup (YC), and Wellfound were evaluated and explicitly rejected as automatable sources — all three actively block non-browser access (reCAPTCHA / Cloudflare challenge, confirmed live via curl). Instead there's a **manual-submit flow**: send the Telegram bot a bare job URL (not a reply) and it fetches the page title, inserts it as `provider: manual`, `status: new`, and it rides the normal tailor-resume cron. See README's "Manually adding a job" section.

**Cross-provider fuzzy dedup**: since RemoteOK/WWR can surface a posting jobwatch already tracks via a company's own ATS board (different `external_id`, same real job), there's now a fuzzy check (normalized company+title+location, any provider, 30-day window) alongside the existing exact-key dedup, in `store.ExistsFuzzyTx` / wired into `poller.go`.

**Resume tailoring** (`scripts/tailor_resume.py`, template `resume/master.tex`): fixed a real, verified root cause of a pattern of fast auto-rejections (12 of ~26 rejections in 45 days were from Amazon alone; only one application in that window progressed to an actual interview). Checked how ATS parsers actually read the generated PDF (`pdftotext`, not visual rendering) and found:
- FontAwesome icon ligatures in the header scrambled the reading order entirely (name/phone/email came out interleaved and out of order in raw sequential extraction) — replaced with plain hyperlinked text, `fontawesome` package removed entirely.
- Phone `+91-7972833243` → `+91 7972833243` (space, not hyphen — hyphen was preventing ATS autofill from splitting country code from number).
- College name → `College of Engineering, Pune (COEP)` (missing comma was causing some ATS institution-matching to truncate to the generic, ambiguous "College of Engineering").
- Skills flattened: `AWS (ECS, EC2, ...)` → `AWS, ECS, EC2, ...` — nested parens were reading as one unmatched string to ATS skill-taggers that split on top-level commas only. Fixed in **three** places that all had to match: `master.tex`, and `tailor_resume.py`'s two separate duplicate hardcoded copies of the same categories (the tailoring cron overwrites the Skills section wholesale every run, so `master.tex` alone wasn't enough — this was a real, verified propagation gap, not a hypothetical one).
- New `inject_keyword_emphasis()`: truthful-but-uncovered JD keywords (checked against `TRUTHFUL_SKILLS`, same truth-lock discipline as before, now hoisted to a module-level constant) get folded into the top bullet as a bolded clause, overflow goes on an "Additional Relevant Skills" line, skipped in tight/one-page mode. Nothing fabricated — this closes real coverage gaps, doesn't invent skills.

All of the above is compiled/verified with `tectonic` + `pdftotext`/`pdfinfo`, not just read as LaTeX source — see the feedback memory on this.

**Filters** (`config.yaml`): broadened this session after a real gap surfaced — `"full-stack"` (hyphenated) added to `include_keywords` (WWR/RemoteOK titles use that form, the existing `"full stack"`/`"fullstack"` entries didn't match it), and `"worldwide"`/`"anywhere"` added to `locations_include` (WWR's location field says "Anywhere in the World", which doesn't contain the literal word "remote"). This will increase notification volume from the two aggregator sources specifically.

## Known gaps / explicitly deferred, not forgotten

1. **`tailor_resume.py` never retries a failed job automatically** — once a job ID has any entry in `tailored.json`, `main()` skips it forever, success or failure. Worked around manually once this session (`rebuild_one(job_id)` bypasses it) but the underlying gap is still there. Worth fixing properly if failures start piling up.
2. **HN "Who's Hiring" as a fourth source** — considered, deferred. Postings are freeform comment text, not structured fields; real NLP-lite parsing effort for uncertain reliability. Not started.
3. **Item C from earlier in this session — LinkedIn/referral contact-finder + outreach email drafting — not built.** Mayur chose "role-based emails only" (no scraping) as the approach after the ban-risk was flagged, but the actual feature (job relevance ranking, targeting 2-3yr-experience reqs specifically, generating short outreach email drafts) was never designed or scoped, let alone built. This is the natural next thing to pick up.
4. Cosmetic: a stray floating bullet next to "Software Development Engineer" / "College of Engineering, Pune" in the rendered resume PDF (pre-existing `itemize[leftmargin=0pt]` quirk, unrelated to any of this session's fixes, never addressed).

## Verification habits that mattered this session (keep doing these)

- After any deploy, actually run the wrapper script on the server and read its output — don't infer success from "the rsync didn't error."
- After any resume-template change, actually compile with `tectonic` and run `pdftotext`/`pdfinfo` on the output — LaTeX source review alone missed real bugs (the FontAwesome ligature corruption wasn't visible by reading the `.tex`, only by extracting the PDF's actual text).
- Before running a live poll/notify test against production Telegram, make sure the DB is actually current first (WAL-checkpoint gotcha above) — a stale-DB test poll caused a real duplicate-notification incident earlier this session.
