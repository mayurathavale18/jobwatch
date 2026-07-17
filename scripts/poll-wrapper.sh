#!/bin/bash
# Poll wrapper with failure tracking
# Outputs status to stdout for the cron agent to read
ROOT_DIR="$HOME/jobwatch"
LOG_DIR="$ROOT_DIR/logs"
FAILCOUNT_FILE="$ROOT_DIR/.poll_failcount"
LOGFILE="$LOG_DIR/poll-$(date +%Y%m%d).log"

mkdir -p "$LOG_DIR"
source "$ROOT_DIR/scripts/lib/status.sh"

# Load env vars
source "$ROOT_DIR/.env"

# Run poll
cd "$ROOT_DIR"

# Rebuild before polling so cron never runs a stale binary against fixed
# source (e.g. the workday.go URL fix that shipped but wasn't picked up
# until the next manual build). go build is a fast no-op when nothing
# changed; if it fails, keep the last good binary and log the failure
# instead of blocking the poll.
BUILD_OUTPUT=$(go build -o bin/jobwatch ./cmd/jobwatch 2>&1)
if [ $? -ne 0 ]; then
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] BUILD_FAILED, using existing binary" >> "$LOGFILE"
    echo "$BUILD_OUTPUT" >> "$LOGFILE"
fi

OUTPUT=$(./bin/jobwatch poll 2>&1)
EXIT_CODE=$?
TIMESTAMP=$(date '+%Y-%m-%d %H:%M:%S')

echo "[$TIMESTAMP] exit=$EXIT_CODE" >> "$LOGFILE"
echo "$OUTPUT" >> "$LOGFILE"
echo "---" >> "$LOGFILE"

if [ $EXIT_CODE -ne 0 ]; then
    FAILCOUNT=$(cat "$FAILCOUNT_FILE" 2>/dev/null || echo 0)
    FAILCOUNT=$((FAILCOUNT + 1))
    echo "$FAILCOUNT" > "$FAILCOUNT_FILE"
    
    LAST_ERROR=$(echo "$OUTPUT" | tail -10)
    echo "POLL_FAILED exit=$EXIT_CODE consecutive=$FAILCOUNT"
    echo "LAST_ERROR: $LAST_ERROR"
    
    if [ $FAILCOUNT -ge 3 ]; then
        echo "THREE_CONSECUTIVE_FAILURES"
        write_status "poll" "ALERT" "3 consecutive failures. Last: $LAST_ERROR"
    else
        write_status "poll" "FAIL" "failure $FAILCOUNT/3: $LAST_ERROR"
    fi
else
    echo "0" > "$FAILCOUNT_FILE"
    echo "POLL_OK"
    # Extract key stats from output
    STATS=$(echo "$OUTPUT" | grep -E '"(new_jobs|companies_ok|companies_failed|duration)"' || true)
    echo "$STATS"
    write_status "poll" "OK" "$STATS"
fi

exit $EXIT_CODE
