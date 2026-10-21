# Shared candidate profile

Jobwatch can validate, store, and export one candidate profile per database.
Resume generation, preference judgments, email generation/revision, founder
outreach, and local follow-up drafts read this profile. Importing a profile
does not itself generate a resume or contact anyone. Discovery sources and
filters still come from YAML. The dashboard's Profile page edits the same stored
profile; MCP tools are future work.

## Dashboard editor

Open **Profile** in the dashboard to edit identity, links, experience, education,
skills, projects, evidence, preferences and outreach defaults. Changes stay in
the form until **Save profile** passes the same Go validation as CLI import.
An empty database starts an incomplete profile; add only what you know.

New evidence and projects start as **needs review**. Link supporting evidence
with checkboxes; unlink a fact from skills, roles and projects before removing
it. Optional choices retain unknown separately from no, and blank amounts stay
unknown. Compensation uses whole currency units, not lakhs or thousands.

Each save checks the revision. If another browser or the CLI saved first, your
edits remain in the form. Download them before discarding edits and loading the
latest profile. Leaving with unsaved changes triggers a browser warning.

The editor preserves source/filter, automation and resume-template settings
without exposing controls for them. Polling still uses YAML. Saving a profile
does not regenerate existing resumes, create drafts, enqueue work or send mail.

## Try it

Run these from the repository root with the same `config.yaml` you use for
Jobwatch. `-config` can select another existing config; its `db_path` selects
the database and follows the existing CLI's working-directory convention.
`-db /absolute/path/jobs.db` selects a database directly without loading YAML.

```sh
cp examples/profile.example.json profile.local.json
# Replace the fictional candidate and preferences with your own data.
./bin/jobwatch profile import -file profile.local.json
./bin/jobwatch profile import -file profile.local.json -apply
./bin/jobwatch profile export > profile.local.json
```

The first import only validates the file. It does not open or create a
database, check whether the revision is current, or print candidate details.
`-apply` validates again and saves. Export writes JSON to stdout; errors go
to stderr. An absent profile is an error, never a fallback to the author's
candidate data. A mistyped export database path does not create a database.

`profile.local.json` is gitignored at the repository root. Exports contain
personal data; choose where to store or share them accordingly. The checked-in
example is fictional, including its compensation and source slug.

## Format and edits

The import and export formats are identical:

```json
{
  "revision": 0,
  "profile": {
    "schemaVersion": 1,
    "identity": {"name": "Example Candidate"}
  }
}
```

A new profile uses revision `0`. A successful save returns revision `1` and
a locally generated UTC timestamp. To edit, export the latest snapshot,
change fields inside `profile`, and import with `-apply`. Keep the exported
revision: the save fails if another process has changed it. There is no
force-overwrite option. Saving an unchanged document also advances its revision.

Exports include `updatedAt`. Import accepts that metadata but always assigns
a new local timestamp. To copy a profile into a different, empty database,
set its revision to `0` first. This still cannot overwrite an existing profile.

## Model

All sections except `schemaVersion` can start empty. Missing optional numbers
and booleans remain unknown; an explicit `false` or `0` is retained. Validation
does not add a name, salary, experience ceiling, or automation defaults.

| Section | Data |
|---|---|
| `identity` | Name, email, phone, location, labelled HTTP/HTTPS links |
| `background` | Skills, employment, education, projects, and evidence statements |
| `search` | Sources, title/location filters, preferences, optional required-experience ceiling |
| `resume` | Template identifier and optional page limit; version 1 supports one page |
| `outreach` | Signature, writing instructions, automatic-draft choice, company-size limit |
| `automation` | Cycle/daily limits, fit threshold, manual bypass choice, unknown-fit policy |

See [the complete example](../examples/profile.example.json) for field names.
Existing source/filter types and provider validation are shared with YAML
configuration rather than maintained as a second provider list.

Background entries have stable, globally unique IDs. Skills, employment,
and projects refer to `background.evidence` through `evidenceIds`. Each evidence
entry has text, a source note, and status `confirmed`, `needs_review`, or
`planned`. Confirmed means candidate-confirmed, not independently verified.
Skill levels are `used`, `familiar`, or `adjacent`; they are not inferred from
a job description. Consumers must decide which statements are eligible for an
application. Shape validation cannot establish that a claim is true.

Employment and education dates use `YYYY-MM`; omit `endDate` for an ongoing
entry. Compensation amounts use whole currency units, with an uppercase
three-letter currency code, `annual`/`monthly`/`hourly` period, and `fixed`/`total`
basis. Supply a minimum, target, or both. Work authorization uses uppercase
two-letter country codes; unspecified authorization or sponsorship stays unknown.
Country/currency code syntax is checked, not legal eligibility or exchange rates.

Imports reject unsupported schema versions, unknown fields, duplicate JSON
keys (including case variants), extra JSON documents, broken evidence references,
invalid source settings, invalid dates, and out-of-range settings. The encoded
snapshot must fit within 1 MiB. A partial profile can be stored, but that does
not imply it is ready for resume generation or any other consumer.

## Code boundaries

- `internal/profile` owns the types, strict decoder, and semantic validation.
- `internal/store.GetProfile` and `SaveProfile` own the singleton SQLite row
  and atomic revision checks. Direct saves also validate the profile.
- `cmd/jobwatch/profile.go` handles flags, files, and JSON output.
- `internal/web/profile.go` serves `GET /api/profile` and revision-checked
  `PUT /api/profile` for the dashboard using the same decoder and store methods.

Future MCP handlers can call the same model and store methods.
There is no separate profile server, new database, or interface framework.
Machine settings and credentials stay in the existing configuration/environment;
the profile has no credential fields. No existing Markdown, LaTeX, or Python
candidate catalogs are imported automatically.

## Resume and email consumers

Go remains the validation boundary. Python invokes `jobwatch profile export -db`
for the selected database and binds that snapshot for one action, including
nested resume, judgment, and outreach calls. The dashboard, worker, and Telegram
handlers pass their database path and running binary through `JOBWATCH_DB_PATH`
and `JOBWATCH_BINARY`. Direct Python calls default to the checkout's database
and built binary. No Python code maintains a second copy of profile validation.

Resume generation needs a name, email, and skills or confirmed background
evidence. Only the `default` template is supported. Partial profiles remain
valid storage documents; an incomplete resume profile stops before JD fetching,
compilation, or delivery. Email generation needs a candidate name. Missing or
invalid stored profiles are errors, with no fallback to historical candidate files.

The current consumers use:

- `identity` and `background` for resume content and email context. Evidence
  must be `confirmed`; projects must also be `confirmed`. Skills linked to
  unconfirmed evidence are excluded. `used` skills can be stated directly;
  `familiar` and `adjacent` skills use a familiarity line when relevant.
- `search.preferences` for LLM fit judgments, and the optional
  `search.maxRequiredExperienceYears` for the required-experience gate.
- `outreach.signature` and `instructions` for email prompts, and the signature
  for local follow-up drafts. An empty signature uses the candidate name.
  `autoDraft` controls automatic outreach after tailoring (unset keeps the
  existing enabled behavior). Manual outreach is still explicit. `maxEmployees`
  controls automatic company-size gating (unset keeps the existing limit of 20).

The neutral `resume/template.tex` owns layout; profile statements are plain text
and escaped before insertion. Employer, title, dates, and education are rendered
from profile fields rather than generated by the LLM. Manual `fix:`/`update:`
requests can still edit the selected resume sections.

Profile sources/filters and `automation` settings are stored and validated but
are not active configuration yet. The existing 8-per-cycle, 20-per-day,
45-point batch fit threshold, manual fit bypass, unknown-fit behavior, and
one-page/two-attempt PDF flow remain in place. The dashboard editor does not
change these limits or add a scheduler or MCP endpoint.

## Existing installations

Import your real profile into the correct database **before enabling the new
consumers**. Build the binary from this change, validate the JSON, then import
with `-apply`. There is no automatic conversion of `master.tex`, `facts.md`,
`preferences.md`, or the previous Python catalogs. Keep the import/export JSON
outside commits.

Generated entries record the profile revision and content hash. An update can
reuse cached resume fields only when both still match. Unversioned or changed
profiles rebuild from current facts; old edit instructions are retained as
`previous_profile_instructions` rather than replayed onto another candidate.
Old PDFs remain on disk, but the composer will not attach them to the new
profile. Rebuild a resume before requesting founder outreach for it.

For a database named `jobwatch.db`, the tracker stays at `resume/tailored.json`
beside it; PDFs and TeX files go under
`artifacts/resumes/<profile-hash>/<company>/` there. Other database names use a
sibling `<database-filename>.state/` directory for both, so databases in the
same folder cannot share tracker entries.
Set `JOBWATCH_ARTIFACT_DIR` to override the output root. On Kubernetes, keep
these paths on the shared PVC. Batch jobs with existing tracker entries are
still skipped; importing a profile does not automatically regenerate them.
Use a manual rebuild for a particular job.
