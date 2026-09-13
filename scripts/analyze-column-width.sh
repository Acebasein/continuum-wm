#!/usr/bin/env bash
#
# analyze-column-width.sh -- Phase 7 research tool, NOT part of
# continuum-wm itself. Verifies, empirically, two open questions before
# any schema or restore code gets written:
#
#   1. Does `set-column-width <N>%` produce EXACTLY N% of the output's
#      logical width, or does niri's real formula (borders? gaps?) differ
#      from a naive pixel_width / output_width calculation?
#   2. Does `set-column-width` really have no window/column targeting at
#      all -- does it always affect whatever's currently FOCUSED,
#      regardless of which window we actually intended?
#
# Per the project's own rule ("when uncertain, do not guess"), we answer
# both with real data before building anything on top of an assumption.
#
# Usage: ./analyze-column-width.sh <app_id>
#   e.g. ./analyze-column-width.sh kitty
#
# Requires: niri, jq. Safe to run: only affects the ONE window matching
# app_id, only changes its width (restores nothing afterward -- reset it
# yourself with your usual keybind if you care about its size after).

set -euo pipefail

APP_ID="${1:-}"
if [[ -z "$APP_ID" ]]; then
    echo "usage: $0 <app_id>" >&2
    echo "example: $0 kitty" >&2
    exit 1
fi

echo "=== Step 0: raw output info (so we can adjust jq paths if this doesn't match expectations) ==="
niri msg --json outputs
echo

echo "=== Step 1: find the target window (app_id=$APP_ID) ==="
WINDOW_JSON="$(niri msg --json windows | jq -c --arg app "$APP_ID" '.[] | select(.app_id == $app)')"
if [[ -z "$WINDOW_JSON" ]]; then
    echo "no window found with app_id=$APP_ID -- open one first" >&2
    exit 1
fi
WINDOW_COUNT="$(echo "$WINDOW_JSON" | wc -l)"
if [[ "$WINDOW_COUNT" -gt 1 ]]; then
    echo "more than one window matches app_id=$APP_ID -- this script only handles a single unambiguous target, aborting" >&2
    exit 1
fi

WINDOW_ID="$(echo "$WINDOW_JSON" | jq -r '.id')"
WORKSPACE_ID="$(echo "$WINDOW_JSON" | jq -r '.workspace_id')"
echo "target window id=$WINDOW_ID (workspace_id=$WORKSPACE_ID)"
echo

echo "=== Step 2: find that workspace's output, and the output's logical width ==="
OUTPUT_NAME="$(niri msg --json workspaces | jq -r --argjson wsid "$WORKSPACE_ID" '.[] | select(.id == $wsid) | .output')"
if [[ -z "$OUTPUT_NAME" || "$OUTPUT_NAME" == "null" ]]; then
    echo "could not determine output for workspace_id=$WORKSPACE_ID" >&2
    exit 1
fi
echo "output: $OUTPUT_NAME"

# NOTE: the exact jq path below (.logical.width) is a GUESS based on common
# compositor IPC conventions -- Step 0's raw dump is printed specifically
# so you can correct this path if it's wrong, rather than the script
# silently computing garbage.
OUTPUT_WIDTH="$(niri msg --json outputs | jq -r --arg name "$OUTPUT_NAME" '.[$name].logical.width // empty')"
if [[ -z "$OUTPUT_WIDTH" ]]; then
    echo "could not read logical width for output $OUTPUT_NAME via .[\"$OUTPUT_NAME\"].logical.width" >&2
    echo "check Step 0's raw JSON above and adjust the jq path in this script" >&2
    exit 1
fi
echo "output logical width: ${OUTPUT_WIDTH}px"
echo

echo "=== Step 3: focus-targeting check ==="
echo "Focusing the TARGET window first (so we have a clean baseline)..."
niri msg action focus-window --id "$WINDOW_ID"
sleep 0.3

echo "Now focusing a DIFFERENT window (if one exists), then calling set-column-width WITHOUT re-focusing the target..."
OTHER_WINDOW_ID="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '[.[] | select(.id != $id)] | .[0].id // empty')"
if [[ -n "$OTHER_WINDOW_ID" ]]; then
    niri msg action focus-window --id "$OTHER_WINDOW_ID"
    sleep 0.3
    BEFORE_TARGET_WIDTH="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[0]')"
    BEFORE_OTHER_WIDTH="$(niri msg --json windows | jq -r --argjson id "$OTHER_WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[0]')"

    # Pick a target percentage that's guaranteed to differ noticeably from
    # whatever's already there -- otherwise, if the "other" window happens
    # to already be close to our chosen test value, a real change could be
    # invisible purely by coincidence (confirmed this nearly happened: an
    # earlier run had the other window already at ~49.5%, close enough to
    # a naive "set to 50%" test that no visible change would occur either
    # way, making the result impossible to interpret).
    OTHER_CURRENT_PCT="$(awk -v w="$BEFORE_OTHER_WIDTH" -v ow="$OUTPUT_WIDTH" 'BEGIN{printf "%.0f", w/ow*100}')"
    if [[ "$OTHER_CURRENT_PCT" -ge 50 ]]; then
        TEST_PCT="33.333"
    else
        TEST_PCT="66.667"
    fi
    echo "  other window is currently ~${OTHER_CURRENT_PCT}% -- testing with ${TEST_PCT}% to guarantee a visible difference either way"

    echo "  (other window is now focused, target is NOT focused)"
    echo "  attempting: niri msg action set-column-width ${TEST_PCT}%  (deliberately NOT passing any id)"
    niri msg action set-column-width "${TEST_PCT}%"
    sleep 0.3

    AFTER_TARGET_WIDTH="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[0]')"
    AFTER_OTHER_WIDTH="$(niri msg --json windows | jq -r --argjson id "$OTHER_WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[0]')"

    echo "  target window width:  before=${BEFORE_TARGET_WIDTH}px  after=${AFTER_TARGET_WIDTH}px"
    echo "  other window width:   before=${BEFORE_OTHER_WIDTH}px  after=${AFTER_OTHER_WIDTH}px"
    if [[ "$AFTER_OTHER_WIDTH" != "$BEFORE_OTHER_WIDTH" && "$AFTER_TARGET_WIDTH" == "$BEFORE_TARGET_WIDTH" ]]; then
        echo "  => CONFIRMED: set-column-width affects the FOCUSED window, not our intended target."
        echo "     Restore will need an explicit focus-window step before calling set-column-width."
    elif [[ "$AFTER_TARGET_WIDTH" != "$BEFORE_TARGET_WIDTH" ]]; then
        echo "  => UNEXPECTED: the target changed even though it wasn't focused. Re-examine this result carefully."
    else
        echo "  => STILL INCONCLUSIVE: neither width changed. Something else is going on -- check for an error above, or whether these two windows are even in the same column/workspace."
    fi
else
    echo "  (only one window open -- can't test the focus-targeting question this way; skipping)"
fi

echo
echo "Re-focusing target window before the percentage-accuracy test..."
niri msg action focus-window --id "$WINDOW_ID"
sleep 0.3
echo

echo "=== Step 4: percentage-accuracy test (does N% really mean N%?) ==="
for PCT in 33.333 50 66.667; do
    echo "--- requesting ${PCT}% ---"
    niri msg action set-column-width "${PCT}%"
    sleep 0.3

    OBSERVED_WIDTH="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.window_size[0]')"
    OBSERVED_TILE_WIDTH="$(niri msg --json windows | jq -r --argjson id "$WINDOW_ID" '.[] | select(.id == $id) | .layout.tile_size[0] // empty')"

    ACTUAL_PCT_WINDOW="$(awk -v w="$OBSERVED_WIDTH" -v ow="$OUTPUT_WIDTH" 'BEGIN{printf "%.2f", w/ow*100}')"
    echo "  requested: ${PCT}%"
    echo "  observed window_size width: ${OBSERVED_WIDTH}px  -> naive computed %: ${ACTUAL_PCT_WINDOW}%"
    if [[ -n "$OBSERVED_TILE_WIDTH" ]]; then
        ACTUAL_PCT_TILE="$(awk -v w="$OBSERVED_TILE_WIDTH" -v ow="$OUTPUT_WIDTH" 'BEGIN{printf "%.2f", w/ow*100}')"
        echo "  observed tile_size width:   ${OBSERVED_TILE_WIDTH}px  -> naive computed %: ${ACTUAL_PCT_TILE}%"
    fi
    echo
done

echo "=== Done. Compare 'requested' vs 'computed %' above for each preset. ==="
echo "If they match closely (within ~1%), the naive width/output_width formula is trustworthy."
echo "If there's a consistent offset, that tells us what niri's real formula accounts for that ours doesn't (e.g. gaps)."
