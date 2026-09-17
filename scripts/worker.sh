#!/usr/bin/env bash
# jobwatch worker entrypoint (k8s Deployment jobwatch-worker): long-polls
# Telegram and runs queued events. Replaces the tg-sync CronJob. Must be the
# only Telegram consumer for the bot -- the Deployment uses the Recreate
# strategy so an old and new pod never poll at the same time.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"
cd "$ROOT_DIR"

if [ -f "$ROOT_DIR/.env" ]; then
  set -a
  source "$ROOT_DIR/.env"
  set +a
fi

exec "$ROOT_DIR/bin/jobwatch" worker -config "$ROOT_DIR/config.yaml"
