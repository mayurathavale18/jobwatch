#!/usr/bin/env bash
# jobwatch outreach-one — runs the founder-outreach step for exactly one
# already-tailored job, on demand.
#
# Invoked two ways: jobwatch's Go tg-sync when a user replies "outreach"/
# "outreach: <email>" to a job notification, and the dashboard's "Draft
# outreach" button (POST /api/jobs/{id}/outreach). Mirrors tailor-one.sh's
# structure exactly.
#
# Usage: outreach-one.sh <job_id> [founder_email]

set -euo pipefail

JOB_ID="${1:-}"
FOUNDER_EMAIL="${2:-}"
if [ -z "$JOB_ID" ]; then
  echo "ERROR: Job ID required" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

cd "$ROOT_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

ARGS=(--outreach --job-id "$JOB_ID")
if [ -n "$FOUNDER_EMAIL" ]; then
  ARGS+=(--founder-email "$FOUNDER_EMAIL")
fi

exec python3 "$SCRIPT_DIR/tailor_resume.py" "${ARGS[@]}"
