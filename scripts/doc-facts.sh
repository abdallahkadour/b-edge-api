#!/usr/bin/env bash
#
# Prints the facts the documentation makes claims about.
#
# WHY THIS EXISTS
#
# `project-docs/DOCUMENTATION.md` carries a block that reads "Verified against
# code 2026-09-05: 33 migrations, 29 tables, 17 domains, 115 route
# registrations, 645 Go tests." That block is the most useful thing in the
# index and the easiest thing in the repo to get quietly wrong: every one of
# those numbers goes stale on an ordinary working day, and nothing fails when
# it does. On 2026-09-19 the real numbers were 43 / 32 / 24 / 143 / 825 - ten
# migrations of drift, accumulated without a single broken build.
#
# A number in a document is a claim about the code. This script recomputes
# those claims from the code so the claim can be CHECKED rather than trusted.
# It is deliberately dumb and deterministic - no network, no database, no
# judgement. `check-docs.sh` compares this output against what the docs say;
# the `sync-docs` skill decides what to do about a difference.
#
# OUTPUT
#
# `key=value` lines, one per fact, sorted and stable, so callers can diff two
# runs or grep a single key. Values are integers or short strings, never
# prose. Stable output is the point: this is meant to be committed into the
# docs as a baseline and compared later.
#
# WHY SOME COUNTS LOOK "WRONG"
#
# Two counts deliberately disagree with the obvious reading:
#
#   - `tables` is CREATE minus DROP across all migrations, not `CREATE TABLE`
#     occurrences. A table created in 001 and dropped in 019 is not a table.
#   - `domains` splits into total and route-bearing. `internal/` holds
#     middleware and config alongside real domains, so a bare directory count
#     overstates it - which is how "17 domains" and "24 directories" can both
#     be true and only one of them belongs in a sentence about domains.
#
# The web repo is OPTIONAL. It is a separate git repository that happens to
# sit beside this one, and the documentation index spans both. If it is not
# found, the web_* facts are omitted rather than reported as zero - a missing
# repository and an empty one are different things, and reporting 0 would
# silently "fix" web drift by pretending there is none.

set -euo pipefail

API_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="${BEDGE_WEB_DIR:-$(cd "$API_DIR/.." && pwd)/b-edge-web}"

# Count matches without tripping `set -e` when there are none.
count() { grep -rhoE "$1" "${@:2}" 2>/dev/null | wc -l | tr -d ' '; }

# ---------------------------------------------------------------- b-edge-api

mig_dir="$API_DIR/db/migrations"
migrations=$(ls "$mig_dir"/*.up.sql 2>/dev/null | wc -l | tr -d ' ')
latest=$(ls "$mig_dir"/*.up.sql 2>/dev/null | tail -1 | xargs -r basename | sed 's/\.up\.sql$//')

created=$(grep -rhoiE 'CREATE TABLE (IF NOT EXISTS )?[a-z_]+' "$mig_dir"/*.up.sql 2>/dev/null \
  | awk '{print tolower($NF)}' | sort -u)
dropped=$(grep -rhoiE 'DROP TABLE (IF EXISTS )?[a-z_]+' "$mig_dir"/*.up.sql 2>/dev/null \
  | awk '{print tolower($NF)}' | sort -u)
tables=$(comm -23 <(echo "$created") <(echo "$dropped") | grep -c . || true)

domains_total=$(find "$API_DIR/internal" -maxdepth 1 -mindepth 1 -type d ! -name pkg 2>/dev/null | wc -l | tr -d ' ')
domains_routed=$(find "$API_DIR/internal" -maxdepth 2 -name 'handler.go' 2>/dev/null | wc -l | tr -d ' ')
leaf_packages=$(find "$API_DIR/internal/pkg" -maxdepth 1 -mindepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')

routes=$(count '\.(Get|Post|Put|Patch|Delete)\("' "$API_DIR/internal" --include='*.go')
go_tests=$(count '^func Test[A-Za-z0-9_]+' "$API_DIR" --include='*_test.go')

swagger_paths=$(python3 -c "
import json,sys
try:
    print(len(json.load(open('$API_DIR/docs/swagger.json'))['paths']))
except Exception:
    print('unknown')
" 2>/dev/null || echo unknown)

env_vars=$(grep -rhoE 'os\.Getenv\("[A-Z0-9_]+"\)' "$API_DIR" --include='*.go' 2>/dev/null \
  | grep -oE '"[A-Z0-9_]+"' | tr -d '"' | sort -u | grep -c . || true)

echo "api_migrations=$migrations"
echo "api_migration_latest=${latest:-none}"
echo "api_tables=$tables"
echo "api_domains_total=$domains_total"
echo "api_domains_routed=$domains_routed"
echo "api_leaf_packages=$leaf_packages"
echo "api_route_registrations=$routes"
echo "api_swagger_paths=$swagger_paths"
echo "api_go_tests=$go_tests"
echo "api_env_vars=$env_vars"

# ---------------------------------------------------------------- b-edge-web

if [ -d "$WEB_DIR/projects" ]; then
  specs=$(find "$WEB_DIR/projects" -name '*.spec.ts' -not -path '*/node_modules/*' 2>/dev/null | wc -l | tr -d ' ')

  # Test CASES, not files - the file count says nothing about coverage, and
  # quoting it as if it did is how "6 spec files" got read as "6 tests".
  #
  # Counted statically rather than by running `ng test`, because this script
  # must stay fast and side-effect free. Verified to agree with the runner on
  # 2026-09-19: 33 either way.
  web_tests=$(grep -rhoE "^[[:space:]]*it\(" "$WEB_DIR/projects" --include='*.spec.ts' 2>/dev/null | wc -l | tr -d ' ')
  ng_routes=$(count "path: *'" "$WEB_DIR/projects" --include='*.routes.ts')

  # Help guides are typed data (GuideSection[]), not markdown. A TOPIC is one
  # GuideTopic - the thing a reader actually navigates to - and it is what
  # should grow when a user-facing feature ships.
  #
  # Counted structurally rather than by indentation. Every `id:` in the file
  # is one of three things: the guide root, a section, or a topic. Sections
  # are exactly the objects carrying a `topics: [`, so
  #
  #     topics = all ids - sections - 1 root
  #
  # The first version of this matched on indent width (`^\s{6,}id:`) and
  # silently counted sections as topics, reporting 20 customer topics where
  # there are 15. Indentation is a formatting choice; `topics: [` is the
  # structure. Verified against all three guides: 21/5, 30/7, 9/4 ids and
  # sections resolve to 15, 22 and 4 topics.
  help_total=0
  for g in customer artist admin; do
    f=$(find "$WEB_DIR/projects" -name "${g}-guide.ts" -not -path '*/node_modules/*' 2>/dev/null | head -1)
    if [ -n "$f" ]; then
      ids=$(grep -cE '^[[:space:]]*id: ' "$f" 2>/dev/null || true)
      secs=$(grep -cE 'topics: *\[' "$f" 2>/dev/null || true)
      n=$(( ids - secs - 1 ))
      [ "$n" -lt 0 ] && n=0
      echo "web_help_${g}_topics=$n"
      echo "web_help_${g}_sections=$secs"
      help_total=$((help_total + n))
    fi
  done

  echo "web_help_topics_total=$help_total"
  echo "web_ng_routes=$ng_routes"
  echo "web_spec_files=$specs"
  echo "web_tests=$web_tests"
fi
