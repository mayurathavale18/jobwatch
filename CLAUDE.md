# jobwatch — quick reference

Read `HANDOFF.md` first for full context. This file is just the commands/facts used constantly.

## Production (EC2)

- SSH: `ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110`
- Elastic IP: `16.113.24.110` (allocation `eipalloc-0d14093253e61dbb1`, instance `i-074bc79b46ee82bde`, `t3.micro`, `ap-south-2`)
- AWS CLI: `export AWS_PROFILE=portfolio` before any `aws` command. Root creds in `creds.yml` are NEVER to be used — CLI profile only.
- Dashboard: `https://jobwatch.mayurathavale.com` (nginx reverse proxy → `127.0.0.1:8787`, HTTP Basic Auth user `mayur`, real Let's Encrypt cert, auto-renews). Password isn't stored in this repo — it's in whatever password manager Mayur saved it to when it was generated.
- Repo on the box lives at `/opt/jobwatch`, owned by the `jobwatch` system user (not `ubuntu`) — `sudo` needed to read/write most of it.
- Service: `sudo systemctl status/restart jobwatch` (the dashboard `serve` process). Cron (`sudo -u jobwatch crontab -l`) runs poll/tg-sync/tailor-resume/dashboard-watchdog/daily-summary/weekly-backup.

## Deploying a change

The server is **not** a git clone (private repo, no deploy key on the box) — it's an `rsync` target. After committing+pushing locally:

```bash
IP=16.113.24.110
# Go/internal changes:
rsync -az -e "ssh -i ~/.ssh/jobwatch-key.pem" internal cmd config.yaml go.mod go.sum ubuntu@"$IP":/tmp/jobwatch_deploy/
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@"$IP" "sudo cp -a /tmp/jobwatch_deploy/. /opt/jobwatch/ && sudo chown -R jobwatch:jobwatch /opt/jobwatch && rm -rf /tmp/jobwatch_deploy"

# Single file (scripts, resume/*, config.yaml):
rsync -avz -e "ssh -i ~/.ssh/jobwatch-key.pem" <local-path> ubuntu@"$IP":/tmp/<basename>
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@"$IP" "sudo cp /tmp/<basename> /opt/jobwatch/<dest> && sudo chown jobwatch:jobwatch /opt/jobwatch/<dest> && rm /tmp/<basename>"
```

`poll-wrapper.sh` runs `go build` on every 15-min cron tick automatically, so Go source changes take effect on the next poll without a manual rebuild — but the binary won't exist yet on a *fresh* box or right after a big source change until that first run. Python/LaTeX changes (`tailor_resume.py`, `master.tex`) need no rebuild, just the file copy.

**Verify after every deploy** — don't assume rsync + a green log line means it worked:
```bash
ssh -i ~/.ssh/jobwatch-key.pem ubuntu@16.113.24.110 "sudo -u jobwatch bash -lc '/opt/jobwatch/scripts/poll-wrapper.sh'"
diff <(md5sum <local-file>) <(ssh ... "sudo md5sum /opt/jobwatch/<file>")  # confirm byte-identical
```

## Recurring gotchas (already bit us once each — don't repeat)

- **`$HOME`-relative paths break on the server.** The `jobwatch` service user's `$HOME` *is* `/opt/jobwatch` (the repo root), not its parent — `$HOME/jobwatch/...` or `Path.home() / "jobwatch" / ...` silently resolves wrong. Every path must resolve relative to the script's own location instead. Check both `.sh` **and** `.py` when auditing for this.
- **SQLite WAL mode.** `jobwatch.db`'s base file only reflects data that's been checkpointed — `cp`/`rsync` of just the `.db` file can silently drop recent rows still sitting in `.db-wal`. Run `sqlite3 jobwatch.db "PRAGMA wal_checkpoint(TRUNCATE);"` before copying the DB anywhere.
- **Single Telegram consumer.** Never run `poll`/`tg-sync` from two machines (or two processes) against the same bot at once — whichever polls first silently eats the other's updates, and worse, can double-notify if their DBs are out of sync (happened once during the initial deploy cutover).
- **tectonic needs `libgraphite2-3`** (apt) or it fails at runtime with a missing shared-library error that only surfaces when a resume actually gets compiled, not during setup. Already in `deploy/setup.sh`.
- **`tailor_resume.py`'s `main()` never retries a job once it has *any* entry in `tailored.json`, including a `"status": "failed"` one.** To force a retry: call `tailor_resume.rebuild_one(job_id)` directly (same path the Telegram "fix" reply uses) rather than waiting for the next cron cycle.
- **`OPENCODE_API_KEY` must exist in the server's `/opt/jobwatch/.env`, not just the laptop's.** Added for JD-extraction/verdict LLM calls in `tailor_resume.py` — without it, every job silently falls back to rule-based scoring (no error, just a `(rule-based, LLM unavailable)` tag in the Telegram message), so it's easy to deploy and not notice it's missing. Check with the same `source .env && echo $OPENCODE_API_KEY` pattern used to verify `JOBWATCH_TG_TOKEN` today.
- **t3.micro CPU credits**: a cold `go build` + `npm install/build` together can exhaust burst credits on a fresh instance and make SSH itself unresponsive (banner-exchange timeout, not a crash). Fix: `aws ec2 modify-instance-credit-specification --instance-credit-specifications "InstanceId=...,CpuCredits=unlimited"` temporarily, switch back to `standard` once done. A 2GB swapfile is also provisioned on the box for this.

## Testing

`go test ./...` — full suite, always run before pushing. `gofmt -l .` should be empty.
