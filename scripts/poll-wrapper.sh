#!/bin/bash
# Poll wrapper with failure tracking
# Outputs status to stdout for the cron agent to read
FAILCOUNT_FILE="$HOME/jobwatch/.poll_failcount"
LOGFILE="$HOME/jobwatch/logs/poll-$(date +%Y%m%d).log"

mkdir -p "$HOME/jobwatch/logs"

# Load env vars
source "$HOME/jobwatch/.env"

# Run poll
cd "$HOME/jobwatch"
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
    fi
else
    echo "0" > "$FAILCOUNT_FILE"
    echo "POLL_OK"
    # Extract key stats from output
    echo "$OUTPUT" | grep -E '"(new_jobs|companies_ok|companies_failed|duration)"' || true
fi

exit $EXIT_CODE
