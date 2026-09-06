#!/usr/bin/env bash
#
# Restore the latest backup into a scratch database and prove it is usable.
#
# WHY THIS EXISTS
#
# A backup nobody has restored is not a backup, it is a file. The failure mode
# this catches is the one that matters: a dump that has been written nightly
# for months and cannot be restored, discovered on the day it is needed.
#
# WHAT IT ACTUALLY CHECKS - not just "pg_restore exited 0"
#
#   1. every expected table is present
#   2. row counts are non-zero where they must be
#   3. btree_gist and pgcrypto survived
#   4. the GIST exclusion constraint EXISTS on the restored bookings table
#   5. that constraint still REJECTS a double booking
#
# (4) and (5) are the point. That constraint is the only thing preventing two
# customers being booked into one slot, it depends on an extension, and a
# restore that quietly dropped it would leave a database that looks complete
# and has lost its most important guarantee.
#
# The scratch database is dropped at the end whether the drill passes or not.
#
# USAGE
#   scripts/restore-drill.sh                       # newest dump in ./backups
#   scripts/restore-drill.sh backups/bedge-….dump  # a specific one

set -euo pipefail

DB_USER="${DB_USER:-postgres}"
BACKUP_DIR="${BACKUP_DIR:-$(cd "$(dirname "$0")/.." && pwd)/backups}"
DOCKER_CONTAINER="${DOCKER_CONTAINER:-bedge-postgres}"
SCRATCH="bedge_restore_drill_$$"

dump="${1:-}"
if [ -z "$dump" ]; then
  dump=$(ls -t "$BACKUP_DIR"/bedge-*.dump 2>/dev/null | head -1 || true)
fi
[ -n "$dump" ] && [ -f "$dump" ] || { echo "FAIL: no dump found in $BACKUP_DIR" >&2; exit 1; }

echo "drill: restoring $(basename "$dump") into $SCRATCH"

psql_scratch() { docker exec -i "$DOCKER_CONTAINER" psql -U "$DB_USER" -d "$SCRATCH" -tAc "$1"; }
cleanup() {
  docker exec "$DOCKER_CONTAINER" psql -U "$DB_USER" -d postgres \
    -c "DROP DATABASE IF EXISTS $SCRATCH;" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker exec "$DOCKER_CONTAINER" psql -U "$DB_USER" -d postgres -c "CREATE DATABASE $SCRATCH;" >/dev/null

# --no-owner: the scratch database has different role ownership and we are
# testing the DATA and SCHEMA, not the grants.
docker exec -i "$DOCKER_CONTAINER" pg_restore --no-owner -U "$DB_USER" -d "$SCRATCH" < "$dump" >/dev/null 2>&1 \
  || echo "  note: pg_restore reported warnings (usually ownership) - continuing to the real checks"

fail=0
check() { # name, actual, expected-condition-description, test-result
  if [ "$4" = "0" ]; then printf "  [PASS] %s\n" "$1"; else printf "  [FAIL] %s (got: %s, want %s)\n" "$1" "$2" "$3"; fail=1; fi
}

# 1. tables
tables=$(psql_scratch "SELECT count(*) FROM pg_tables WHERE schemaname='public';")
[ "$tables" -ge 29 ]; check "all tables restored" "$tables" ">= 29" "$?"

# 2. data
bookings=$(psql_scratch "SELECT count(*) FROM bookings;")
[ "$bookings" -ge 0 ]; check "bookings table readable ($bookings rows)" "$bookings" "any" "$?"
artists=$(psql_scratch "SELECT count(*) FROM artists;")
[ "$artists" -gt 0 ]; check "artists present ($artists)" "$artists" "> 0" "$?"

# 3. extensions
exts=$(psql_scratch "SELECT count(*) FROM pg_extension WHERE extname IN ('btree_gist','pgcrypto');")
[ "$exts" -eq 2 ]; check "btree_gist and pgcrypto restored" "$exts" "2" "$?"

# 4. the constraint exists
excl=$(psql_scratch "SELECT count(*) FROM pg_constraint WHERE conname='bookings_artist_id_tstzrange_excl' AND contype='x';")
[ "$excl" -eq 1 ]; check "GIST exclusion constraint present" "$excl" "1" "$?"

# 5. and it still bites. Insert one booking, then an overlapping one for the
#    same artist; the second must be rejected. A constraint that exists but no
#    longer enforces is the outcome this drill is really looking for.
aid=$(psql_scratch "SELECT id FROM artists LIMIT 1;")
sid=$(psql_scratch "SELECT id FROM salons LIMIT 1;")
stid=$(psql_scratch "SELECT id FROM stores LIMIT 1;")
svid=$(psql_scratch "SELECT id FROM services LIMIT 1;")
cid=$(psql_scratch "SELECT id FROM users LIMIT 1;")

ins() {
  docker exec -i "$DOCKER_CONTAINER" psql -U "$DB_USER" -d "$SCRATCH" -tAc "
    INSERT INTO bookings (salon_id, store_id, artist_id, service_id, customer_id,
                          start_time, end_time, blocked_until, status,
                          original_price, final_price, deposit_amount)
    VALUES ('$sid','$stid','$aid','$svid','$cid',
            '2099-01-01 $1','2099-01-01 $2','2099-01-01 $2','confirmed',10,10,0);" 2>&1
}

# `|| true` on both: the SECOND insert is SUPPOSED to fail, and `set -e`
# would otherwise kill the drill at the exact moment it is doing its job.
first=$(ins "10:00+00" "11:00+00" || true)
second=$(ins "10:30+00" "11:30+00" || true)

if echo "$second" | grep -q "conflicting key value violates exclusion constraint"; then
  printf "  [PASS] %s\n" "exclusion constraint still REJECTS a double booking"
else
  printf "  [FAIL] %s\n" "overlapping booking was ACCEPTED - the guard is gone"
  echo "         first:  $first"
  echo "         second: $second"
  fail=1
fi

echo
if [ "$fail" -eq 0 ]; then
  echo "DRILL PASSED - $(basename "$dump") is restorable and the guards survive"
  exit 0
fi
echo "DRILL FAILED - do not rely on this backup" >&2
exit 1
