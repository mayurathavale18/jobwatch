# Jobwatch improvements: local operating guide

Implemented and independently reviewed on 10 October 2026. These changes are local; they have not been deployed. The initial sync check found local HEAD and origin/master at `4ce23b30f80952a19bef82f6bb0342e39c69ac6b`. All 71 tracked deployment files matched the production PVC after line-ending normalization. The running binary had no Git revision metadata, so this establishes source consistency rather than an exact binary revision.

## What changed and why

1. Eligibility: engineering decisions no longer reject an engineer because the JD mentions legal or marketing teams. Mandatory and preferred experience are distinguished, including section headings. This remains a heuristic, and the existing three-year required-experience cap remains in place.
2. Capture: Greenhouse, Lever and Ashby retain each complete original posting JSON. Tailoring, rebuilding, email drafting and `extract_jd.py` share the description parser. Manual JD overrides take precedence; captured usable text avoids another fetch; thin/missing captures use existing network fallbacks.
3. Freshness: repeat polls update title/location/URL/raw and `last_seen_at`, preserving application state, notes and manual text. `source_scans` records fetched counts and results for every configured source. Workday is explicitly bounded; aggregator coverage is unknown. Neither last-seen nor scan success proves a role remains open. Existing records have unknown freshness until observed again.
4. Identity: different source IDs and requisition IDs survive. Similar company/title/location creates an unconfirmed `job_relations` pair. Similarity never merges records or transfers application status. This can increase alerts; relation review is currently available through the local report rather than a dashboard reconciliation screen.
5. Acquisition: exported/pasted alert records can be previewed or imported without account access, fetching pages, notifications or tailoring. Recognised employer-board URLs produce suggestions for Greenhouse, Lever and Ashby. Suggestions require verification and a deliberate config edit.
6. Application workflow: status changes and successful composer sends create `application_events`. Manual reply/contact records support local follow-up drafting. Follow-ups wait seven days, require a sent contact, stop on a recorded reply or excluded status, and stop after two explicitly recorded follow-up sends. Drafts never count as sends.
7. Ranking: offline export/inference/evaluation tooling supports a local Laya checkpoint. It checks posting/profile/rubric hashes, separates holdout cases, reports abstentions and false rejections, and refuses to silently truncate over-budget input. It changes no fit score, job status or notification decision.

## Safe local setup

Do not run a second `worker`, `tg-sync` or `poll` with production credentials. These commands need none of them.

Build the CLI, using the existing frontend output:

```powershell
go build -o bin/jobwatch.exe ./cmd/jobwatch
```

Create a separate local config with a separate database, for example `config.local.yaml`:

```yaml
db_path: jobwatch-improvements.db
companies:
  - name: Example
    provider: lever
    slug: example
```

Do not use the production database as a learning sandbox. If working on an existing local database, back it up with SQLite's backup API, which includes WAL contents:

```powershell
python -c "import sqlite3; source=sqlite3.connect('jobwatch.db'); backup=sqlite3.connect('jobwatch-before-improvements.db'); source.backup(backup); backup.close(); source.close()"
```

The additive migrations run when the Go CLI opens a database. Open an older database with the new CLI before using the Python operations commands. A preview import does not open or migrate the database.

To migrate a backed-up disposable database without inserting jobs, point `config.local.yaml` to it and apply an empty import:

```powershell
python -c "from pathlib import Path; Path('empty-import.json').write_text('[]', encoding='utf-8')"
./bin/jobwatch.exe import-jobs -config config.local.yaml -file empty-import.json -apply
```

An empty applied import performs the additive schema migration and inserts no job rows. Changed JDs do not automatically re-tailor existing tracker entries; use the existing explicit rebuild path after reviewing the changed evidence. Facts are updated editorial evidence; the existing resume templates are not yet a fully connected achievement catalog.


## Import opportunities and discover employer boards

Input is a JSON array. Each record requires `url`, `company`, `title` and `source`. URL must use HTTPS without embedded credentials. `jd` and `location` are optional. A missing JD remains unknown; pasting the complete original JD is more useful than a link alone.

Example `alerts.json`:

```json
[
  {
    "url": "https://jobs.lever.co/example/posting-id",
    "company": "Example",
    "title": "Backend Engineer",
    "location": "Remote from India",
    "jd": "Paste the complete posting here, including restrictions and compensation.",
    "source": "LinkedIn saved alert exported by candidate"
  }
]
```

```powershell
# Validation and board suggestions only; no database writes.
./bin/jobwatch.exe import-jobs -config config.local.yaml -file alerts.json

# Atomic import. Existing exact manual URLs retain their existing data.
./bin/jobwatch.exe import-jobs -config config.local.yaml -file alerts.json -apply

python scripts/jobwatch_ops.py report --db jobwatch-improvements.db
```

Maximum input: 10 MiB and 2,000 records. Unknown JSON fields and trailing input are rejected. Exact URL identity retains query parameters: different tracking URLs may remain distinct and appear as possible relations. No inferred company names are used in this import path.

LinkedIn/Naukri/Instahyre acquisition starts with saved alerts, candidate exports and pasted listings. This implementation does not scrape signed-in accounts or automate applications. The existing dashboard/manual-link path remains available. Do not assume an agent-browser wrapper changes a site's access policy.

## Record contacts and produce local follow-ups

Existing dashboard/Telegram status updates now create history automatically. Successful composer sends record their actual timestamp and recipient. Old application/contact dates are not fabricated or backfilled.

```powershell
python scripts/jobwatch_ops.py timeline --db jobwatch-improvements.db --job-id 1

# Record only a contact that really happened. Supply a timezone and verified recipient.
python scripts/jobwatch_ops.py record --db jobwatch-improvements.db --job-id 1 --kind contact_sent --at '2026-10-10T10:00:00+05:30' --to 'recruiter@example.com' --detail 'Sent through my email client'

# A recorded reply stops automatic draft suggestions for this job.
python scripts/jobwatch_ops.py record --db jobwatch-improvements.db --job-id 1 --kind reply_received --detail 'Recruiter acknowledged the application'

# Preview due drafts, then write local JSON draft files if useful.
python scripts/jobwatch_ops.py followups --db jobwatch-improvements.db
python scripts/jobwatch_ops.py followups --db jobwatch-improvements.db --write-dir local-followups
```

Review recipient, role and wording before manually composing/sending. The draft uses the sent-contact recipient, never the mutable recipient of an unsent composer draft. Unknown recipients stay blank. Invalid or timezone-free legacy send timestamps cannot establish a deadline and therefore abstain from scheduling.

After a follow-up was actually sent, explicitly record it with `--kind followup_sent`, its verified `--to`, timestamp and supporting detail. This is how the two-follow-up cap is maintained. Ordinary manual records are append-only and not idempotent: do not record the same send twice. Generating a local draft is idempotent for its contact anchor. Reply ingestion is manual; no mailbox monitoring or scheduled sending has been installed.

## Evaluate ranking before relying on it

```powershell
python scripts/rank_shadow.py export --db jobwatch-improvements.db --out cases.json --limit 100
```

Exports contain personal job-search data; keep them private. Inspect the source JD, label cases `prioritize`, `review` or `reject`, and set a genuinely held-out subset's `split` to `holdout`. Leave uncertain cases unlabelled. Keep related/reposted listings in the same split to avoid leakage. These labels represent your preference, not recruiter outcomes.

Create a frozen candidate profile containing verified facts and current preferences. Include salary, location and work-authorisation constraints. Do not quietly omit constraints to fit a small context window. Full facts/preferences may exceed this checkpoint's budget; an abstention is expected in that case.

Use the already installed Laya environment and an existing cached local checkpoint. Inference forces CPU, one PyTorch thread and offline Hugging Face access; no dependency or model download is performed by this script.

```powershell
& '../laya-test/.venv/Scripts/python.exe' scripts/rank_shadow.py infer --cases cases.json --profile candidate-profile.txt --checkpoint 'C:/path/to/cached/checkpoint' --out predictions.json
python scripts/rank_shadow.py evaluate --cases cases.json --predictions predictions.json --profile candidate-profile.txt --out evaluation.json
```

The installed English checkpoint has a 512-token maximum and a 192-token head budget. The adapter conservatively reserves framing and separators, leaving at most 316 state tokens. Larger inputs receive `review` with an explicit abstention reason. This conservative check may abstain even when the actual short question would leave more room; it protects evidence integrity.

Local synthetic adapter smoke on 10 October 2026: Laya 0.3.28, cached revision `7b928d828b7b0e022f929d9bd2e44165aa270148`, CPU/one thread; load about 35.74 seconds; first classification about 4.29 seconds; parameter tensors 1,685,175,308 bytes (about 1.57 GiB). These are laptop measurements, not Contabo measurements or process RSS. The checkpoint also emitted a temperature-calibration warning for another option-count bucket. Jobwatch confidence is uncalibrated regardless of that warning; it is not an interview or ATS-pass probability.

Before production model decisions: obtain representative human-labelled jobs, compare to the current pipeline, inspect false rejections and abstentions, calibrate on a separate set, and measure warm latency plus peak RSS/CPU on the actual deployment hardware. No classifier should invent resume evidence, guaranteed salary, visa eligibility or recipient addresses.

## Validation and deployment boundary

Each of the seven steps received independent read-only review, with identified blockers fixed before progressing. Final local verification: 151 Python tests passed, the full `go test ./...` suite passed in an isolated Linux container with the real existing frontend output, and an integrated disposable database exercise passed preview/import/idempotency/report/timeline/follow-up/export assertions. Python regression tests cover eligibility, captured descriptions, provenance, local workflow and shadow evaluation. Go tests cover capture, migrations, refreshes, identity, imports and application history.

Changes have not been pushed to master or deployed. Master pushes auto-deploy. No live polling, Telegram consumption, account scraping, Gmail writes or recruiter messages were performed during implementation. The operations commands are CLI workflows; no new dashboard screens, email-alert parser or employer-board crawler were added.
