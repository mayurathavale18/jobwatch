#!/usr/bin/env bash
# jobwatch tailor-one — tailors a resume for exactly one job id immediately,
# bypassing the batch cron's daily budget/rate-limit gating. Invoked by the
# dashboard's "add job link" endpoint (internal/web) right after a manual
# job is inserted, so the Telegram resume+verdict arrives in ~10-30s
# instead of waiting for the next tailor-resume cron tick.
#
# Usage: tailor-one.sh <job_id>

set -euo pipefail

JOB_ID="${1:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
LOCK_FILE="$ROOT_DIR/.tailor-resume.lock"

cd "$ROOT_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

export PATH="$ROOT_DIR:$PATH"

# Shares the batch cron's lock (tailor-resume-wrapper.sh) since both
# mutate tailored.json and the jobs table -- but waits (up to 5 minutes)
# rather than skipping outright, since an on-demand submission should
# still get processed once the batch run finishes, not silently vanish.
exec 200>"$LOCK_FILE"
if ! flock -w 300 200; then
  echo "TAILOR_ONE_FAILED: could not acquire lock within 5 minutes (batch cron still running?)" >&2
  exit 1
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" --job-id "$JOB_ID"
