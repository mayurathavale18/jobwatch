#!/bin/bash
# Weekly backup — copy DB, rotate keeping last 4
BACKUP_DIR="$HOME/jobwatch/backups"
mkdir -p "$BACKUP_DIR"

BACKUP_FILE="$BACKUP_DIR/jobwatch-$(date +%F).db"
cp "$HOME/jobwatch/jobwatch.db" "$BACKUP_FILE"

# Keep only last 4 backups
ls -t "$BACKUP_DIR"/jobwatch-*.db 2>/dev/null | tail -n +5 | xargs rm -f 2>/dev/null

echo "BACKUP_OK file=$BACKUP_FILE size=$(du -h "$BACKUP_FILE" | cut -f1)"
echo "All backups:"
ls -lht "$BACKUP_DIR"/jobwatch-*.db 2>/dev/null
