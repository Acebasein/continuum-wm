package restore

import (
	"context"
	"fmt"
	"time"

	"continuum-wm/internal/niri"
	"continuum-wm/internal/session"
)

// MergeColumnTopology attempts to reconstruct the saved column structure
// for entities that were captured sharing one column, but which restore
// necessarily placed into SEPARATE columns (each entity is placed
// independently via move-window-to-workspace, which has no way to know
// two entities were meant to share a column). Called BEFORE any width/
// height restoration -- per the researched restore-ordering principle:
// reconstruct topology FIRST, apply sizing SECOND, since niri
// redistributes a column's height whenever its membership changes;
// sizing before merging would just be invalidated by the merge itself.
//
// SAFETY, confirmed necessary via real testing: ConsumeWindowIntoColumn
// has NO targeting option at all -- it always consumes whatever window is
// immediately to the right of the FOCUSED column, regardless of intent.
// This function verifies, from LIVE data, that the next entity's window
// is genuinely adjacent (immediately to the right) of the anchor before
// ever calling it. If adjacency can't be confirmed -- e.g. an unrelated
// window ended up between them -- merging stops for this column at that
// exact point, leaving the remaining entities as separate columns (safe,
// matches the project's "prefer incomplete over incorrect" principle)
// rather than risk consuming the wrong window.
//
// results must be the same length and order as col.Entities (one Result
// per entity, from Reconcile). Entities that weren't successfully
// matched (NiriWindowID == 0) are simply skipped for merging purposes --
// merging stops entirely rather than trying to merge "around" a gap,
// since that could scramble the intended row order.
func MergeColumnTopology(ctx context.Context, client *niri.Client, col session.Column, results []Result) {
	if len(col.Entities) < 2 || len(results) != len(col.Entities) {
		return // nothing to merge, or an internal count mismatch -- don't guess
	}

	var anchorID uint64
	for _, r := range results {
		if r.NiriWindowID != 0 {
			anchorID = r.NiriWindowID
			break
		}
	}
	if anchorID == 0 {
		fmt.Println("  (skipping column merge: no successfully matched window to anchor the column)")
		return
	}

	for i := 1; i < len(results); i++ {
		nextID := results[i].NiriWindowID
		if nextID == 0 {
			fmt.Printf("  (stopping column merge: entity %s was not successfully matched)\n", col.Entities[i].PersistentID)
			return
		}

		anchorCol, anchorOK := columnIndexOf(ctx, client, anchorID)
		nextCol, nextOK := columnIndexOf(ctx, client, nextID)
		if !anchorOK || !nextOK {
			fmt.Println("  (stopping column merge: could not read live layout position for a required window)")
			return
		}

		// FIXED BUG, confirmed via real testing under the daemon: if both
		// windows were ALREADY_PRESENT from a previous successful merge
		// (a prior run, or an earlier restore in the same session), they
		// can already be in the SAME column -- nextCol == anchorCol, not
		// anchorCol+1. The original check only accepted "immediately to
		// the right," treating "already merged" as a failure and
		// incorrectly aborting further merges in this column. Treat
		// already-equal as success (nothing to do) and continue.
		if nextCol == anchorCol {
			fmt.Printf("  entity %s is already in the anchor column (window id=%d) -- nothing to merge\n", col.Entities[i].PersistentID, nextID)
			continue
		}
		if nextCol != anchorCol+1 {
			fmt.Printf("  (stopping column merge: entity %s is not immediately adjacent to the anchor column (anchor at %d, entity at %d) -- leaving remaining entities as separate columns)\n",
				col.Entities[i].PersistentID, anchorCol, nextCol)
			return
		}

		if err := client.FocusWindow(ctx, anchorID); err != nil {
			fmt.Printf("  (column merge failed: could not focus anchor window id=%d: %v)\n", anchorID, err)
			return
		}
		if err := client.ConsumeWindowIntoColumn(ctx); err != nil {
			fmt.Printf("  (column merge failed: consume-window-into-column errored: %v)\n", err)
			return
		}

		// Verify the merge actually happened, rather than assuming
		// success just because the command didn't error.
		newAnchorCol, ok1 := columnIndexOf(ctx, client, anchorID)
		newNextCol, ok2 := columnIndexOf(ctx, client, nextID)
		if ok1 && ok2 && newAnchorCol == newNextCol {
			fmt.Printf("  merged entity %s into the column (window id=%d)\n", col.Entities[i].PersistentID, nextID)
		} else {
			fmt.Printf("  (column merge for entity %s: could not verify success -- stopping further merges for this column)\n", col.Entities[i].PersistentID)
			return
		}
	}
}

// columnIndexOf returns the live column index (pos_in_scrolling_layout[0])
// for the window with the given id, if determinable.
func columnIndexOf(ctx context.Context, client *niri.Client, windowID uint64) (uint64, bool) {
	wins, err := client.Windows(ctx)
	if err != nil {
		return 0, false
	}
	for _, w := range wins {
		if w.ID == windowID {
			if w.Layout != nil && w.Layout.PosInScrollingLayout != nil {
				return w.Layout.PosInScrollingLayout[0], true
			}
			return 0, false
		}
	}
	return 0, false
}

// mergeWidthSettleToleranceLogicalPx: how far apart (in logical pixels)
// two windows' tile_size[0] in the same column are allowed to be and
// still be considered "settled" for merge-geometry purposes. Column
// width is SHARED across every window in a column, so once niri has
// truly finished reflowing a merge, matched windows should read back
// (near-)identical widths -- this isn't the same tolerance as
// layoutCorrectionTolerance (which is a fraction of output width, used
// to decide whether a correction request is worth issuing); this one is
// just checking "have these two numbers converged with each other yet",
// so a small fixed pixel epsilon is more appropriate than a fraction.
const mergeWidthSettleToleranceLogicalPx = 1.0

// waitForMergedColumnSettle actively polls until every successfully-
// matched entity in col reports BOTH the same live column index AND the
// same tile_size width -- i.e. the merge has genuinely taken effect
// geometrically, not just topologically -- rather than trusting a fixed
// delay. See ApplyColumnLayout's settle-delay comment for the full
// reasoning on why this exists specifically for multi-entity columns.
//
// CONFIRMED via real testing: column-index equality alone is not
// sufficient -- a merge can report matching column indices while the
// FIRST entity's tile_size[0] still reads back niri's pre-merge default
// (e.g. an even 2-column split) for one more poll cycle before the
// actual width reflow catches up. Checking both conditions closes that
// gap.
//
// Bounded, never blocks indefinitely: gives up and proceeds anyway after
// maxMergeSettleWait, same as the old fixed-delay behavior would have --
// this is never WORSE than the previous approach on a slow/stuck case,
// just adaptive (and typically faster) on a normal one.
func waitForMergedColumnSettle(ctx context.Context, client *niri.Client, col session.Column, results []Result) {
	if len(col.Entities) < 2 {
		return // nothing merged, nothing to verify
	}

	var matchedIDs []uint64
	for _, r := range results {
		if r.NiriWindowID != 0 {
			matchedIDs = append(matchedIDs, r.NiriWindowID)
		}
	}
	if len(matchedIDs) < 2 {
		return // fewer than 2 real windows -- no merge geometry to verify
	}

	const pollInterval = 150 * time.Millisecond
	const maxMergeSettleWait = 1500 * time.Millisecond
	deadline := time.Now().Add(maxMergeSettleWait)

	for {
		// One client.Windows() call per poll, checked against every
		// matched ID -- deliberately not calling columnIndexOf per ID,
		// since that would re-fetch the full window list once per ID per
		// poll for no benefit (client.Windows() already returns
		// everything in one call).
		wins, err := client.Windows(ctx)
		if err == nil {
			colByID := make(map[uint64]uint64, len(matchedIDs))
			widthByID := make(map[uint64]float64, len(matchedIDs))
			for _, w := range wins {
				if w.Layout != nil && w.Layout.PosInScrollingLayout != nil {
					colByID[w.ID] = w.Layout.PosInScrollingLayout[0]
					widthByID[w.ID] = w.Layout.TileSize[0]
				}
			}
			allMatch := true
			var firstCol uint64
			var firstWidth float64
			firstSet := false
			for _, id := range matchedIDs {
				idx, idxOK := colByID[id]
				width, widthOK := widthByID[id]
				if !idxOK || !widthOK {
					allMatch = false
					break
				}
				if !firstSet {
					firstCol = idx
					firstWidth = width
					firstSet = true
					continue
				}
				if idx != firstCol {
					allMatch = false
					break
				}
				if absFloat(width-firstWidth) > mergeWidthSettleToleranceLogicalPx {
					allMatch = false
					break
				}
			}
			if allMatch {
				return // converged -- both column index and width now reflect the merge
			}
		}
		if time.Now().After(deadline) {
			fmt.Println("  (merged column did not fully settle within the wait budget -- proceeding with sizing anyway)")
			return
		}
		time.Sleep(pollInterval)
	}
}

// ApplyColumnLayout applies Phase 7 layout restoration -- column width
// (once per column) and per-entity height -- AFTER every entity in the
// column has already been reconciled/restored via Reconcile, AND after
// MergeColumnTopology has reconstructed the correct column membership.
//
// outputHint is used to look up the output's logical dimensions, needed
// for the measure-and-correct loop below -- see applyWidthWithCorrection/
// applyHeightWithCorrection.
//
// results must be the same length and in the same order as
// col.Entities -- one Result per entity, from calling Reconcile on each
// in turn. This is deliberately a separate pass from Reconcile/Entity,
// not folded into them: layout restoration needs to know about every
// entity in the column at once (to find one successfully-matched window
// to focus for width), which a single-entity function can't see.
//
// WIDTH: confirmed via real testing that niri's set-column-width has NO
// window/column targeting option at all (unlike set-window-height, which
// has --id) -- it always acts on whichever column currently has focus.
// So this temporarily focuses a successfully-matched window in the
// column, applies the width, then restores whatever was focused
// beforehand, to avoid leaving the user's visible focus somewhere they
// didn't put it -- consistent with this project's practice everywhere
// else of never stealing focus without putting it back.
//
// HEIGHT: confirmed via real testing that set-window-height's --id
// genuinely targets the specified window independent of focus, so no
// focus-stealing is needed for this part at all.
//
// CALIBRATION (re-enabled after being disabled due to a confirmed
// regression): confirmed via real testing that niri's set-column-width/
// set-window-height UNDERSHOOT the requested percentage by a small,
// consistent amount every time they're called. Since capture also
// measures real tile_size to derive its saved fraction, requesting that
// SAVED fraction again at restore applies the same undershoot a SECOND
// time, compounding into a visibly wrong result (confirmed: every
// restored window showed a large gap at the bottom). Rather than
// guessing a fixed correction factor (confirmed NOT to hold consistently
// across different outputs/scale factors), each request is followed by
// measuring the ACTUAL resulting tile_size and, if it's off from the
// target by more than a small tolerance, issuing exactly ONE corrective
// request scaled by the observed error -- capped at one correction, same
// "don't retry indefinitely" discipline used elsewhere in this project.
func ApplyColumnLayout(ctx context.Context, client *niri.Client, col session.Column, results []Result, outputHint string) {
	if len(results) != len(col.Entities) {
		fmt.Println("  (skipping layout restoration: entity/result count mismatch -- internal error, not attempting to guess)")
		return
	}

	var outputWidth, outputHeight float64
	if outputHint != "" {
		if outputs, err := client.Outputs(ctx); err == nil {
			if out, ok := outputs[outputHint]; ok && out.Logical != nil {
				outputWidth = float64(out.Logical.Width)
				outputHeight = float64(out.Logical.Height)
			}
		}
	}

	// Settle delay before touching layout at all. CONFIRMED via real
	// testing: for a SOLO-entity column (nothing to merge), a fixed brief
	// delay is enough -- this is about a freshly-launched, slow-starting
	// app's window not having fully settled into its final shape yet.
	//
	// For a MULTI-entity column, a fixed delay is NOT reliable enough:
	// CONFIRMED via real testing that MergeColumnTopology's own success
	// check (column INDEX equality) can become true before the actual
	// GEOMETRIC reflow (width/height redistribution between the merged
	// windows) has caught up -- confirmed directly: a merged column's
	// width/height measurements matched niri's PRE-merge defaults (an
	// even 2-column split for width, a solo full-height ceiling for
	// height) rather than genuinely post-merge values, even though the
	// merge's own topology check had already reported success moments
	// earlier. So multi-entity columns get an ADAPTIVE wait instead,
	// actively polling until geometry actually reflects the merge -- see
	// waitForMergedColumnSettle.
	if len(col.Entities) > 1 {
		waitForMergedColumnSettle(ctx, client, col, results)
	} else {
		const preLayoutSettleDelay = 300 * time.Millisecond
		time.Sleep(preLayoutSettleDelay)
	}

	// --- WIDTH (once per column) ---
	if col.WidthFraction != nil {
		var referenceWindowID uint64
		for _, r := range results {
			if r.NiriWindowID != 0 {
				referenceWindowID = r.NiriWindowID
				break
			}
		}
		if referenceWindowID == 0 {
			fmt.Println("  (skipping width restoration: no successfully matched window in this column)")
		} else {
			previousFocusID, havePrevious := findFocusedWindow(ctx, client)

			if err := client.FocusWindow(ctx, referenceWindowID); err != nil {
				fmt.Printf("  (width restoration: could not focus window id=%d: %v)\n", referenceWindowID, err)
			} else {
				applyWidthWithCorrection(ctx, client, referenceWindowID, *col.WidthFraction, outputWidth)

				if havePrevious && previousFocusID != referenceWindowID {
					if err := client.FocusWindow(ctx, previousFocusID); err != nil {
						fmt.Printf("  (could not restore previous focus to window id=%d: %v)\n", previousFocusID, err)
					}
				}
			}
		}
	}

	// --- HEIGHT (per entity, no focus needed) ---
	//
	// CONFIRMED VIA REAL TESTING: a SOLO window in a column cannot be
	// resized shorter than its natural full-column height at all --
	// height is fundamentally about dividing a column's space between
	// MULTIPLE occupants, and with nothing else in the column to receive
	// the "freed" space, niri simply keeps a lone window at its natural
	// maximum regardless of what's requested. Confirmed uniformly across
	// four different solo-column entities in the same test run, all
	// stuck at an identical ~95.2-95.4% ceiling no matter what lower
	// value was requested -- while the SAME correction logic worked
	// correctly for every column with 2+ entities in that same run. So:
	// only attempt height restoration when this column genuinely has
	// more than one entity to divide space between.
	if len(col.Entities) > 1 {
		for i, ent := range col.Entities {
			r := results[i]
			if r.NiriWindowID == 0 || ent.HeightFraction == nil {
				continue
			}
			applyHeightWithCorrection(ctx, client, r.NiriWindowID, *ent.HeightFraction, outputHeight)
		}
	}
}

// layoutCorrectionTolerance: how far (as a fraction of the output
// dimension) the achieved result is allowed to differ from the target
// before we bother issuing a corrective request. A first guess, not
// empirically tuned -- similar in spirit to settleWindow -- worth
// revisiting if it turns out too tight (correcting on noise) or too loose
// (leaving a visible gap uncorrected).
const layoutCorrectionTolerance = 0.01

func applyWidthWithCorrection(ctx context.Context, client *niri.Client, windowID uint64, targetFraction float64, outputWidth float64) {
	if outputWidth <= 0 {
		fmt.Println("  (skipping width restoration: unknown output width, cannot calibrate)")
		return
	}

	// Brief delay between issuing the resize and measuring its result.
	// CONFIRMED VIA REAL TESTING: a resize can appear "stuck" in our own
	// measurement (reading back the OLD size) even though the true, final
	// result is correct moments later -- Wayland resizes aren't
	// instantaneous, the client has to acknowledge and redraw at the new
	// size, and a heavy/slow app (Firefox, Chrome) can take a moment to
	// do so. Confirmed directly: a trace reported Firefox "stuck" at 50%
	// width across two attempts, yet a raw niri query taken moments later
	// showed it at the CORRECT, full width all along -- our own
	// measurement was just too fast, not the resize itself failing.
	const postResizeSettleDelay = 200 * time.Millisecond

	request := targetFraction
	for attempt := 1; attempt <= 2; attempt++ {
		change := fmt.Sprintf("%.4f%%", request*100)
		if err := client.SetColumnWidth(ctx, change); err != nil {
			fmt.Printf("  (width restoration failed via window id=%d: %v)\n", windowID, err)
			return
		}
		time.Sleep(postResizeSettleDelay)

		actualWidth, ok := measuredTileSize(ctx, client, windowID, 0)
		if !ok {
			fmt.Printf("  restored column width to %s (via window id=%d) -- could not verify actual result\n", change, windowID)
			return
		}
		achievedFraction := actualWidth / outputWidth
		errFraction := targetFraction - achievedFraction

		fmt.Printf("  width: requested %.4f%%, achieved %.4f%% (via window id=%d)\n", request*100, achievedFraction*100, windowID)

		if attempt == 2 || absFloat(errFraction) <= layoutCorrectionTolerance || achievedFraction <= 0 {
			return // done -- either converged, or already used our one correction
		}

		// ONE corrective adjustment, scaled by the observed ratio between
		// what we asked for and what we actually got -- not a fixed,
		// guessed constant, since that's confirmed not to hold
		// consistently across different outputs/scale factors.
		//
		// CLAMPED, confirmed necessary via real testing: when achieved is
		// far enough below target (roughly less than half), the raw
		// formula can produce a request over 100% -- meaningless as a
		// column-width proportion. Confirmed this actually happened and
		// silently no-op'd (niri appears to ignore/clamp an out-of-range
		// request rather than erroring), leaving the correction with zero
		// effect. Clamp to a sane (0, 1] range before issuing it.
		request = targetFraction * (targetFraction / achievedFraction)
		if request > 1.0 {
			request = 1.0
		} else if request <= 0 {
			request = targetFraction // fall back to the original target rather than a nonsensical value
		}
		fmt.Printf("  (width off by %.4f%%, applying one corrective request: %.4f%%)\n", errFraction*100, request*100)
	}
}

func applyHeightWithCorrection(ctx context.Context, client *niri.Client, windowID uint64, targetFraction float64, outputHeight float64) {
	if outputHeight <= 0 {
		fmt.Println("  (skipping height restoration: unknown output height, cannot calibrate)")
		return
	}

	request := targetFraction
	for attempt := 1; attempt <= 2; attempt++ {
		change := fmt.Sprintf("%.4f%%", request*100)
		if err := client.SetWindowHeight(ctx, windowID, change); err != nil {
			fmt.Printf("  (height restoration failed for window id=%d: %v)\n", windowID, err)
			return
		}
		time.Sleep(200 * time.Millisecond)

		actualHeight, ok := measuredTileSize(ctx, client, windowID, 1)
		if !ok {
			fmt.Printf("  restored height to %s for window id=%d -- could not verify actual result\n", change, windowID)
			return
		}
		achievedFraction := actualHeight / outputHeight
		errFraction := targetFraction - achievedFraction

		fmt.Printf("  height: requested %.4f%%, achieved %.4f%% (window id=%d)\n", request*100, achievedFraction*100, windowID)

		if attempt == 2 || absFloat(errFraction) <= layoutCorrectionTolerance || achievedFraction <= 0 {
			return
		}

		request = targetFraction * (targetFraction / achievedFraction)
		if request > 1.0 {
			request = 1.0
		} else if request <= 0 {
			request = targetFraction
		}
		fmt.Printf("  (height off by %.4f%%, applying one corrective request: %.4f%%)\n", errFraction*100, request*100)
	}
}

// measuredTileSize reads back windowID's current tile_size[dim] (0 for
// width, 1 for height) from live niri state -- the actual measurement the
// correction loops above are built around.
func measuredTileSize(ctx context.Context, client *niri.Client, windowID uint64, dim int) (float64, bool) {
	wins, err := client.Windows(ctx)
	if err != nil {
		return 0, false
	}
	for _, w := range wins {
		if w.ID == windowID && w.Layout != nil {
			return w.Layout.TileSize[dim], true
		}
	}
	return 0, false
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

// findFocusedWindow returns the currently-focused window's id, if any.
func findFocusedWindow(ctx context.Context, client *niri.Client) (uint64, bool) {
	wins, err := client.Windows(ctx)
	if err != nil {
		return 0, false
	}
	for _, w := range wins {
		if w.IsFocused {
			return w.ID, true
		}
	}
	return 0, false
}
