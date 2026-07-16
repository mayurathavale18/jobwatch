#!/usr/bin/env bash
# jobwatch resume-tailoring wrapper — generates tailored resumes + PDFs for
# new jobs and sends them over Telegram. Without this scheduled, new jobs
# only ever get the plain-text poll notification -- never a tailored PDF.
#
# A single invocation can take several minutes (tailor_resume.py rate-limits
# itself to 1 Telegram send/60s, up to MAX_PER_CYCLE=8 jobs/run), so this
# follows the same lock-file pattern as tg-sync-wrapper.sh to never overlap.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$ROOT_DIR/logs"
LOCK_FILE="$ROOT_DIR/.tailor-resume.lock"

# tailor_resume.py resolves its own paths via Path.home(), so this isn't
# strictly required today, but matching poll-wrapper.sh's/tg-sync-wrapper.sh's
# cd-to-root convention avoids relying on that staying true.
cd "$ROOT_DIR"
FAIL_COUNTER="$ROOT_DIR/.tailor-resume-failures"

mkdir -p "$LOG_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

# Prevent concurrent runs
if [ -f "$LOCK_FILE" ]; then
  LOCK_PID=$(cat "$LOCK_FILE" 2>/dev/null)
  if kill -0 "$LOCK_PID" 2>/dev/null; then
    echo "TAILOR_SKIP: previous run (PID $LOCK_PID) still active"
    exit 0
  else
    rm -f "$LOCK_FILE"
  fi
fi
echo $$ > "$LOCK_FILE"
trap 'rm -f "$LOCK_FILE"' EXIT

LOG_FILE="$LOG_DIR/tailor-resume-$(date +%Y%m%d).log"
STDERR_FILE=$(mktemp)

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting tailor_resume.py" >> "$LOG_FILE"

if python3 "$SCRIPT_DIR/tailor_resume.py" >> "$LOG_FILE" 2>"$STDERR_FILE"; then
  echo "0" > "$FAIL_COUNTER"
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] TAILOR_OK" >> "$LOG_FILE"
  echo "TAILOR_OK"
else
  EXIT_CODE=$?
  STDERR_TAIL=$(tail -5 "$STDERR_FILE" 2>/dev/null || echo "(no stderr)")
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] TAILOR_FAIL (exit $EXIT_CODE): $STDERR_TAIL" >> "$LOG_FILE"

  FAILURES=0
  if [ -f "$FAIL_COUNTER" ]; then
    FAILURES=$(cat "$FAIL_COUNTER" 2>/dev/null || echo 0)
  fi
  FAILURES=$((FAILURES + 1))
  echo "$FAILURES" > "$FAIL_COUNTER"

  if [ "$FAILURES" -ge 3 ]; then
    echo "TAILOR_ALERT: 3 consecutive failures. Last error: $STDERR_TAIL"
  else
    echo "TAILOR_FAIL: failure $FAILURES/3. stderr: $STDERR_TAIL"
  fi
fi

rm -f "$STDERR_FILE"
