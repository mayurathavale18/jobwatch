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

### Evidence-backed shadow decisions (11 October 2026)

`scripts/job_decision.py` is a separate local path. It records exact requirement excerpts and produces `apply`, `review` or `skip`; these are candidate recommendations, never actions. They are not interchangeable with the older classifier's `prioritize/review/reject` labels. The production Go filter and tailoring experience cap remain unchanged.

The conservative English extractor preserves requirement clauses and required/preferred/unknown strength. It retains specialty wording in the evidence rather than inventing structured domain expertise. Title evidence establishes occupation; JD mentions of engineers do not turn an accounting/design role into an engineering role. The parser is a heuristic, not a complete semantic extractor. Uncertain requirements and unconfirmed country eligibility or annual cash lead to review. Human-reviewed interpretations must retain their evidence. Preferred experience and responsibilities still need deliberate assessment; the script does not establish recruiter flexibility.

```powershell
python scripts/job_decision.py cases.json candidate-constraints.json decisions.json
```

Use a frozen structured candidate file and inspect the extraction/output before relying on recommendations. Keep labels independently human-authored; diagnostic recommendations are not gold labels. This command does not read the jobs database, load credentials, send alerts or apply to jobs.

Candidate fields are `experience_years`, `country`, `facts` (frozen source text), `minimum_annual_cash` (currency-to-floor mapping) and `cash_basis`. Current constraints are 1.75 years, India, INR 2,500,000 / USD 35,000 annual targets. Their fixed/base/CTC split is unspecified, so the diagnostic candidate sets `cash_basis: null`; do not silently call these fixed-pay thresholds.

An optional case `human_review` records `skill_match`, `skill_match_confirmed`, `skill_evidence` (exact JD excerpt), `candidate_skill_evidence` (exact facts excerpt), `location_eligible`, `location_evidence`, `salary_evidence` and `annual_cash` (`minimum`, `maximum`, `currency`, `period`, `basis`). These fields are explicit human interpretations, not model-derived facts. Positive skill evidence does not establish completeness; the reviewer must inspect the full role. Location eligibility is deliberately human-confirmed: neither a remote header nor generic global-company prose grants India hiring permission automatically.

The cash validator currently supports only an unambiguous literal annual base/fixed quote, such as the **synthetic** `Annual base salary USD 40000-50000.`; numeric values, currency, period and basis must agree. Other formats, conversion needs and unmatched candidate cash basis remain review. This limits coverage deliberately: it does not parse every real compensation layout. A range crossing the floor remains review; only a confirmed comparable upper bound below the floor supports skip. Preferred experience above candidate years also remains review. `apply` requires all these checks plus confirmed skills and no unresolved requirement; it still means a recommendation, not submission.

### Narrow Laya responsibility packets

`rank_shadow.py infer-packets` compares selected responsibilities with selected candidate facts. Each packet contains `id`, `selection_version: 1`, `state_sha256`, `profile_sha256`, `candidate_evidence` and `posting_evidence`. Both evidence fields are nonempty arrays of exact excerpts from the frozen profile and JD respectively. Hashes use the existing shadow adapter's canonical JSON/source hashing. Selection is explicit; no automatic truncation or full-profile replacement occurs.

```powershell
& '../laya-test/.venv/Scripts/python.exe' scripts/rank_shadow.py infer-packets --cases cases.json --packets packets.json --profile candidate-profile.txt --checkpoint 'C:/path/to/cached/checkpoint' --out packet-predictions.json
```

Outputs are `strong_match`, `partial_match` or `insufficient_evidence`, bound to selected evidence, source/profile hashes and rubric hash. They are advisory about the selected responsibilities only. A strong match cannot establish pay, work authorization, country hiring, complete skill coverage or experience eligibility; it cannot override an evidence-backed skip. Missing excerpts are rejected before loading the model, and over-budget packets abstain without a forward pass. There is no automatic connection from these predictions to the recommendation engine or production pipeline.

The old `evaluate` command intentionally accepts only its original label schema. Packet correctness requires separately human-labelled responsibility comparisons, not translating an assistant's suggestions into supposedly independent labels. The diagnostic example packets under local `reports/` contain private candidate facts and are not intended for public commits.

Local packet smoke on 11 October 2026 used the same cached Laya 0.3.28 checkpoint, CPU and one thread. Two assistant-selected diagnostic packets contained 64 and 144 state tokens; both ran within the 316-token conservative budget. Load took 11.33 seconds, and forward passes took approximately 2.29 and 3.01 seconds. Both returned `strong_match`; these outputs are not independently labelled correctness or calibrated confidence. Exact excerpts prevent invented evidence, but selection can bias the result. Process RSS and Contabo performance remain unmeasured; parameter tensors alone occupy about 1.57 GiB.

### Independently label responsibility packets

Prepare labels before showing the model's outputs. Each label record contains `id`, `packet_sha256`, `label`, `split`, `annotator`, `provenance` and `reason`. Leave uncertain/unreviewed labels null with blank metadata. A recorded label must use `provenance: "human"`, a named annotator and a nonempty reason. Provenance is an explicit assertion, not authenticated proof of who labelled the data.

Allowed packet labels are `strong_match`, `partial_match`, and `insufficient_evidence`. Judge the selected responsibility against the selected candidate evidence only. Keep this separate from `apply/review/skip`. Assign related jobs and employers to one development/holdout split before inspecting predictions; the current diagnostic pack keeps previously viewed Coinbase/Supabase pilot employers in development (`train`), with ElevenLabs/Temporal/Grafana held out. The evaluator scores only non-null holdout labels. Split discipline is a review responsibility; this CLI does not automatically discover every related listing.

```powershell
python scripts/rank_shadow.py evaluate-packets --cases cases.json --packets packets.json --profile candidate-profile.txt --predictions packet-predictions.json --labels human-labels.json --out packet-evaluation.json
```

The evaluator verifies posting/profile/packet/rubric hashes and the advisory scope. It separates missing predictions from abstentions and reports classified-only agreement, coverage, confusion and false strong matches. Unlabelled inputs produce no accuracy value. Its output cannot overwrite any source input. The older whole-JD evaluator remains unchanged.

The local six-case pack in `reports/human-review-20261011/` has exact frozen excerpts, blank labels and no predictions shown. Three holdout cases are useful for error inspection, not a reliable accuracy estimate. Do not convert assistant recommendations into human labels or use this small purposive sample as evidence of interview/ATS outcomes.

Each of the seven steps received independent read-only review, with identified blockers fixed before progressing. Final local verification: 151 Python tests passed, the full `go test ./...` suite passed in an isolated Linux container with the real existing frontend output, and an integrated disposable database exercise passed preview/import/idempotency/report/timeline/follow-up/export assertions. Python regression tests cover eligibility, captured descriptions, provenance, local workflow and shadow evaluation. Go tests cover capture, migrations, refreshes, identity, imports and application history.

Changes have not been pushed to master or deployed. Master pushes auto-deploy. No live polling, Telegram consumption, account scraping, Gmail writes or recruiter messages were performed during implementation. The operations commands are CLI workflows; no new dashboard screens, email-alert parser or employer-board crawler were added.


### Requirement evidence ledger and compact challengers (11 October 2026)

`evidence_ledger.py` preserves reviewed, exact JD clauses and atomic capability excerpts, then retrieves up to three related facts per capability. Each fact retains its exact text, source line, headings, source hash, explicitly stated knowledge level and unresolved qualifiers. Retrieval is lexical with a small alias table: related evidence is not proof. Unlabelled facts retain `unknown` level. Missing matches can be retrieval misses, rather than missing skills.

Requirements are a JSON array of `{"id":"case-id","clauses":[{"evidence":"exact JD excerpt","capabilities":["exact substring within that excerpt"]}]}`. Omitting capabilities uses the full clause. These inputs are deliberately reviewed selections; this step does not automatically understand every responsibility in a new JD. Source changes, invented excerpts and altered retrieval results fail validation before inference.

```powershell
python scripts/evidence_ledger.py --cases cases.json --facts facts.md --requirements requirements.json --out ledger.json
python scripts/classifier_shadow.py --mode classify --ledger ledger.json --cases cases.json --facts facts.md --checkpoint 'C:/path/to/gliclass/snapshot' --out role-predictions.json
python scripts/classifier_shadow.py --mode support --ledger ledger.json --cases cases.json --facts facts.md --checkpoint 'C:/path/to/nli/snapshot' --out support-predictions.json
```

The challenger requires existing local checkpoints and outputs a new file. It uses CPU/one PyTorch thread, offline loading and complete-input token counting without truncation. GLiClass uses the inspected uni-encoder library framing, including role labels and prompt. NLI verifies the checkpoint's declared entailment/neutral/contradiction mapping. Unresolved facts, missing retrieval and oversized input abstain. Neutral means support is not established; it cannot reject a job.

The local experiment used `knowledgator/gliclass-instruct-edge-v1.0` revision `727be8a417f6a7718e591b025e07054c146d8139` and `cross-encoder/nli-deberta-v3-small` revision `fa2804872c3b4bd748f38c0185cc85775361e735`. Public checkpoint weights were downloaded explicitly during setup; inference itself is offline. Existing benchmark environment: Python 3.14.6, torch 2.14.1, transformers 5.18.0. GLiClass 0.1.20, sentencepiece 0.2.2 and psutil 7.2.2 were installed into a separate local benchmark dependency directory, leaving the Laya environment unchanged. Set child-process PYTHONPATH to that directory if reproducing that setup; these packages are not production dependencies.

GLiClass's six role-family labels and NLI's single-fact/capability judgments are different tasks from the earlier responsibility S/P/I rubric. Their scores are uncalibrated. An entailment score for a technology name cannot establish specialist implementation or complete responsibility coverage. Models can return spurious entailment and contradiction even for exact source excerpts. No output changes facts, resumes, eligibility, production filtering or application decisions.

The 16 previously inspected cases are diagnostic regression inputs, not fresh holdout data. The local ledger contains 133 fact lines and 49 capability selections, with ten retrieval misses and 93 retrieved pairs. Private run artifacts and runtime measurements remain under `reports/evidence-ledger-20261011/`; candidate facts are not public source artifacts. Production hardware capacity and model quality remain separate completion gates.

Real CPU smoke runs completed for both checkpoints. Corrected process-tree sampling on Windows measured about 505 MiB RSS for GLiClass and 1216 MiB for NLI; the latter exceeds a 1 GiB budget in this environment. These are laptop measurements, not deployment sizing guarantees. NLI produced spurious entailment for managing direct reports from unrelated integration evidence, so its output remains diagnostic. The full Python suite now passes 227 tests.


### Hosted Jev paired responsibility comparison

The TypeSafe skill and current official API documentation define the hosted experiment: https://github.com/typesafe-ai/skills/tree/main/skills/typesafe-ai and https://docs.typesafe.ai/api. `scripts/jev_shadow.py` sends the same frozen responsibility packets and unchanged `FIT_QUESTION` used by the Laya experiment. It pins `jev-1.13.0` at the official `https://api.typesafe.ai/v1/systemone` endpoint. Credentials come from `JEV_API_KEY` in the process environment or repository-local ignored `.env`, and remain in the authorization header. No key, header or provider error body is logged.

```powershell
python scripts/jev_shadow.py --cases cases.json --packets packets.json --profile candidate-profile.txt --out jev-predictions.json
```

Unlike `rank_shadow.py`'s wrapper object, this command writes a prediction array directly. Pass that array to `rank_shadow.evaluate_packets` for the existing hash-checked evaluation. Every prediction retains exact selected evidence, source/rubric hashes, resolved model version, probabilities, usage and request latency. `insufficient_evidence` remains an advisory abstention. Confidence is not calibrated against Jobwatch outcomes.

The script permits at most 16 requests, 16,000 serialized bytes per request and 200,000 total serialized request bytes per invocation. The per-request ceiling is deliberately conservative for short packets, not an exact tokenizer count. Returned model IDs must match the pinned version. Requests reject redirects, time out after 60 seconds and never retry automatically. A sibling `.journal.jsonl` records pending and validated completed requests with flush/fsync. `--resume` reuses completed requests only when sources, model, rubric and cached predictions validate. An unresolved pending request blocks resumption because it might already have been billed. A new output path starts a new experiment and can incur new charges; this is a small benchmark workflow, not a global credit limiter.

TypeSafe's model documentation lists $0.042 per million input tokens and free output tokens: https://docs.typesafe.ai/models. Cost reported from usage is an estimate at that published rate, not a verified account balance debit. The 16 previously inspected cases and their original user labels support a paired diagnostic comparison with Laya; they are not fresh holdout validation, and some label reasons consider factors outside the responsibility-only rubric. GLiClass role-family and NLI fact/claim experiments remain distinct tasks and cannot be ranked against this S/P/I comparison as equivalent accuracy tests. Hosted results do not change production filtering, facts, resumes, applications or outreach.

Authorized live comparison completed: all 16 responses returned pinned Jev 1.13.0, using 7,938 input tokens (estimated $0.00033340). Jev and Laya each coincided with 9/16 supplied labels. On the original inspected 12-case holdout group, all-label coincidence was 8/12 versus 6/12, and strong predictions against human partial labels were two versus four. These are diagnostic counts with known annotation caveats, not fresh validation or evidence that Jev is universally better. Median Jev HTTP/validation latency was 309.34 ms. All 242 Python tests passed; production remains unchanged.
