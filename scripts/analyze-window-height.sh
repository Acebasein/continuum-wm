#!/usr/bin/env bash
#
# analyze-window-height.sh -- Phase 7 research tool, NOT part of
# continuum-wm itself. Verifies, empirically, two open questions:
#
#   1. `set-window-height --help` documents an --id option (unlike
#      set-column-width, which has none at all). Does --id ACTUALLY work
#      independent of focus, or does it silently fall back to affecting
#      the focused window regardless, the way set-column-width does? We
#      don't trust the help text alone -- we test it directly.
#   2. Does `set-window-height <N>%` produce close to N% of the output's
#      logical HEIGHT, with a similar small systematic offset to what we
#      found for width (tile_size vs window_size, gaps, etc.)?
#
# Usage: ./analyze-window-height.sh <app_id>
#   e.g. ./analyze-window-height.sh kitty
#
# Requires: niri, jq, awk. Safe to run: only affects the ONE window
# matching app_id's height.

set -euo pipefail

APP_ID="${1:-}"
if [[ -z "$APP_ID" ]]; then
    echo "usage: $0 <app_id>" >&2
    echo "example: $0 kitty" >&2
    exit 1
fi

echo "=== Step 1: find the target window (app_id=$APP_ID) ==="
WINDOW_JSON="$(niri msg --json windows | jq -c --arg app "$APP_ID" '.[] | select(.app_id == $app)')"
if [[ -z "$WINDOW_JSON" ]]; then
    echo "no window found with app_id=$APP_ID -- open one first" >&2
    exit 1
fi
WINDOW_COUNT="$(echo "$WINDOW_JSON" | wc -l)"
if [[ "$WINDOW_COUNT" -gt 1 ]]; then
    echo "more than one window matches app_id=$APP_ID -- aborting" >&2
    exit 1
fi

WINDOW_ID="$(echo "$WINDOW_JSON" | jq -r '.id')"
WORKSPACE_ID="$(echo "$WINDOW_JSON" | jq -r '.workspace_id')"
echo "target window id=$WINDOW_ID (workspace_id=$WORKSPACE_ID)"
echo

echo "=== Step 2: find that workspace's output, and the output's logical height ==="
OUTPUT_NAME="$(niri msg --json workspaces | jq -r --argjson wsid "$WORKSPACE_ID" '.[] | select(.id == $wsid) | .output')"
if [[ -z "$OUTPUT_NAME" || "$OUTPUT_NAME" == "null" ]]; then
    echo "could not determine output for workspace_id=$WORKSPACE_ID" >&2
    exit 1
fi
OUTPUT_HEIGHT="$(niri msg --json outputs | jq -r --arg name "$OUTPUT_NAME" '.[$name].logical.height // empty')"
if [[ -z "$OUTPUT_HEIGHT" ]]; then
    echo "could not read logical height for output $OUTPUT_NAME -- check niri msg --json outputs directly" >&2
    exit 1
fi
echo "output: $OUTPUT_NAME  logical height: ${OUTPUT_HEIGHT}px"
echo

echo "=== Step 3: does --id actually work independent of focus? ==="
OTHER_WINDOW_ID="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '[.[] | select(.id != $id)] | .[0].id // empty')"
if [[ -z "$OTHER_WINDOW_ID" ]]; then
    echo "  (only one window open -- can't test --id-vs-focus independence this way; skipping to Step 4)"
else
    echo "Focusing the OTHER window (deliberately NOT the target)..."
    niri msg action focus-window --id "$OTHER_WINDOW_ID"
    sleep 0.3

    BEFORE_TARGET_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[1]')"
    BEFORE_OTHER_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$OTHER_WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[1]')"

    # Pick a test percentage guaranteed to differ from the target's
    # current height, so a real change (if any) is unambiguous -- same
    # lesson learned from the column-width script's first, coincidental
    # non-result.
    TARGET_CURRENT_PCT="$(awk -v h="$BEFORE_TARGET_HEIGHT" -v oh="$OUTPUT_HEIGHT" 'BEGIN{printf "%.0f", h/oh*100}')"
    if [[ "$TARGET_CURRENT_PCT" -ge 75 ]]; then
        TEST_PCT="50"
    else
        TEST_PCT="100"
    fi
    echo "  target is currently ~${TARGET_CURRENT_PCT}% height -- testing with ${TEST_PCT}% via --id (other window stays focused throughout)"
    echo "  attempting: niri msg action set-window-height --id $WINDOW_ID ${TEST_PCT}%"
    niri msg action set-window-height --id "$WINDOW_ID" "${TEST_PCT}%"
    sleep 0.3

    AFTER_TARGET_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[1]')"
    AFTER_OTHER_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$OTHER_WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[1]')"

    echo "  target window height (id=$WINDOW_ID, NOT focused):  before=${BEFORE_TARGET_HEIGHT}px  after=${AFTER_TARGET_HEIGHT}px"
    echo "  other window height  (id=$OTHER_WINDOW_ID, focused): before=${BEFORE_OTHER_HEIGHT}px  after=${AFTER_OTHER_HEIGHT}px"
    if [[ "$AFTER_TARGET_HEIGHT" != "$BEFORE_TARGET_HEIGHT" && "$AFTER_OTHER_HEIGHT" == "$BEFORE_OTHER_HEIGHT" ]]; then
        echo "  => CONFIRMED: --id genuinely targets the specified window, independent of focus."
        echo "     No focus-stealing needed for height restoration -- simpler than set-column-width."
    elif [[ "$AFTER_OTHER_HEIGHT" != "$BEFORE_OTHER_HEIGHT" ]]; then
        echo "  => --id is NOT respected: the FOCUSED window changed instead of our --id target."
        echo "     Height restoration will need the same focus-then-restore-focus dance as width."
    else
        echo "  => INCONCLUSIVE: neither height changed. Check for an error above."
    fi
fi
echo

echo "=== Step 4: percentage-accuracy test (via --id, no focus manipulation needed if Step 3 confirmed it works) ==="
for PCT in 50 100; do
    echo "--- requesting ${PCT}% ---"
    niri msg action set-window-height --id "$WINDOW_ID" "${PCT}%"
    sleep 0.3

    OBSERVED_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[1]')"
    OBSERVED_TILE_HEIGHT="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.tile_size[1] // empty')"

    ACTUAL_PCT_WINDOW="$(awk -v h="$OBSERVED_HEIGHT" -v oh="$OUTPUT_HEIGHT" 'BEGIN{printf "%.2f", h/oh*100}')"
    echo "  requested: ${PCT}%"
    echo "  observed window_size height: ${OBSERVED_HEIGHT}px  -> naive computed %: ${ACTUAL_PCT_WINDOW}%"
    if [[ -n "$OBSERVED_TILE_HEIGHT" ]]; then
        ACTUAL_PCT_TILE="$(awk -v h="$OBSERVED_TILE_HEIGHT" -v oh="$OUTPUT_HEIGHT" 'BEGIN{printf "%.2f", h/oh*100}')"
        echo "  observed tile_size height:   ${OBSERVED_TILE_HEIGHT}px  -> naive computed %: ${ACTUAL_PCT_TILE}%"
    fi
    echo
done

echo "=== Done. Same interpretation as the width script: compare requested vs computed %. ==="
