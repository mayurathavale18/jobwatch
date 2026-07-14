# jobwatch

Polls the public job-board APIs of a configurable list of companies, detects
new postings, filters them by keyword/location, stores them in SQLite,
sends a Telegram notification per new match, and serves a local dashboard
to track application status.

Supported ATS providers: **Greenhouse**, **Lever**, **Ashby**.

## Setup

1. Build the binary:

   ```sh
   make build
   ```

2. Copy `config.yaml` and edit it — add your companies, tune the keyword and
   location filters. See [Adding a company](#adding-a-company) below for how
   to find each provider's `slug`.

3. Create a Telegram bot and get your chat ID (see below), then export:

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
(`greenhouse`, `lever`, or `ashby`), and a `slug`. The slug is specific to
each ATS and is usually visible in the company's careers page URL:

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

## Cron example

Run a poll every 15 minutes:

```cron
*/15 * * * * cd /path/to/jobwatch && ./bin/jobwatch poll -config config.yaml >> poll.log 2>&1
```

Run the dashboard as a systemd user service or in a long-lived shell/tmux
session with `make run-serve` — it binds to `127.0.0.1` only, so it's not
exposed beyond your machine.

## Commands

| Command | Behavior |
|---|---|
| `jobwatch poll` | Runs one polling cycle across all companies, stores new jobs, sends Telegram notifications for matches, then exits. Intended to be called by cron. |
| `jobwatch backfill` | Same as `poll`, but never sends notifications — use this once, on first run, to seed the database. |
| `jobwatch serve` | Runs the dashboard web server (long-running). |
| `jobwatch test-notify` | Sends a single test Telegram message and exits. Fails with a clear error if `JOBWATCH_TG_TOKEN` / `JOBWATCH_TG_CHAT` aren't set or the API call fails. |

All commands accept `-config path/to/config.yaml` (default `config.yaml`).

## Design notes

- Providers are normalized into one `Job` struct in `internal/providers`.
  Adding a fourth ATS (e.g. Workday) means implementing the `Provider`
  interface (`Fetch(ctx, company) ([]Job, error)`) in one new file.
- Dedupe key is `(provider, company_slug, external_id)`, enforced both by a
  SQLite `UNIQUE` constraint and an explicit existence check before insert.
- Polling fans out to at most 5 companies concurrently (semaphore-bounded),
  with a 15s timeout per HTTP request. A failure on one company is logged
  and skipped — it never aborts the whole cycle.
- New jobs that fail the configured filters are still stored (status
  `ignored`) so they're never re-evaluated or re-notified on subsequent
  polls — only jobs that pass filters get status `new` and a notification.
- SQLite runs in WAL mode; all writes go through explicit transactions.

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
