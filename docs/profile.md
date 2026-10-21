# Shared candidate profile

Jobwatch can validate, store, and export one candidate profile per database.
This is the foundation for dashboard and agent customization. **The current
poller, resume generator, and email tools do not read it yet.** Importing a
profile does not change filters, generate a resume, send a message, or create
an outreach draft.

## Try it

Run these from the repository root with the same `config.yaml` you use for
Jobwatch. `-config` can select another existing config; its `db_path` selects
the database and follows the existing CLI's working-directory convention.

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

Future HTTP and MCP handlers can call the same model and store methods.
There is no separate profile server, new database, or interface framework.
Machine settings and credentials stay in the existing configuration/environment;
the profile has no credential fields. No existing Markdown, LaTeX, or Python
candidate catalogs are imported automatically.
