#!/usr/bin/env bash
#
# regression-smoke-test.sh -- Continuum-WM regression suite, test 1 of N.
#
# SCOPE, DELIBERATE: this is a SAFE, NON-DESTRUCTIVE sanity check on
# capture only -- it never closes windows, never launches anything, never
# touches restore. It exists to catch obvious capture-side regressions
# (a schema field silently broken, a window count mismatch, a crash)
# quickly and repeatably, without risking real desktop state. Restore-side
# regression testing (close windows, run restore, verify) is a separate,
# harder problem -- inherently destructive to whatever's currently open --
# and deserves its own, more careful design (see the note at the bottom).
#
# Usage: ./regression-smoke-test.sh [path-to-continuum-cli]
#   defaults to ./continuum-cli if not given

set -euo pipefail

BIN="${1:-./continuum-cli}"
if [[ ! -x "$BIN" ]]; then
    echo "FAIL: $BIN not found or not executable" >&2
    exit 1
fi

TMPFILE="$(mktemp --suffix=.yaml)"
trap 'rm -f "$TMPFILE"' EXIT

FAILURES=0
check() {
    local desc="$1"
    local ok="$2"
    if [[ "$ok" == "true" ]]; then
        echo "  PASS: $desc"
    else
        echo "  FAIL: $desc"
        FAILURES=$((FAILURES + 1))
    fi
}

echo "=== Test 1: capture succeeds and produces valid YAML ==="
if ! "$BIN" capture "$TMPFILE" > /tmp/capture-output.txt 2>&1; then
    echo "  FAIL: capture command itself failed"
    cat /tmp/capture-output.txt
    exit 1
fi
if [[ ! -s "$TMPFILE" ]]; then
    check "capture produced a non-empty file" "false"
    exit 1
fi
check "capture produced a non-empty file" "true"

echo
echo "=== Test 2: schema_version matches what this binary expects ==="
CAPTURED_SCHEMA="$(grep -m1 '^schema_version:' "$TMPFILE" | awk '{print $2}')"
echo "  captured schema_version: $CAPTURED_SCHEMA"
check "schema_version is present and numeric" "$([[ "$CAPTURED_SCHEMA" =~ ^[0-9]+$ ]] && echo true || echo false)"

echo
echo "=== Test 3: 'show' can read back what 'capture' just wrote ==="
if ! "$BIN" show "$TMPFILE" > /tmp/show-output.txt 2>&1; then
    check "show succeeds on the just-captured file" "false"
    cat /tmp/show-output.txt
else
    check "show succeeds on the just-captured file" "true"
fi

echo
echo "=== Test 4: captured entity count is sane vs. live niri window count ==="
# Cross-check against live state independently, via niri directly -- not
# by trusting continuum-cli's own numbers about itself.
LIVE_WINDOW_COUNT="$(niri msg --json windows | jq 'length')"
LIVE_NAMED_COUNT="$(niri msg --json windows | jq '[.[] | select(.app_id != null and .app_id != "")] | length')"
CAPTURED_ENTITY_COUNT="$(grep -c 'app_id:' "$TMPFILE" || true)"

echo "  live windows (total): $LIVE_WINDOW_COUNT"
echo "  live windows (with a real app_id): $LIVE_NAMED_COUNT"
echo "  captured entities: $CAPTURED_ENTITY_COUNT"

# Capture deliberately SKIPS windows with no app_id (see design doc,
# "transient system popups" finding) -- so captured count should be <=
# the named-window count, and should not be wildly smaller (which would
# suggest something is being dropped that shouldn't be, e.g. the
# orphaned-window race we found and fixed earlier).
check "captured count does not exceed live named-window count" \
    "$([[ "$CAPTURED_ENTITY_COUNT" -le "$LIVE_NAMED_COUNT" ]] && echo true || echo false)"

if [[ "$LIVE_NAMED_COUNT" -gt 0 ]]; then
    DIFF=$((LIVE_NAMED_COUNT - CAPTURED_ENTITY_COUNT))
    check "captured count is not suspiciously far below live count (diff <= 2)" \
        "$([[ "$DIFF" -le 2 ]] && echo true || echo false)"
fi

echo
echo "=== Test 5: every column with 2+ entities shares one width_fraction (Phase 7 invariant) ==="
# This directly encodes the confirmed Phase 7 finding: width is a
# per-COLUMN property, shared by every window in it. If this ever fails,
# either niri's behavior changed, or a capture-side regression broke the
# grouping logic.
python3 - "$TMPFILE" <<'PYEOF' || check "column width-sharing invariant holds" "false"
import sys, re

with open(sys.argv[1]) as f:
    content = f.read()

# Crude but dependency-free YAML scan: find each column block's
# width_fraction and count entities under it, by indentation.
columns = re.findall(
    r'width_fraction:\s*([\d.]+)\n((?:.*\n)*?)(?=\s*- persistent_id: col-|\s*- persistent_id: ws-|\Z)',
    content
)
bad = 0
for width, body in columns:
    entity_widths = re.findall(r'width_fraction:\s*([\d.]+)', body)
    # body includes nested entity blocks that don't have their own
    # width_fraction (only height_fraction), so entity_widths should be
    # empty -- if a stray width_fraction shows up per-entity, that's wrong.
    if entity_widths:
        bad += 1

if bad > 0:
    print(f"  found {bad} column(s) with unexpected per-entity width_fraction")
    sys.exit(1)
print("  PASS: column width-sharing invariant holds")
PYEOF

echo
echo "=== Test 6: favorite-workspace flag can be set and survives a re-read ==="
# Auto-detect the target, rather than requiring the caller to know the
# exact output name -- this doubles as an early real test of the
# eventual product feature (design doc backlog: "default monitor
# detection"). Heuristic: internal laptop panels are conventionally named
# eDP-* regardless of the trailing number, which is EXACTLY why this
# matters here -- this project's own testing has seen the same physical
# panel enumerate as eDP-1 on one reboot and eDP-2 on the next. Falls back
# to the first idx=1 workspace found (any output) if no eDP-* output
# exists in this capture, e.g. a desktop with no internal panel at all.
read -r TARGET_WS_ID TARGET_OUTPUT <<< "$(python3 - "$TMPFILE" <<'PYEOF'
import sys, re

path = sys.argv[1]
with open(path) as f:
    content = f.read()

workspaces = re.findall(
    r'- persistent_id: (ws-[\w-]+)\n\s*output_hint: ([\w.\-]+)\n\s*idx_hint: (\d+)',
    content
)

edp_candidates = [(pid, output) for pid, output, idx in workspaces if idx == "1" and output.startswith("eDP-")]
any_candidates = [(pid, output) for pid, output, idx in workspaces if idx == "1"]

if edp_candidates:
    pid, output = edp_candidates[0]
elif any_candidates:
    pid, output = any_candidates[0]
else:
    sys.exit(0)  # nothing at idx=1 at all -- print nothing, handled below

print(pid, output)
PYEOF
)"

if [[ -z "$TARGET_WS_ID" ]]; then
    echo "  (skipping: no workspace at idx=1 found in this capture at all -- not a failure, just nothing to test against right now)"
else
    echo "  auto-detected favorite target: $TARGET_WS_ID (output=$TARGET_OUTPUT, idx=1)"

    if ! "$BIN" set-favorite "$TMPFILE" "$TARGET_WS_ID" > /tmp/set-favorite-output.txt 2>&1; then
        check "set-favorite command succeeds" "false"
        cat /tmp/set-favorite-output.txt
    else
        check "set-favorite command succeeds" "true"

        # Re-read the file set-favorite just rewrote (session.Save is
        # atomic -- see design doc -- so this is always either the old or
        # fully-new content, never a half-write).
        FAVORITE_COUNT="$(grep -c 'is_favorite: true' "$TMPFILE" || true)"
        check "exactly one workspace is marked favorite after set-favorite" \
            "$([[ "$FAVORITE_COUNT" -eq 1 ]] && echo true || echo false)"

        # Confirm it's specifically OUR target workspace that's favorite,
        # not some other one -- extract the block for TARGET_WS_ID and
        # check its own is_favorite value directly, rather than just
        # trusting the global count above.
        TARGET_IS_FAVORITE="$(python3 - "$TMPFILE" "$TARGET_WS_ID" <<'PYEOF'
import sys, re
path, target = sys.argv[1], sys.argv[2]
with open(path) as f:
    content = f.read()
m = re.search(re.escape(target) + r'\n\s*output_hint:.*\n\s*idx_hint:.*\n\s*is_favorite: (true|false)', content)
print(m.group(1) if m else "not_found")
PYEOF
)"
        check "the TARGET workspace specifically is the one marked favorite" \
            "$([[ "$TARGET_IS_FAVORITE" == "true" ]] && echo true || echo false)"
    fi
fi

echo
echo "================================"
if [[ "$FAILURES" -eq 0 ]]; then
    echo "ALL CHECKS PASSED"
    exit 0
else
    echo "$FAILURES CHECK(S) FAILED"
    exit 1
fi

# --- Notes on what's deliberately NOT covered here yet ---
#
# Restore-side testing (close a window, restore it, verify it comes back
# correctly) is NOT included in this script, on purpose: it's inherently
# destructive to whatever's currently open on the real desktop, which
# makes it unsafe to run casually/repeatedly the way this smoke test is
# meant to be run. A real restore regression suite would need either:
#   (a) a dedicated, disposable test workspace with known throwaway apps
#       (e.g. always spawn fresh Kitty/Ghostty instances into a specific
#       test workspace, never touch anything else), or
#   (b) a mocked niri.Client (Go interface, not this bash script) so
#       matching/reconciliation logic can be tested without a live
#       compositor at all -- covers Reconcile/Entity's decision logic
#       directly, but not real placement/layout behavior.
# Both are real, valuable next steps -- just bigger undertakings than
# this first smoke test, and deserve their own design pass.
