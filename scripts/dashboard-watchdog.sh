#!/bin/bash
# Dashboard watchdog - restart if not responding
# Outputs status to stdout for the cron agent

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8787/ 2>/dev/null)

if [ "$HTTP_CODE" = "200" ]; then
    echo "DASHBOARD_OK"
    exit 0
fi

echo "DASHBOARD_DOWN http=$HTTP_CODE"

# Kill any existing jobwatch serve processes
pkill -f "jobwatch serve" 2>/dev/null
sleep 1

# Start fresh
# Load env vars
source "$HOME/jobwatch/.env"
cd "$HOME/jobwatch"
nohup ./bin/jobwatch serve > /tmp/jobwatch-serve.log 2>&1 &
SERVE_PID=$!
sleep 2

HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8787/ 2>/dev/null)
if [ "$HTTP_CODE" = "200" ]; then
    echo "DASHBOARD_RESTARTED pid=$SERVE_PID"
else
    echo "DASHBOARD_RESTART_FAILED http=$HTTP_CODE"
    exit 1
fi
