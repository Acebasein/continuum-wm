// Package restore implements the per-entity restore state machine described
// in the design doc, Part 8.
//
// Phase 3 scope: ONE saved entity, matched via app_id + baseline-diffing
// (a newly appeared window, since we launched it, with the right app_id).
//
// Phase 4 additions:
//   - a brief "settle window" after the first candidate window appears,
//     to detect a race where a second window of the same app_id shows up
//     too (see settleWindow) -- multiple candidates are disambiguated via
//     saved CWD when possible, and reported as AMBIGUOUS otherwise, never
//     guessed.
//   - when a saved entity has a high-confidence CWD (Part 6), we
//     cross-check the matched window's actual CWD before trusting the
//     match, as a secondary safety net beyond the disambiguation above.
package restore

import (
	"context"
	"fmt"
	"time"

	"continuum-wm/internal/niri"
	"continuum-wm/internal/session"
)

// State is a restore outcome or in-progress stage, matching the design
// doc's state machine names where a Phase 3 equivalent exists.
type State string

const (
	StateLaunching         State = "LAUNCHING"
	StateObserving         State = "OBSERVING"
	StateMatched           State = "MATCHED"
	StatePlacing           State = "PLACING"
	StateVerifying         State = "VERIFYING"
	StateRestored          State = "RESTORED"
	StateFailed            State = "FAILED"
	StatePartiallyRestored State = "PARTIALLY_RESTORED"

	// StateAmbiguous: more than one plausible new window appeared for this
	// entity, and we could not confidently pick one -- see the settle
	// window / disambiguation logic in Entity. Per the project's core
	// principle, we never guess here: nothing is touched or placed.
	StateAmbiguous State = "AMBIGUOUS"
)

// settleWindow is how long we keep watching for ADDITIONAL candidate
// windows after the first one appears, before deciding whether we have a
// clean single match or a genuine ambiguity to resolve/report. This value
// is a first guess, not something we've validated empirically yet -- see
// design doc Part 16 risk notes; revisit if real testing shows it's too
// short (a legitimately slow second window) or too long (unnecessarily
// delaying every restore).
const settleWindow = 750 * time.Millisecond

// Result describes the final outcome of a restore attempt, plus enough
// detail to explain why, per the design doc's emphasis on visible,
// debuggable failure reasons rather than a silent pass/fail.
type Result struct {
	State        State
	Reason       string
	NiriWindowID uint64 // 0 if we never matched a window
}

// Entity attempts to restore a single saved entity, using workspaceIdxHint
// (the workspace's captured niri index) as the placement target.
//
// This function deliberately does the state transitions in a strict,
// visible sequence -- each step is logged as it happens (via the returned
// Result at each stage isn't streamed today; Phase 9 will add proper
// structured logging. For now, callers should print progress themselves,
// as continuum-cli's restore command does).
func Entity(ctx context.Context, client *niri.Client, ent session.Entity, workspaceIdxHint uint8, timeout time.Duration) Result {
	if len(ent.Launch.Command) == 0 {
		return Result{State: StateFailed, Reason: "no launch command was captured for this entity"}
	}
	if ent.AppID == "" {
		return Result{State: StateFailed, Reason: "entity has no app_id -- refusing to guess how to match it"}
	}

	// --- Establish a baseline BEFORE launching, so we can tell a newly
	// opened window apart from one that already existed. This directly
	// implements the design doc's "sequential/transactional restoration"
	// principle (Part 13): one entity at a time, launch-then-observe, not
	// launch-everything-and-sort-it-out-after.
	baseline, err := client.Windows(ctx)
	if err != nil {
		return Result{State: StateFailed, Reason: fmt.Sprintf("could not read baseline windows: %v", err)}
	}
	existingIDs := make(map[uint64]bool, len(baseline))
	for _, w := range baseline {
		existingIDs[w.ID] = true
	}

	// --- LAUNCHING ---
	// If we have a saved CWD for this entity, pass it through explicitly
	// (see niri.LaunchDetached) rather than letting the new process
	// inherit continuum-cli's own directory -- confirmed experimentally
	// that without this, every restored terminal silently opened wherever
	// continuum-cli itself was run from, regardless of each entity's
	// actual saved directory. The CWD verification step further below
	// remains as a secondary safety net (e.g. for entities where this
	// isn't applicable, or to catch a genuine matching error), not the
	// primary mechanism for getting the directory right.
	obsCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	events, errs, err := client.EventStream(obsCtx)
	if err != nil {
		return Result{State: StateFailed, Reason: fmt.Sprintf("could not start event stream: %v", err)}
	}

	if _, err := niri.LaunchDetached(ent.Launch.Command, ent.ProviderMetadata.CWD); err != nil {
		return Result{State: StateFailed, Reason: fmt.Sprintf("launch failed: %v", err)}
	}

	// --- OBSERVING / MATCHED ---
	// Phase 4: rather than committing to the FIRST qualifying window we
	// see (Phase 3's behavior, which is vulnerable to a race -- e.g. the
	// user manually opens a second window of the same app_id during our
	// observation window), we wait for a brief settle period after the
	// first candidate appears, collecting any others that show up too.
	// This is what makes ambiguity detectable at all: without it, a race
	// is indistinguishable from the normal case.
	var candidates []uint64
	seen := make(map[uint64]bool)

	collectCandidate := func(w niri.Window) {
		if existingIDs[w.ID] || seen[w.ID] {
			return // pre-existing window, or already recorded
		}
		if w.AppID == nil || *w.AppID != ent.AppID {
			return // some other app opened a window at the same time
		}
		seen[w.ID] = true
		candidates = append(candidates, w.ID)
	}

	// Phase A: wait (up to the full timeout) for the FIRST candidate.
waitForFirst:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return Result{State: StateFailed, Reason: "event stream closed before a matching window appeared"}
			}
			if ev.Kind == niri.EventWindowOpenedOrChanged {
				collectCandidate(ev.WindowOpenedOrChanged.Window)
				if len(candidates) > 0 {
					break waitForFirst
				}
			}

		case perr, ok := <-errs:
			if ok {
				fmt.Printf("  (non-fatal parse error while observing: %v)\n", perr)
			}

		case <-obsCtx.Done():
			return Result{State: StateFailed, Reason: fmt.Sprintf("timed out after %s waiting for app_id=%q to appear", timeout, ent.AppID)}
		}
	}

	// Phase B: having seen one candidate, keep watching briefly for any
	// others, so a race is detected rather than silently resolved by
	// whichever window happened to be first.
	settleTimer := time.NewTimer(settleWindow)
	defer settleTimer.Stop()
settleLoop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				break settleLoop
			}
			if ev.Kind == niri.EventWindowOpenedOrChanged {
				collectCandidate(ev.WindowOpenedOrChanged.Window)
			}

		case perr, ok := <-errs:
			if ok {
				fmt.Printf("  (non-fatal parse error while settling: %v)\n", perr)
			}

		case <-settleTimer.C:
			break settleLoop

		case <-obsCtx.Done():
			break settleLoop
		}
	}

	var matchedID uint64
	switch len(candidates) {
	case 0:
		// Shouldn't happen (Phase A guarantees at least one), but handle
		// defensively rather than silently proceeding with a zero-value ID.
		return Result{State: StateFailed, Reason: "internal error: no candidates recorded despite exiting the wait phase"}

	case 1:
		matchedID = candidates[0]
		fmt.Printf("  observed 1 candidate window (id=%d) -- no ambiguity to resolve\n", matchedID)

	default:
		fmt.Printf("  observed %d candidate windows with app_id=%q: %v -- attempting to disambiguate\n", len(candidates), ent.AppID, candidates)
		// More than one candidate -- attempt CWD-based disambiguation if
		// we have a trustworthy saved CWD to check against. This is Part
		// 7's CWD signal used as an ACTIVE disambiguator, not just a
		// post-match sanity check.
		if ent.ProviderMetadata.CWDConfidence != session.CWDHigh || !session.IsKnownTerminal(ent.AppID) {
			return Result{
				State:  StateAmbiguous,
				Reason: fmt.Sprintf("%d windows with app_id=%q appeared, and no reliable saved CWD is available to disambiguate them -- refusing to guess", len(candidates), ent.AppID),
			}
		}

		var cwdMatches []uint64
		for _, id := range candidates {
			pid, ok := findWindowPID(ctx, client, id)
			if !ok {
				continue
			}
			cwd, confidence := session.ResolveTerminalCWD(ent.AppID, pid)
			if confidence == session.CWDHigh && cwd == ent.ProviderMetadata.CWD {
				cwdMatches = append(cwdMatches, id)
			}
		}

		if len(cwdMatches) != 1 {
			return Result{
				State: StateAmbiguous,
				Reason: fmt.Sprintf(
					"%d windows with app_id=%q appeared; %d of them had a cwd matching the saved value %q -- refusing to guess unless exactly one matches",
					len(candidates), ent.AppID, len(cwdMatches), ent.ProviderMetadata.CWD,
				),
			}
		}

		matchedID = cwdMatches[0]
		fmt.Printf("  disambiguated by cwd: window id=%d matched saved cwd %q\n", matchedID, ent.ProviderMetadata.CWD)
	}

	// --- CWD VERIFICATION (Phase 4 addition) ---
	// If we have a trustworthy saved CWD for this entity (Part 7's
	// strongest disambiguation signal), cross-check it against the
	// newly matched window's actual CWD before proceeding. This is what
	// lets us catch a bad match (e.g. a relaunch race where a second,
	// unrelated window of the same app_id appeared first) instead of
	// blindly placing whatever the baseline-diff happened to find.
	//
	// If we can't determine the candidate's CWD (its shell may not have
	// been assigned/settled yet, or it isn't a recognized terminal), we
	// proceed anyway -- app_id + baseline-diffing is still real evidence
	// on its own; the CWD check is a bonus verification when available,
	// not a requirement to proceed.
	if ent.ProviderMetadata.CWDConfidence == session.CWDHigh && session.IsKnownTerminal(ent.AppID) {
		if pid, ok := findWindowPID(ctx, client, matchedID); ok {
			candidateCWD, candidateConfidence := session.ResolveTerminalCWD(ent.AppID, pid)
			if candidateConfidence == session.CWDHigh && candidateCWD != ent.ProviderMetadata.CWD {
				return Result{
					State: StateFailed,
					Reason: fmt.Sprintf(
						"matched window (id=%d) has cwd %q, which does not match saved cwd %q -- refusing to place (likely a relaunch race with another instance of the same app)",
						matchedID, candidateCWD, ent.ProviderMetadata.CWD,
					),
					NiriWindowID: matchedID,
				}
			}
		}
	}

	// --- PLACING ---
	reference := fmt.Sprintf("%d", workspaceIdxHint)
	if err := client.MoveWindowToWorkspace(ctx, matchedID, reference, false); err != nil {
		return Result{
			State:        StatePartiallyRestored,
			Reason:       fmt.Sprintf("window matched (id=%d) but placement failed: %v", matchedID, err),
			NiriWindowID: matchedID,
		}
	}

	// --- VERIFYING ---
	// Re-query live state and confirm the window ended up on a workspace
	// whose idx matches what we asked for. We check idx rather than
	// tracking a specific workspace id, because "reference" addressing is
	// idx-based (see design doc caveat on IdxHint above).
	liveWorkspaces, err := client.Workspaces(ctx)
	if err != nil {
		return Result{
			State:        StatePartiallyRestored,
			Reason:       fmt.Sprintf("window placed (id=%d) but could not verify: %v", matchedID, err),
			NiriWindowID: matchedID,
		}
	}
	idxByWorkspaceID := make(map[uint64]uint8, len(liveWorkspaces))
	for _, w := range liveWorkspaces {
		idxByWorkspaceID[w.ID] = w.Idx
	}

	liveWindows, err := client.Windows(ctx)
	if err != nil {
		return Result{
			State:        StatePartiallyRestored,
			Reason:       fmt.Sprintf("window placed (id=%d) but could not verify: %v", matchedID, err),
			NiriWindowID: matchedID,
		}
	}
	for _, w := range liveWindows {
		if w.ID != matchedID {
			continue
		}
		if w.WorkspaceID != nil && idxByWorkspaceID[*w.WorkspaceID] == workspaceIdxHint {
			return Result{State: StateRestored, NiriWindowID: matchedID}
		}
		return Result{
			State:        StatePartiallyRestored,
			Reason:       "window exists but is not on the expected workspace",
			NiriWindowID: matchedID,
		}
	}

	return Result{
		State:        StatePartiallyRestored,
		Reason:       "window matched and placement command succeeded, but the window could not be found on re-query",
		NiriWindowID: matchedID,
	}
}

// findWindowPID looks up a live window's PID by its niri window id.
// Returns ok=false if the window can't be found or has no PID reported.
func findWindowPID(ctx context.Context, client *niri.Client, windowID uint64) (int32, bool) {
	windows, err := client.Windows(ctx)
	if err != nil {
		return 0, false
	}
	for _, w := range windows {
		if w.ID == windowID && w.PID != nil {
			return *w.PID, true
		}
	}
	return 0, false
}
