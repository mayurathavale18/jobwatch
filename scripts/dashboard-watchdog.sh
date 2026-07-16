#!/bin/bash
# Dashboard watchdog - restart if not responding
# Outputs status to stdout for the cron agent

ROOT_DIR="$HOME/jobwatch"
LOG_DIR="$ROOT_DIR/logs"
mkdir -p "$LOG_DIR"
source "$ROOT_DIR/scripts/lib/status.sh"

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8787/ 2>/dev/null)

if [ "$HTTP_CODE" = "200" ]; then
    echo "DASHBOARD_OK"
    write_status "dashboard-watchdog" "OK" "http=$HTTP_CODE"
    exit 0
fi

echo "DASHBOARD_DOWN http=$HTTP_CODE"

# Kill any existing jobwatch serve processes
pkill -f "jobwatch serve" 2>/dev/null
sleep 1

# Start fresh
# Load env vars
source "$ROOT_DIR/.env"
cd "$ROOT_DIR"
nohup ./bin/jobwatch serve > /tmp/jobwatch-serve.log 2>&1 &
SERVE_PID=$!
sleep 2

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8787/ 2>/dev/null)
if [ "$HTTP_CODE" = "200" ]; then
    echo "DASHBOARD_RESTARTED pid=$SERVE_PID"
    write_status "dashboard-watchdog" "ALERT" "was down (http=$HTTP_CODE before restart), restarted pid=$SERVE_PID"
else
    echo "DASHBOARD_RESTART_FAILED http=$HTTP_CODE"
    write_status "dashboard-watchdog" "ALERT" "restart failed, http=$HTTP_CODE"
    exit 1
fi
