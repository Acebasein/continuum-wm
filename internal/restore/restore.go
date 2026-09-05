// Package restore implements the per-entity restore state machine described
// in the design doc, Part 8.
//
// Phase 3 scope: ONE saved entity, matched via app_id + baseline-diffing
// (a newly appeared window, since we launched it, with the right app_id).
//
// Phase 4 addition: when a saved entity has a high-confidence CWD (Part 6),
// we cross-check the matched window's actual CWD before trusting the match
// (see the CWD VERIFICATION step below) -- this is Part 7's CWD signal,
// used as a post-match sanity check rather than full a-priori scoring.
//
// Known remaining gap, not yet handled: if TWO windows of the same app_id
// appear within the observation window (e.g. the user manually opens a
// second instance during a restore, or a race with another in-flight
// restore of the same app), this code still takes the first one it sees
// rather than detecting the ambiguity and reporting AMBIGUOUS. Worth
// revisiting once we have a concrete scenario to test it against, per the
// project's general principle of not building speculative complexity
// ahead of a real test case.
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
	StateLaunching          State = "LAUNCHING"
	StateObserving          State = "OBSERVING"
	StateMatched            State = "MATCHED"
	StatePlacing            State = "PLACING"
	StateVerifying          State = "VERIFYING"
	StateRestored           State = "RESTORED"
	StateFailed             State = "FAILED"
	StatePartiallyRestored  State = "PARTIALLY_RESTORED"
)

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
	// Phase 3 matching rule (deliberately simple -- see package doc):
	// the first WindowOpenedOrChanged event whose id was NOT in our
	// baseline, and whose app_id matches, is our match. No scoring, no
	// ambiguity handling -- those require multiple candidates to exist,
	// which Phase 3's test setup guarantees will not happen.
	var matchedID uint64
	matchLoop:
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return Result{State: StateFailed, Reason: "event stream closed before a matching window appeared"}
			}
			if ev.Kind == niri.EventWindowOpenedOrChanged {
				w := ev.WindowOpenedOrChanged.Window
				if existingIDs[w.ID] {
					continue // pre-existing window, not our launch
				}
				if w.AppID == nil || *w.AppID != ent.AppID {
					continue // some other app opened a window at the same time
				}
				matchedID = w.ID
				break matchLoop
			}

		case perr, ok := <-errs:
			if ok {
				// A parse error on one line isn't fatal to the whole
				// observation -- keep waiting, consistent with Phase 1's
				// "don't crash on one bad line" design.
				fmt.Printf("  (non-fatal parse error while observing: %v)\n", perr)
			}

		case <-obsCtx.Done():
			return Result{State: StateFailed, Reason: fmt.Sprintf("timed out after %s waiting for app_id=%q to appear", timeout, ent.AppID)}
		}
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
