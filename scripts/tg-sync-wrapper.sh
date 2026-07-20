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
source "$SCRIPT_DIR/lib/status.sh"

# Source env
if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

# Prevent concurrent runs. flock on an open fd is a single atomic syscall --
# the previous check-then-write PID-file pattern here had a TOCTOU race: two
# invocations starting close together (scheduled cron overlapping a
# dashboard "Run now" click, or a double-click) could both pass the check
# before either wrote the file, and run concurrently. Both would then fetch
# the *same* batch of pending Telegram updates (offset only advances once,
# at the end of a run) and process them twice.
exec 200>"$LOCK_FILE"
if ! flock -n 200; then
  echo "TG_SYNC_SKIP: another run already holds the lock"
  write_status "tg-sync" "SKIP" "another run already holds the lock"
  exit 0
fi

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
  write_status "tg-sync" "OK" "$(tail -1 "$LOG_FILE" 2>/dev/null)"
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
    write_status "tg-sync" "ALERT" "3 consecutive failures. Last: $STDERR_TAIL"
  else
    echo "TG_SYNC_FAIL: failure $FAILURES/3. stderr: $STDERR_TAIL"
    write_status "tg-sync" "FAIL" "failure $FAILURES/3: $STDERR_TAIL"
  fi
fi

rm -f "$STDERR_FILE"
