#!/usr/bin/env bash
#
# Answers one question: "what documentation is now wrong?"
#
# WHY THIS EXISTS
#
# Documentation in this project does not rot because anyone is careless. It
# rots because nothing fails when it does. A migration lands, the build is
# green, the tests pass, the PR merges - and `DOCUMENTATION.md` still says 33
# migrations. Ten of those in a row is how the index came to be a fortnight
# behind while every commit in between was individually fine.
#
# So this is a FAILING CHECK, not a report nobody reads. It exits non-zero
# when the docs contradict the code, which is the only thing that makes a
# documentation rule survive contact with a deadline.
#
# WHAT IT CHECKS - TWO DIFFERENT KINDS OF WRONG
#
# 1. FACT DRIFT. The docs make counted claims ("43 migrations", "29 tables").
#    `doc-facts.sh` recomputes them; this compares against the baseline
#    committed at the last doc sync. Mechanical, exact, no judgement.
#
# 2. IMPLICATED SURFACES. Some changes cannot be checked by counting, because
#    the doc that goes stale is PROSE. Adding a customer-facing screen does
#    not change any number, but it does mean `customer-guide.ts` is now
#    missing a topic. So changed paths are mapped to the documents that have
#    a standing relationship with them, and those are raised for review.
#
#    This half deliberately reports MORE than is strictly wrong. A false
#    positive costs someone ten seconds; a false negative is how the help
#    pages come to describe a product that no longer exists.
#
# WHAT IT DOES NOT DO
#
# It does not edit anything. Deciding whether a new screen deserves a help
# topic, and writing that topic in the product's voice, is judgement - that
# is the `/sync-docs` skill's job, and this script is what tells it where to
# look. Keeping the detector separate from the writer means the detector can
# run in CI, in a hook, and on a laptop without an agent.
#
# USAGE
#
#   ./scripts/check-docs.sh                # since the last recorded sync
#   ./scripts/check-docs.sh --since HEAD~5
#   ./scripts/check-docs.sh --facts-only   # skip the change mapping
#
# Exit 0 = docs consistent. Exit 1 = something needs attention.

set -uo pipefail

API_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="${BEDGE_WEB_DIR:-$(cd "$API_DIR/.." && pwd)/b-edge-web}"
BASELINE="$API_DIR/project-docs/doc-facts.baseline"
DOCS_INDEX="$API_DIR/project-docs/DOCUMENTATION.md"

SINCE=""; FACTS_ONLY=0
while [ $# -gt 0 ]; do
  case "$1" in
    --since) SINCE="${2:-}"; shift 2 ;;
    --facts-only) FACTS_ONLY=1; shift ;;
    -h|--help) sed -n '2,50p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

bold() { printf '\033[1m%s\033[0m\n' "$1"; }
issues=0

# ───────────────────────────────────────────────── 1. fact drift

bold "1. Counted claims"

CURRENT="$("$API_DIR/scripts/doc-facts.sh")"

if [ ! -f "$BASELINE" ]; then
  echo "   No baseline at project-docs/doc-facts.baseline."
  echo "   This is the first run. Current facts:"
  echo "$CURRENT" | sed 's/^/     /'
  echo
  echo "   Record it with:  ./scripts/doc-facts.sh > project-docs/doc-facts.baseline"
  issues=$((issues + 1))
else
  drifted=0
  while IFS='=' read -r key val; do
    [ -z "$key" ] && continue
    old=$(grep -E "^${key}=" "$BASELINE" 2>/dev/null | head -1 | cut -d= -f2-)
    if [ -z "$old" ]; then
      printf '   %-28s %s   (new fact, not in baseline)\n' "$key" "$val"
      drifted=$((drifted + 1))
    elif [ "$old" != "$val" ]; then
      printf '   %-28s %s -> %s\n' "$key" "$old" "$val"
      drifted=$((drifted + 1))
    fi
  done <<< "$CURRENT"

  if [ "$drifted" -eq 0 ]; then
    echo "   ✓ every counted claim matches the baseline"
  else
    echo
    echo "   $drifted fact(s) drifted. The baseline was written at the last doc"
    echo "   sync, so these are changes the documentation has not seen."
    issues=$((issues + 1))
  fi
fi

# The prose block in the index states some of these numbers in a sentence a
# human reads. A baseline can be updated without touching that sentence, so
# it is checked separately - the sentence is the thing anyone actually trusts.
if [ -f "$DOCS_INDEX" ]; then
  claimed_mig=$(grep -oE '\*\*[0-9]+ migrations\*\*' "$DOCS_INDEX" | head -1 | grep -oE '[0-9]+')
  actual_mig=$(echo "$CURRENT" | grep '^api_migrations=' | cut -d= -f2)
  if [ -n "$claimed_mig" ] && [ "$claimed_mig" != "$actual_mig" ]; then
    echo
    echo "   ⚠ DOCUMENTATION.md prose says \"$claimed_mig migrations\"; the repo has $actual_mig."
    echo "     Fix the 'Verified against code' block, not just the baseline."
    issues=$((issues + 1))
  fi
fi

[ "$FACTS_ONLY" -eq 1 ] && { echo; exit $(( issues > 0 ? 1 : 0 )); }

# ───────────────────────────────────────────────── 2. implicated surfaces

echo
bold "2. Documents implicated by the changes"

# Default range: the commit in which the baseline was last written.
#
# ASKED OF GIT, NOT RECORDED IN THE FILE. An earlier version stored
# `_synced_commit=$(git rev-parse HEAD)` inside the baseline, which cannot
# work: a commit's own hash is not knowable until after it exists, so the
# value was always one commit stale, and `--amend` invalidated it outright -
# it recorded a hash that no longer existed in the repository at all.
#
# The commit that last touched the baseline file IS the last doc sync, by
# definition. It needs no bookkeeping, survives amend and rebase, and cannot
# disagree with reality.
if [ -z "$SINCE" ]; then
  SINCE=$(git -C "$API_DIR" log -1 --format=%H -- "$BASELINE" 2>/dev/null)
  [ -z "$SINCE" ] && SINCE="HEAD~1"
fi

changed_in() {
  local dir="$1" range="$2"
  git -C "$dir" rev-parse --verify "$range" >/dev/null 2>&1 || range="HEAD~1"
  {
    git -C "$dir" diff --name-only "$range"...HEAD 2>/dev/null
    git -C "$dir" diff --name-only HEAD 2>/dev/null          # unstaged
    git -C "$dir" diff --name-only --cached 2>/dev/null      # staged
    git -C "$dir" ls-files --others --exclude-standard 2>/dev/null
  } | sort -u | grep -v '^$'
}

API_CHANGED="$(changed_in "$API_DIR" "$SINCE")"

# The web repo has no baseline of its own, and no commit in common with this
# one - they are separate repositories. So the shared reference is TIME: a
# documentation sync happens at a moment, and anything committed to either
# repo after that moment has not been through it.
#
# Using HEAD~1 here instead, as the first version did, meant the most recent
# web commit was re-flagged on every run forever - a permanent false positive,
# which is exactly the kind of noise that teaches people to ignore a check.
SYNC_TIME=$(git -C "$API_DIR" log -1 --format=%cI -- "$BASELINE" 2>/dev/null)

WEB_CHANGED=""
if [ -d "$WEB_DIR/.git" ]; then
  if [ -n "$SYNC_TIME" ]; then
    WEB_CHANGED="$( {
      git -C "$WEB_DIR" log --since="$SYNC_TIME" --format= --name-only 2>/dev/null
      git -C "$WEB_DIR" diff --name-only HEAD 2>/dev/null
      git -C "$WEB_DIR" diff --name-only --cached 2>/dev/null
      git -C "$WEB_DIR" ls-files --others --exclude-standard 2>/dev/null
    } | sort -u | grep -v '^$' )"
  else
    WEB_CHANGED="$(changed_in "$WEB_DIR" HEAD~1)"
  fi
fi

api_n=$(echo "$API_CHANGED" | grep -c . || true)
web_n=$(echo "$WEB_CHANGED" | grep -c . || true)
echo "   b-edge-api: $api_n changed file(s) since ${SINCE:0:7}"
echo "   b-edge-web: $web_n changed file(s) since ${SYNC_TIME:-HEAD~1}"
echo

# Each rule is: a path pattern, the document it implicates, and the REASON
# that relationship exists. The reason is printed because a bare filename
# tells the reader nothing about what to go and change.
#
# Rules live here rather than in the skill so that CI and a laptop without an
# agent enforce exactly the same relationships.
flag() {  # flag <pattern> <where> <document> <reason>
  local pattern="$1" where="$2" doc="$3" reason="$4" hits
  case "$where" in
    api) hits=$(echo "$API_CHANGED" | grep -E "$pattern" || true) ;;
    web) hits=$(echo "$WEB_CHANGED" | grep -E "$pattern" || true) ;;
  esac
  # The help guides live INSIDE the feature directories they document, so a
  # guide edit matched its own rule and the checker asked for the very
  # update that had just been made. Updating a doc is not a reason to flag
  # that doc.
  hits=$(echo "$hits" | grep -v '/help/' || true)
  [ -z "$hits" ] && return 0
  printf '   \033[1m%s\033[0m\n' "$doc"
  printf '     why: %s\n' "$reason"
  echo "$hits" | head -4 | sed 's/^/     - /'
  local n; n=$(echo "$hits" | grep -c .)
  [ "$n" -gt 4 ] && echo "     - ... and $((n - 4)) more"
  echo
  issues=$((issues + 1))
}

flag '^db/migrations/.*\.up\.sql$' api \
  'project-docs/DOCUMENTATION.md + B-Edge-ERD.html' \
  'a migration changes the schema version, the table count and the ERD; the index header states all three'

flag '^internal/[a-z]+/handler\.go$' api \
  'make swagger + project-docs/E2E-TEST-PLAN.md' \
  'the HTTP surface changed, so the generated spec and the end-to-end suite both describe something older'

flag '^internal/pkg/' api \
  'project-docs/CLAUDE.md (leaf packages)' \
  'CLAUDE.md names the leaf packages and why each exists; a new or changed one belongs in that list'

flag '_test\.go$' api \
  'project-docs/DOCUMENTATION.md (test counts)' \
  'the index quotes a Go test count as evidence of coverage'

flag '^(\.env\.example|internal/config/)' api \
  'README.md + B-Edge-Deployment-Runbook-v1.md' \
  'configuration is what someone needs before the app will start; both documents list the variables'

flag '^project-docs/.*\.md$' api \
  'project-docs/DOCUMENTATION.md (the index)' \
  'the index carries a one-line description of every document; a new or renamed file is invisible until it is listed'

flag 'customer-pwa/src/app/features/' web \
  'customer-guide.ts (help)' \
  'a customer-facing screen changed; the in-app guide is the only place the customer is told how it works'

flag 'artist-dashboard/src/app/features/dashboard/' web \
  'artist-guide.ts (help)' \
  'an artist-facing screen changed; the artist guide is the support surface for it'

flag 'artist-dashboard/src/app/features/admin' web \
  'admin-guide.ts (help)' \
  'an admin screen changed; there is exactly one admin and the guide is their runbook'

flag '\.routes\.ts$' web \
  'project-docs/E2E-TEST-PLAN.md + help guides' \
  'navigation changed, so the test plan walks a route map that no longer matches and a guide may point at a dead screen'

flag '(README|readme)' web \
  'b-edge-web/README.md' \
  'the README was touched; confirm the commands in it still run'

# ───────────────────────────────────────────────── verdict

if [ "$issues" -eq 0 ]; then
  bold "✓ Documentation is consistent with the code."
  exit 0
fi

bold "$issues item(s) need attention."
echo "Run /sync-docs to work through them, or update by hand and then:"
echo "  ./scripts/doc-facts.sh > project-docs/doc-facts.baseline"
echo "  git add project-docs/doc-facts.baseline   # committing it IS the sync record"
exit 1
