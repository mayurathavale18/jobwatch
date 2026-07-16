#!/usr/bin/env bash
# Shared helper: each cron wrapper sources this and calls write_status once
# per run, so the dashboard's Cron tab has one uniform place to read job
# health from ($ROOT_DIR/logs/<name>.status.json) instead of parsing each
# script's free-form log output differently.
#
# Usage: write_status <name> <OK|FAIL|ALERT|SKIP> <detail text>
# Requires $LOG_DIR to already exist (every wrapper mkdir -p's it first).

write_status() {
  local name="$1" status="$2" detail="$3"
  local status_file="$LOG_DIR/${name}.status.json"
  local now
  now="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  local escaped
  escaped=$(python3 -c 'import json,sys; print(json.dumps(sys.argv[1]))' "$detail" 2>/dev/null) \
    || escaped="\"$detail\""
  printf '{"name":"%s","last_run":"%s","status":"%s","detail":%s}\n' \
    "$name" "$now" "$status" "$escaped" > "$status_file"
}
