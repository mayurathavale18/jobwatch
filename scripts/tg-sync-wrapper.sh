#!/usr/bin/env bash
# jobwatch tg-sync wrapper — drains Telegram replies and applies status/notes
# changes, including "fix" replies (handled inside the jobwatch binary
# itself -- see internal/tgsync -- which shells out to resume-fix.sh).
# Runs every 5 min between 07:00–24:00 IST. Never overlaps.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$ROOT_DIR/logs"
LOCK_FILE="$ROOT_DIR/.tg-sync.lock"
FAIL_COUNTER="$ROOT_DIR/.tg-sync-failures"

# config.yaml's db_path is relative ("./jobwatch.db"); cron's default cwd is
# $HOME, not this repo, so without this cd the jobwatch binary silently
# opened/created an empty db at $HOME/jobwatch.db instead of the real one.
cd "$ROOT_DIR"

mkdir -p "$LOG_DIR"

# Source env
if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

# Prevent concurrent runs
if [ -f "$LOCK_FILE" ]; then
  LOCK_PID=$(cat "$LOCK_FILE" 2>/dev/null)
  if kill -0 "$LOCK_PID" 2>/dev/null; then
    echo "TG_SYNC_SKIP: previous run (PID $LOCK_PID) still active"
    exit 0
  else
    rm -f "$LOCK_FILE"
  fi
fi
echo $$ > "$LOCK_FILE"
trap 'rm -f "$LOCK_FILE"' EXIT

LOG_FILE="$LOG_DIR/tg-sync-$(date +%Y%m%d).log"
STDERR_FILE=$(mktemp)

echo "[$(date '+%Y-%m-%d %H:%M:%S')] Starting tg-sync" >> "$LOG_FILE"

# Run tg-sync (handles status/notes replies and "fix" replies -- the latter
# shells out to resume-fix.sh internally; see internal/tgsync/tgsync.go).
# -fix-script is passed as an absolute path explicitly: its flag default
# ("scripts/resume-fix.sh") is relative to the jobwatch process's cwd, which
# cron does not guarantee to be $ROOT_DIR.
if "$ROOT_DIR/bin/jobwatch" tg-sync -config "$ROOT_DIR/config.yaml" -fix-script "$SCRIPT_DIR/resume-fix.sh" >> "$LOG_FILE" 2>"$STDERR_FILE"; then
  # Success — reset failure counter
  echo "0" > "$FAIL_COUNTER"
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] TG_SYNC_OK" >> "$LOG_FILE"
  echo "TG_SYNC_OK"
else
  EXIT_CODE=$?
  STDERR_TAIL=$(tail -5 "$STDERR_FILE" 2>/dev/null || echo "(no stderr)")
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] TG_SYNC_FAIL (exit $EXIT_CODE): $STDERR_TAIL" >> "$LOG_FILE"

  # Increment failure counter
  FAILURES=0
  if [ -f "$FAIL_COUNTER" ]; then
    FAILURES=$(cat "$FAIL_COUNTER" 2>/dev/null || echo 0)
  fi
  FAILURES=$((FAILURES + 1))
  echo "$FAILURES" > "$FAIL_COUNTER"

  if [ "$FAILURES" -ge 3 ]; then
    echo "TG_SYNC_ALERT: 3 consecutive failures. Last error: $STDERR_TAIL"
  else
    echo "TG_SYNC_FAIL: failure $FAILURES/3. stderr: $STDERR_TAIL"
  fi
fi

rm -f "$STDERR_FILE"
