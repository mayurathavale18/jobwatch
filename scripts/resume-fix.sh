#!/usr/bin/env bash
# jobwatch resume fix/update — regenerates (or edits) a tailored resume,
# recompiles, verifies, and resends.
#
# Called by jobwatch's Go tg-sync when a user replies "fix"/"fix: ..." or
# "update: ..." to a job notification (single Telegram consumer — see
# tgsync.go). mode "fix" fully regenerates from the current master.tex
# (rebuild_one's original behavior); mode "update" reuses the job's cached
# base content and applies edit instructions on top of it. Both apply the
# job's full accumulated instruction history via tailor_resume.py's
# apply_instructions(), not just the newest instruction.
#
# Usage: resume-fix.sh <job_id> <telegram_message_id> <mode> [instruction]

set -euo pipefail

JOB_ID="${1:-}"
REPLY_TO="${2:-}"
MODE="${3:-fix}"
INSTRUCTION="${4:-}"
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

ARGS=(--rebuild "$JOB_ID" "$REPLY_TO" --mode "$MODE")
if [ -n "$INSTRUCTION" ]; then
  ARGS+=(--instruction "$INSTRUCTION")
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" "${ARGS[@]}"
