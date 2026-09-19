#!/usr/bin/env bash
#
# Installs a pre-push hook that warns when documentation has drifted.
#
# WHY THIS IS OPT-IN
#
# Git hooks are not version-controlled and are not shared: installing one
# silently would mean a check appears on one machine and not another, and
# nobody would know which. So this is a deliberate, reversible action a person
# takes, and `--uninstall` puts it back.
#
# WHY IT WARNS RATHER THAN BLOCKS
#
# A pre-push hook that blocks is a hook that gets bypassed with `--no-verify`
# on the first genuinely urgent push, and after that it is bypassed by habit.
# The place to BLOCK is CI (`.github/workflows/docs-check.yml`), where the
# check runs on a machine nobody is in a hurry on. The job of this hook is
# smaller and more useful: tell you now, while the change is still in your
# head, rather than in a review three days later.

set -euo pipefail

REPO="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOOK="$REPO/.git/hooks/pre-push"

if [ "${1:-}" = "--uninstall" ]; then
  if [ -f "$HOOK" ] && grep -q 'b-edge docs-check hook' "$HOOK"; then
    rm "$HOOK"; echo "Removed $HOOK"
  else
    echo "No b-edge docs hook installed at $HOOK"
  fi
  exit 0
fi

if [ -e "$HOOK" ] && ! grep -q 'b-edge docs-check hook' "$HOOK"; then
  echo "A different pre-push hook already exists at:" >&2
  echo "  $HOOK" >&2
  echo "Refusing to overwrite it. Merge the two by hand, or move it aside." >&2
  exit 1
fi

cat > "$HOOK" <<'HOOK_BODY'
#!/usr/bin/env bash
# b-edge docs-check hook — installed by scripts/install-docs-hook.sh
#
# Warns, never blocks. Remove with: ./scripts/install-docs-hook.sh --uninstall
REPO="$(git rev-parse --show-toplevel)"
if [ -x "$REPO/scripts/check-docs.sh" ]; then
  if ! "$REPO/scripts/check-docs.sh" >/tmp/bedge-docs-check.$$ 2>&1; then
    echo
    echo "────────────────────────────────────────────────────────────"
    echo " Documentation drift detected. Pushing anyway."
    echo "────────────────────────────────────────────────────────────"
    cat /tmp/bedge-docs-check.$$
    echo "────────────────────────────────────────────────────────────"
    echo " Run /sync-docs to resolve, or 'make docs-check' to re-read."
    echo
  fi
  rm -f /tmp/bedge-docs-check.$$
fi
exit 0
HOOK_BODY

chmod +x "$HOOK"
echo "Installed $HOOK"
echo "It warns on push and never blocks. Remove with:"
echo "  ./scripts/install-docs-hook.sh --uninstall"
