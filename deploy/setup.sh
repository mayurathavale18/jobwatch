#!/usr/bin/env bash
# Bootstraps jobwatch on a fresh Ubuntu box (tested for Lightsail/EC2
# Ubuntu 22.04/24.04). Run as root (or via sudo) from inside the cloned
# repo, e.g.:
#
#   git clone <your-fork-url> /opt/jobwatch
#   cd /opt/jobwatch
#   sudo bash deploy/setup.sh
#
# Idempotent: safe to re-run after a `git pull` to pick up new deps/config.
set -euo pipefail

if [ "$(id -u)" -ne 0 ]; then
  echo "Run as root (sudo bash deploy/setup.sh)" >&2
  exit 1
fi

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GO_VERSION="1.25.0"
SERVICE_USER="jobwatch"

echo "==> Installing system packages"
apt-get update -qq
apt-get install -y -qq git curl build-essential poppler-utils ca-certificates

echo "==> Installing Go ${GO_VERSION} (if missing)"
if ! command -v go >/dev/null || [ "$(go version | awk '{print $3}')" != "go${GO_VERSION}" ]; then
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  ln -sf /usr/local/go/bin/go /usr/local/bin/go
  ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt
fi

echo "==> Installing Node.js 22 (if missing)"
if ! command -v node >/dev/null || [ "$(node --version | cut -d. -f1)" != "v22" ]; then
  curl -fsSL https://deb.nodesource.com/setup_22.x | bash - >/dev/null
  apt-get install -y -qq nodejs
fi

echo "==> Installing tectonic (only needed for the optional resume-tailoring feature)"
# The official installer drops the binary into the current directory --
# tailor-resume-wrapper.sh's PATH already includes the repo root for
# exactly this reason (see its own comment).
if [ ! -x "$REPO_DIR/tectonic" ] && ! command -v tectonic >/dev/null; then
  (cd "$REPO_DIR" && curl --proto '=https' --tlsv1.2 -fsSL https://drop-sh.fullyjustified.net | sh) || \
    echo "  tectonic install failed -- skip if you're not using the resume-tailoring cron"
fi

echo "==> Creating service user (${SERVICE_USER})"
id -u "$SERVICE_USER" >/dev/null 2>&1 || useradd --system --home "$REPO_DIR" --shell /usr/sbin/nologin "$SERVICE_USER"
chown -R "$SERVICE_USER:$SERVICE_USER" "$REPO_DIR"

echo "==> Building (frontend + Go binary)"
sudo -u "$SERVICE_USER" bash -lc "cd '$REPO_DIR' && PATH=/usr/local/go/bin:\$PATH make build"

if [ ! -f "$REPO_DIR/.env" ]; then
  cp "$REPO_DIR/.env.example" "$REPO_DIR/.env"
  echo "==> Wrote .env from .env.example -- EDIT IT with your real Telegram bot token/chat id before starting the service"
fi

if [ ! -f "$REPO_DIR/config.yaml" ]; then
  echo "==> No config.yaml found -- copy one in (see README) before starting the service"
fi

echo "==> Installing systemd service"
sed "s#/opt/jobwatch#$REPO_DIR#g" "$REPO_DIR/deploy/jobwatch.service" > /etc/systemd/system/jobwatch.service
systemctl daemon-reload
systemctl enable jobwatch

echo "==> Installing crontab for ${SERVICE_USER} (core jobs only -- edit deploy/crontab.example to add the resume-tailoring pipeline)"
sed "s#/opt/jobwatch#$REPO_DIR#g" "$REPO_DIR/deploy/crontab.example" | crontab -u "$SERVICE_USER" -

cat <<EOF

==> Done. Next steps:
    1. Edit $REPO_DIR/.env with your real Telegram bot token + chat id.
    2. Edit $REPO_DIR/config.yaml with your companies/keywords/locations.
    3. Seed the database once:  sudo -u $SERVICE_USER $REPO_DIR/bin/jobwatch backfill -config $REPO_DIR/config.yaml
    4. Start the dashboard:     systemctl start jobwatch
    5. Check it's up:           systemctl status jobwatch ; curl -s localhost:8787 | head -c 200
EOF
