#!/bin/bash
# Weekly backup — copy DB, rotate keeping last 4
ROOT_DIR="$HOME/jobwatch"
LOG_DIR="$ROOT_DIR/logs"
BACKUP_DIR="$ROOT_DIR/backups"
mkdir -p "$BACKUP_DIR" "$LOG_DIR"
source "$ROOT_DIR/scripts/lib/status.sh"

BACKUP_FILE="$BACKUP_DIR/jobwatch-$(date +%F).db"
cp "$ROOT_DIR/jobwatch.db" "$BACKUP_FILE"

# Keep only last 4 backups
ls -t "$BACKUP_DIR"/jobwatch-*.db 2>/dev/null | tail -n +5 | xargs rm -f 2>/dev/null

SIZE=$(du -h "$BACKUP_FILE" | cut -f1)
echo "BACKUP_OK file=$BACKUP_FILE size=$SIZE"
echo "All backups:"
ls -lht "$BACKUP_DIR"/jobwatch-*.db 2>/dev/null

write_status "weekly-backup" "OK" "file=$(basename "$BACKUP_FILE") size=$SIZE"
