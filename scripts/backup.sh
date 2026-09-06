#!/usr/bin/env bash
#
# Take a compressed, verifiable backup of the B-Edge database.
#
# WHY THIS EXISTS
#
# This database holds deposit records, booking history and artist earnings.
# Losing it is not a degraded service, it is the end of the business's record
# of who owes whom. Self-hosting Postgres is the right call at this stage
# (see project-docs/B-Edge-Deployment-Runbook-v1.md), and backups are the one
# thing managed Postgres would have bought that must be replaced rather than
# skipped.
#
# WHAT THIS DOES NOT DO
#
# It does not give point-in-time recovery. A nightly dump means losing up to
# 24 hours of bookings in a disaster. That is an accepted trade at one launch
# artist and it stops being acceptable at real volume - at which point the
# answer is WAL archiving (pgBackRest / wal-g), not a tighter cron.
#
# Stated here rather than discovered during an incident.
#
# USAGE
#   scripts/backup.sh                    # local docker Postgres
#   PGHOST=db.internal scripts/backup.sh # a real host
#
# ENVIRONMENT
#   BACKUP_DIR        where dumps land          (default ./backups)
#   RETAIN_DAYS       how many days to keep     (default 14)
#   DOCKER_CONTAINER  set to use docker exec    (default bedge-postgres)
#   PGHOST            set to bypass docker entirely
#   OFFSITE_CMD       command to copy off-box   (see the note below)

set -euo pipefail

DB_NAME="${DB_NAME:-bedge}"
DB_USER="${DB_USER:-postgres}"
BACKUP_DIR="${BACKUP_DIR:-$(cd "$(dirname "$0")/.." && pwd)/backups}"
RETAIN_DAYS="${RETAIN_DAYS:-14}"
DOCKER_CONTAINER="${DOCKER_CONTAINER:-bedge-postgres}"

stamp=$(date -u +%Y%m%dT%H%M%SZ)
out="$BACKUP_DIR/bedge-$stamp.dump"

mkdir -p "$BACKUP_DIR"

# -Fc (custom format) rather than plain SQL: it is compressed, and it allows
# restoring a single table during an incident instead of replaying everything.
if [ -n "${PGHOST:-}" ]; then
  pg_dump -Fc -h "$PGHOST" -U "$DB_USER" -d "$DB_NAME" -f "$out"
else
  docker exec "$DOCKER_CONTAINER" pg_dump -Fc -U "$DB_USER" -d "$DB_NAME" > "$out"
fi

size=$(wc -c < "$out" | tr -d ' ')

# A zero-length or absurdly small dump is a failed backup that exited 0 -
# a container that started but had no database, a permissions error swallowed
# by the pipe. Refuse to keep it, and refuse to rotate anything on the
# strength of it.
if [ "$size" -lt 10000 ]; then
  echo "FAIL: dump is only ${size} bytes - refusing to treat this as a backup" >&2
  rm -f "$out"
  exit 1
fi

# pg_restore --list parses the archive's table of contents. If this fails the
# file is corrupt, and it is better to know now than during a restore.
if ! pg_restore --list "$out" > /dev/null 2>&1; then
  if ! docker exec -i "$DOCKER_CONTAINER" pg_restore --list < "$out" > /dev/null 2>&1; then
    echo "FAIL: dump is not a readable pg_restore archive" >&2
    exit 1
  fi
fi

echo "ok: $out ($(( size / 1024 )) KiB)"

# ── Off-box copy ─────────────────────────────────────────────────────────────
#
# THE MOST IMPORTANT LINE IN THIS FILE, and the one that is not configured by
# default. A dying disk takes the local backups with it, so a backup that only
# exists on the database host is not a backup - it is a convenience copy.
#
# Set OFFSITE_CMD to anything that accepts the file path, e.g.:
#   OFFSITE_CMD='rclone copy {} r2:bedge-backups/'
#   OFFSITE_CMD='aws s3 cp {} s3://bedge-backups/ --endpoint-url …'
#
# Cloudflare R2 and Backblaze B2 both have free tiers larger than this
# database will be for a long time.
if [ -n "${OFFSITE_CMD:-}" ]; then
  cmd="${OFFSITE_CMD//\{\}/$out}"
  if eval "$cmd"; then
    echo "ok: copied off-box"
  else
    # Loud, and non-zero: a local-only backup is the failure mode this whole
    # script exists to avoid, so it must not exit 0 and look healthy in cron.
    echo "FAIL: off-box copy failed - this backup exists in ONE place" >&2
    exit 1
  fi
else
  echo "WARNING: OFFSITE_CMD unset - this backup exists only on this machine" >&2
fi

# Rotation happens LAST, and only after a verified dump, so a failing backup
# can never delete the good ones that came before it.
deleted=$(find "$BACKUP_DIR" -name 'bedge-*.dump' -type f -mtime "+$RETAIN_DAYS" -print -delete | wc -l | tr -d ' ')
[ "$deleted" -gt 0 ] && echo "rotated: removed $deleted dump(s) older than $RETAIN_DAYS days"

exit 0
