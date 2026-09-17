# jobwatch — quick reference

Read `HANDOFF.md` first for full context. This file is just the commands/facts used constantly.

## Production (Contabo k3s) — migrated off EC2 in Aug 2026

- SSH: `ssh -p 2222 -i ~/.ssh/id_ed25519_contabo root@169.58.234.131` — **sshd
  is on port 2222**. Port 22 is the Wish/BubbleTea terminal-portfolio pod
  (plain `ssh root@ip` "hangs" printing TUI escape codes; that's not a broken
  server).
- Everything runs in **k3s** on one box (`vmi3532744`), alongside
  `ssh-portfolio` and `vaultwarden`. Manifests live in `/root/*.yaml` on the
  box (`jobwatch-deploy.yaml`, `jobwatch-cronjobs.yaml`, `jobwatch-pvc.yaml`,
  `ingress-jobwatch.yaml`).
- Dashboard: `https://jobwatch.mayurathavale.com` (traefik ingress +
  cert-manager Let's Encrypt → `jobwatch` Deployment on 8787, Basic Auth via
  traefik middleware; password is in Mayur's password manager, not this repo).
- All code + data live on the `jobwatch-data` PVC (local-path), mounted at
  `/opt/jobwatch` in every pod. Host path: resolve with
  `kubectl get pv "$(kubectl get pvc jobwatch-data -o jsonpath={.spec.volumeName})" -o jsonpath={.spec.local.path}`.
  The host's `/opt/jobwatch` is a **stale pre-migration copy — never deploy there**.
- Containers all run the locally-built `jobwatch-runtime:v1` image
  (`imagePullPolicy: Never`, built from `/root/jobwatch-image/Dockerfile`) —
  it's just Ubuntu + Go + tectonic deps; code is not baked in, so deploys
  never rebuild the image.
- Cron: k8s CronJobs `jobwatch-poll` (*/15), `jobwatch-tailor-resume` (*/30),
  `jobwatch-daily-summary` (23:50), `jobwatch-weekly-backup` (Sun 02:00).
  Dashboard restart: `kubectl rollout restart deploy/jobwatch`.
- **Worker**: Deployment `jobwatch-worker` (`deploy/k8s/jobwatch-worker.yaml`,
  applied by CD) runs `jobwatch worker`: long-polls Telegram (replies handled in
  ~1s, 24h) and runs the SQLite `events` queue — Telegram updates plus dashboard
  actions (manual tailor, outreach, cron run-now), which the dashboard only
  enqueues. Queue state is visible on the Cron tab and via
  `sqlite3 jobwatch.db "select id,kind,status,attempts,error from events order by id desc limit 20"`.
  Logs: `kubectl logs deploy/jobwatch-worker`. Locally, dashboard buttons do
  nothing unless `jobwatch worker` is also running.
- Secrets live in `<PVC>/.env` (Telegram, OpenCode, web3career, Apollo, Gmail).

## Deploying a change

**Push to `master` auto-deploys** via `.github/workflows/deploy.yml`: builds
the frontend (vite outputs into `internal/web/dist`, which is `go:embed`ed),
rsyncs source to the box, copies it into the PVC, rebuilds the Go binary via
`kubectl exec deploy/jobwatch`, and does a rollout restart. Uses a dedicated
deploy keypair (public half in root's `authorized_keys`, private half in the
`CONTABO_SSH_KEY` GitHub secret) — separate from the personal
`id_ed25519_contabo`, so it can be rotated independently.

For manual/emergency deploys, the server is **not** a git clone — it's an
rsync target:

```bash
SSH="ssh -p 2222 -i ~/.ssh/id_ed25519_contabo root@169.58.234.131"
PVC=$($SSH 'kubectl get pv "$(kubectl get pvc jobwatch-data -o jsonpath={.spec.volumeName})" -o jsonpath={.spec.local.path}')
rsync -azR -e "ssh -p 2222 -i ~/.ssh/id_ed25519_contabo" internal cmd scripts config.yaml go.mod go.sum resume/master.tex root@169.58.234.131:"$PVC"/
$SSH 'kubectl exec deploy/jobwatch -- bash -c "cd /opt/jobwatch && go build -o bin/jobwatch ./cmd/jobwatch" && kubectl rollout restart deploy/jobwatch'
```

`poll-wrapper.sh` still runs `go build` on every 15-min tick, so Go changes
reach the cron jobs on the next poll even without a restart. Python/LaTeX
changes need only the file copy.

**Verify after every deploy** — don't assume rsync + a green Actions run means
it worked: run a poll inside the pod
(`kubectl exec deploy/jobwatch -- bash -c 'cd /opt/jobwatch && ./scripts/poll-wrapper.sh'`)
and `md5sum` the changed file inside the PVC vs local.

## Recurring gotchas (already bit us once each — don't repeat)

- **`$HOME`-relative paths break on the server.** The `jobwatch` service user's `$HOME` *is* `/opt/jobwatch` (the repo root), not its parent — `$HOME/jobwatch/...` or `Path.home() / "jobwatch" / ...` silently resolves wrong. Every path must resolve relative to the script's own location instead. Check both `.sh` **and** `.py` when auditing for this.
- **Browser-cached `index.html` hides new dashboard deploys.** `internal/web/server.go`'s `spaHandler` now sets `Cache-Control: no-cache` on `index.html` specifically (JS/CSS are content-hashed filenames, safe to cache normally) — without it, a browser that cached the SPA shell before a deploy keeps loading the old JS bundle by its old, still-valid hashed URL, so a shipped feature can look "missing" even though it deployed fine. If a UI change ever looks live-but-absent, hard-refresh before assuming the deploy failed.
- **SQLite WAL mode.** `jobwatch.db`'s base file only reflects data that's been checkpointed — `cp`/`rsync` of just the `.db` file can silently drop recent rows still sitting in `.db-wal`. Run `sqlite3 jobwatch.db "PRAGMA wal_checkpoint(TRUNCATE);"` before copying the DB anywhere.
- **Single Telegram consumer.** The prod `jobwatch-worker` pod is the bot's only `getUpdates` consumer. Never run `tg-sync` or a second `worker` against the same bot (laptop included, since the local `.env` has the prod token) — whichever polls first silently eats the other's updates. Keep the worker Deployment on `strategy: Recreate` and `replicas: 1` for the same reason. Also never `poll` from two machines: out-of-sync DBs double-notify (happened once during the EC2 cutover).
- **tectonic needs `libgraphite2-3`** (apt) or it fails at runtime with a missing shared-library error that only surfaces when a resume actually gets compiled, not during setup. Already in `deploy/setup.sh`.
- **`tailor_resume.py`'s `main()` never retries a job once it has *any* entry in `tailored.json`, including a `"status": "failed"` one.** To force a retry: call `tailor_resume.rebuild_one(job_id)` directly (same path the Telegram "fix" reply uses) rather than waiting for the next cron cycle.
- **`OPENCODE_API_KEY` must exist in the server's `/opt/jobwatch/.env`, not just the laptop's.** Added for JD-extraction/verdict LLM calls in `tailor_resume.py` — without it, every job silently falls back to rule-based scoring (no error, just a `(rule-based, LLM unavailable)` tag in the Telegram message), so it's easy to deploy and not notice it's missing. Check with the same `source .env && echo $OPENCODE_API_KEY` pattern used to verify `JOBWATCH_TG_TOKEN` today.
- **Founder-outreach feature needs `APOLLO_API_KEY`/`GMAIL_CLIENT_ID`/`GMAIL_CLIENT_SECRET`/`GMAIL_REFRESH_TOKEN` in the server's `/opt/jobwatch/.env`**, not just the laptop's — same class of gotcha as `OPENCODE_API_KEY` above. Without Apollo configured, outreach silently skips every job as `skipped_sector`/`skipped_size` misses (no crash); without Gmail configured, a qualifying job's outreach step fails silently into `outreach_status='failed'` (retried next cycle, never surfaced unless you check `jobs.outreach_status` or `logs/cron.log` directly). Run `scripts/gmail_auth_setup.py` once locally to mint `GMAIL_REFRESH_TOKEN` (see `docs/superpowers/specs/2026-07-27-founder-outreach-design.md`). **Apollo's free tier cannot return founder_name/founder_email** (every person-data endpoint 403s with `API_INACCESSIBLE`) — only `employee_count` works; founder email has to come from the dashboard's manual override field until the plan is upgraded.
- **The `$HOME`-relative-paths gotcha changed shape on k3s**: pods run as root, so `$HOME` is `/root` (ephemeral, wiped every restart) while the repo is `/opt/jobwatch`. Anything written under `$HOME` that isn't throwaway (Go build cache is fine) silently vanishes on pod restart. `OUTPUT_ROOT` in `tailor_resume.py` (`~/Documents/...`) only survives because tailor runs happen in CronJob pods whose PDFs get sent to Telegram before the pod dies — don't rely on those files persisting.

## Testing

`go test ./...` — full suite, always run before pushing. `gofmt -l .` should be empty.
