# jobwatch

Polls the public job-board APIs of a configurable list of companies, detects
new postings, filters them by keyword/location, stores them in SQLite,
sends a Telegram notification per new match, and serves a local dashboard
to track application status.

Supported ATS providers: **Greenhouse**, **Lever**, **Ashby**, **Workday**.

## Setup

1. Build (this compiles the React dashboard first, then embeds it into the
   Go binary — needs Go 1.25+ and Node 22+):

   ```sh
   make build
   ```

2. Copy `config.yaml` and edit it — add your companies, tune the keyword and
   location filters. See [Adding a company](#adding-a-company) below for how
   to find each provider's `slug`.

3. Create a Telegram bot and get your chat ID (see below). Copy
   `.env.example` to `.env` and fill in both values (the cron wrapper
   scripts and `deploy/jobwatch.service` both `source .env`), or just
   `export` them directly in your shell:

   ```sh
   export JOBWATCH_TG_TOKEN="123456:ABC-DEF..."
   export JOBWATCH_TG_CHAT="123456789"
   ```

4. Seed the database so you don't get flooded with notifications for every
   job that already exists:

   ```sh
   ./bin/jobwatch backfill -config config.yaml
   ```

5. From then on, run polling on a schedule (cron example below):

   ```sh
   ./bin/jobwatch poll -config config.yaml
   ```

6. View the dashboard:

   ```sh
   ./bin/jobwatch serve -config config.yaml
   # open http://127.0.0.1:8787
   ```

## Creating a Telegram bot and finding your chat ID

1. Open Telegram, message **@BotFather**, and send `/newbot`. Follow the
   prompts (choose a name and a username ending in `bot`). BotFather replies
   with a token that looks like `123456789:AAExampleTokenValue`. That's your
   `JOBWATCH_TG_TOKEN`.
2. Send any message to your new bot (search for its username and hit
   Start/send "hi").
3. Fetch updates to find your chat ID:

   ```sh
   curl "https://api.telegram.org/bot<YOUR_TOKEN>/getUpdates"
   ```

   Look for `"chat":{"id":123456789,...}` in the response — that number is
   your `JOBWATCH_TG_CHAT`. (If the response is empty, make sure you sent
   the bot a message first.)
4. Verify it's wired up:

   ```sh
   ./bin/jobwatch test-notify -config config.yaml
   ```

   This exits with a clear error if either env var is missing or the
   Telegram API call fails, and sends a real message otherwise.

## Adding a company

Each entry in `config.yaml`'s `companies` list needs a `name`, a `provider`
(`greenhouse`, `lever`, `ashby`, or `workday`), and a `slug`. The slug is
specific to each ATS and is usually visible in the company's careers page
URL:

- **Greenhouse**: careers pages are often `https://boards.greenhouse.io/{slug}`,
  or check the network tab for calls to
  `https://boards-api.greenhouse.io/v1/boards/{slug}/jobs`.
- **Lever**: careers pages are often `https://jobs.lever.co/{slug}`.
- **Ashby**: careers pages are often `https://jobs.ashbyhq.com/{slug}`.

You can sanity-check a slug before adding it by curling the API directly,
e.g. for Greenhouse:

```sh
curl -s "https://boards-api.greenhouse.io/v1/boards/stripe/jobs?content=true" | head -c 300
```

Note: Ashby's own docs describe the job board endpoint as `POST`, but the
live API only accepts `GET` (a `POST` returns `401 Unauthorized`); this is
what jobwatch actually does — verified against Ashby's own public board.

### Workday

Workday needs two extra fields, `host` and `site`, because a Workday
careers URL is `https://{slug}.{host}.myworkdayjobs.com/{site}` — three
independent parts, not one slug:

```yaml
- name: "Wells Fargo"
  provider: workday
  slug: wf
  host: wd1
  site: WellsFargoJobs
```

To find these for a company: open their careers page, watch the network
tab for a `POST .../wday/cxs/{tenant}/{site}/jobs` request, and read
`tenant`/`host`/`site` straight out of the URL. Not every company that
"looks big" is actually on Workday — e.g. Oracle's own careers site runs on
Oracle's own recruiting platform, not Workday, despite `myworkdayjobs.com`
hosting plenty of *other* companies' Oracle-titled job postings. Verify
with a real request before adding an entry:

```sh
curl -s -X POST "https://{slug}.{host}.myworkdayjobs.com/wday/cxs/{slug}/{site}/jobs" \
  -H "Content-Type: application/json" -d '{"appliedFacets":{},"limit":5,"offset":0,"searchText":""}'
```

A `422` means the site name is wrong; a `200` with a `jobPostings` array
means it's correct. Large employers can have thousands of open postings —
jobwatch fetches up to 300 per company per poll cycle (most-recent-first),
not the entire board; see the Design notes below.

## Cron example

See `deploy/crontab.example` for a ready-to-edit crontab covering polling,
Telegram sync, the dashboard health-check watchdog, and weekly DB backups.
Copy it, replace the placeholder path, and `crontab deploy/crontab.example`.

The dashboard's Cron tab also has a "Run now" button per job — useful for
triggering `tg-sync` outside its scheduled hours, or any job without
waiting for its next tick, without touching the crontab at all.

Run the dashboard itself as a systemd service (see Deployment below) or in
a long-lived shell/tmux session with `make run-serve` — it binds to
`127.0.0.1` only by default, so it's not reachable beyond the machine it
runs on unless you change `dashboard.addr` in `config.yaml`.

## Deployment (AWS, or any single Linux box)

jobwatch is one Go binary + SQLite + a crontab — there's no database
server, container orchestration, or multi-machine anything to set up. The
whole thing runs comfortably on the smallest instance size available
(e.g. a $3.50-5/mo AWS Lightsail instance, or a `t3.micro`/`t4g.micro` EC2
instance within the free tier).

1. Spin up an Ubuntu 22.04 or 24.04 instance (Lightsail or EC2).
2. Clone this repo onto it, e.g. to `/opt/jobwatch`.
3. Run the bootstrap script as root:

   ```sh
   cd /opt/jobwatch
   sudo bash deploy/setup.sh
   ```

   This installs Go, Node, tectonic, and `pdfinfo`; creates a dedicated
   `jobwatch` system user; builds the frontend + binary; installs
   `deploy/jobwatch.service` (systemd, auto-restart on crash); and installs
   `deploy/crontab.example`'s **core** jobs (poll, tg-sync,
   dashboard-watchdog, weekly-backup) for that user. It does *not* enable
   the resume-tailoring cron — that's commented out in the crontab
   template since it needs your own `resume/master.tex` + `resume/facts.md`
   (see below), not something to inherit from whoever you forked this
   from.
4. Edit `.env` (from `.env.example`) with your real Telegram bot token +
   chat id, and `config.yaml` with your companies/keywords/locations.
5. Seed the database once so you don't get flooded with notifications for
   every job that already exists:

   ```sh
   sudo -u jobwatch /opt/jobwatch/bin/jobwatch backfill -config /opt/jobwatch/config.yaml
   ```

6. Start it: `systemctl start jobwatch` — then `systemctl status jobwatch`
   and `curl -s localhost:8787 | head -c 200` to confirm it's up.

**Reaching the dashboard remotely:** it has no authentication, so don't
open its port to the internet. Either keep it to `127.0.0.1` and reach it
over an SSH tunnel (`ssh -L 8787:localhost:8787 your-instance`), or put it
behind something that does its own auth (a reverse proxy with basic auth,
a VPN, Tailscale, etc.) if you want browser access without tunneling every
time.

**Sharing this with friends:** each person runs their own instance from
their own fork/clone — their own AWS box, their own `config.yaml` (own
companies/keywords), their own Telegram bot, their own `.env`. There's no
shared/multi-tenant deployment model here; `deploy/setup.sh` and this
README are the "product" being shared, not a hosted service.

## Two-way status control via Telegram

Every job notification `poll` sends ends with a hidden tag, e.g. `#J123`.
**Reply directly to that message** in Telegram to update the job's status
or leave a note — no need to open the dashboard. `tg-sync` drains these
replies on its own schedule (see cron example above) and applies them.

Reply with (first word, case-insensitive):

| Reply keyword(s) | Status set |
|---|---|
| `applied`, `done`, `apld` | `applied` |
| `skip`, `skipped`, `ignore`, `no` | `ignored` |
| `shortlist`, `sl`, `later` | `shortlisted` |
| `rejected`, `reject`, `rej` | `rejected` |
| `interview`, `iv` | `interview` |
| `offer` | `offer` |
| `new`, `reset` | `new` |

Or reply starting with `note: ` (e.g. `note: applied via referral`) to
append a timestamped note without changing status.

If your reply doesn't match any keyword, jobwatch replies with the keyword
list above and leaves the job's status untouched. If it can't find a job
tag in the message you replied to, or the tagged job id doesn't exist, it
replies explaining why — either way, nothing in the database changes.

Only replies from the chat configured in `JOBWATCH_TG_CHAT` are processed;
updates from any other chat are logged and ignored.

## Commands

| Command | Behavior |
|---|---|
| `jobwatch poll` | Runs one polling cycle across all companies, stores new jobs, sends Telegram notifications for matches, then exits. Intended to be called by cron. |
| `jobwatch backfill` | Same as `poll`, but never sends notifications — use this once, on first run, to seed the database. |
| `jobwatch serve` | Runs the dashboard web server (long-running). |
| `jobwatch test-notify` | Sends a single test Telegram message and exits. Fails with a clear error if `JOBWATCH_TG_TOKEN` / `JOBWATCH_TG_CHAT` aren't set or the API call fails. |
| `jobwatch tg-sync` | Drains Telegram replies since the last run and applies any status/notes changes found (see above), then exits. Intended to be called by cron. |

All commands accept `-config path/to/config.yaml` (default `config.yaml`).

## Design notes

- Providers are normalized into one `Job` struct in `internal/providers`.
  Adding another ATS means implementing the `Provider` interface
  (`Fetch(ctx, company) ([]Job, error)`) in one new file.
- Dedupe key is `(provider, company_slug, external_id)`, enforced both by a
  SQLite `UNIQUE` constraint and an explicit existence check before insert.
- Polling fans out to at most 5 companies concurrently (semaphore-bounded),
  with a 60s timeout per company (sized for Workday's pagination — up to 15
  sequential requests per company — not just a single HTTP call). A failure
  on one company is logged and skipped — it never aborts the whole cycle.
- The dashboard (`internal/web`) is a Vite+React SPA built to
  `internal/web/dist/` and embedded into the Go binary via `go:embed` —
  `make build` builds the frontend first, since the embed directive needs
  that directory to exist at compile time. The Go side only exposes a JSON
  API (`GET /api/jobs`, `PATCH /api/jobs/{id}`, `GET /api/cron`,
  `POST /api/cron/{name}/run`); there's no server-rendered HTML or
  templating left. `frontend/node_modules/` and `internal/web/dist/` are
  gitignored build artifacts — regenerated by `make build`, not committed.
- New jobs that fail the configured filters are still stored (status
  `ignored`) so they're never re-evaluated or re-notified on subsequent
  polls — only jobs that pass filters get status `new` and a notification.
- SQLite runs in WAL mode; all writes go through explicit transactions.
- `tg-sync`'s Telegram offset (so replies aren't reprocessed) is persisted
  in a small `kv` table; each reply's job lookup and status/notes update
  run in one transaction, committed before the confirmation is sent.

## Development

```sh
make test        # go test ./...
make build        # builds ./bin/jobwatch
```

Test fixtures for provider parsing live in
`internal/providers/testdata/{greenhouse,lever,ashby}/` — the Greenhouse
and Ashby fixtures are trimmed real API responses; the Lever fixture is
built from Lever's documented public schema (no company with an open,
non-empty public Lever board was found while building this, dozens of
candidate slugs were probed and returned either `404` or an empty `[]`).
