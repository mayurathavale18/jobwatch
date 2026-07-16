#!/usr/bin/env bash
# jobwatch resume fix — regenerates a tailored resume from the current
# master.tex (relevance-scored bullets/projects, current layout fixes),
# recompiles, verifies, and resends.
#
# Called by jobwatch's Go tg-sync when a user replies "fix" to a job
# notification (single Telegram consumer — see tgsync.go). Superset of the
# old resume-fix.sh + resume-fix-analyzer.py pair: that analyzer did naive
# truncation and literally re-added \vfill (the exact bug this exists to
# fix), so it's retired in favor of tailor_resume.py --rebuild, which reuses
# the same generation path as normal batch tailoring.
#
# Usage: resume-fix.sh <job_id> [telegram_message_id_to_reply_to]

set -euo pipefail

JOB_ID="${1:-}"
REPLY_TO="${2:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

if [ -n "$REPLY_TO" ]; then
  exec python3 "$SCRIPT_DIR/tailor_resume.py" --rebuild "$JOB_ID" "$REPLY_TO"
else
  exec python3 "$SCRIPT_DIR/tailor_resume.py" --rebuild "$JOB_ID"
fi
