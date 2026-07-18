#!/bin/bash
# Daily summary script — queries DB read-only, outputs formatted text
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
LOG_DIR="$ROOT_DIR/logs"
cd "$ROOT_DIR"
mkdir -p "$LOG_DIR"
source "$ROOT_DIR/scripts/lib/status.sh"
source "$ROOT_DIR/.env"

echo "=== JOBWATCH DAILY SUMMARY $(date +%Y-%m-%d) ==="
echo ""

echo "--- Status breakdown (today) ---"
sqlite3 jobwatch.db "SELECT status, COUNT(*) FROM jobs WHERE first_seen_at >= date('now') GROUP BY status;"
echo ""

echo "--- New jobs (up to 10) ---"
sqlite3 -header jobwatch.db "SELECT company_name, title, url FROM jobs WHERE status='new' AND first_seen_at >= date('now') LIMIT 10;"
echo ""

echo "--- Tailoring failures ---"
if [ -f "$ROOT_DIR/resume/tailored.json" ]; then
    FAILED=$(python3 -c "
import json
with open('$ROOT_DIR/resume/tailored.json') as f:
    data = json.load(f)
failed = [k for k,v in data.items() if v.get('status') == 'failed']
for f_item in failed:
    print(f'  Job {f_item}: {data[f_item].get(\"error\", \"unknown\")}')
if not failed:
    print('  None')
" 2>/dev/null || echo "  (could not read tailored.json)")
    echo "$FAILED"
fi
echo ""

echo "--- Tailoring budget ---"
if [ -f "$ROOT_DIR/resume/tailored.json" ]; then
    python3 -c "
import json
from datetime import date
with open('$ROOT_DIR/resume/tailored.json') as f:
    data = json.load(f)
today = date.today().isoformat()
count = sum(1 for v in data.values() if v.get('tailored_date') == today)
print(f'  Runs today: {count}/20')
queued = [k for k,v in data.items() if v.get('status') == 'queued']
if queued:
    print(f'  Queued for tomorrow: {len(queued)}')
" 2>/dev/null
fi

write_status "daily-summary" "OK" "generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
