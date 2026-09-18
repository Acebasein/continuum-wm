// continuum-cli is Continuum-WM's command-line tool. Phase 2 gives it two
// subcommands, both read-only with respect to the compositor:
//
//	continuum-cli capture <path>   snapshot the live niri session to a YAML file
//	continuum-cli show <path>      load a YAML session file and print it
//
// Neither subcommand launches, moves, or closes anything. Capture only
// reads from niri; show only reads from disk.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"continuum-wm/internal/monitor"
	"continuum-wm/internal/niri"
	"continuum-wm/internal/restore"
	"continuum-wm/internal/session"
	"continuum-wm/internal/tui"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmd := os.Args[1]

	// profile-settings takes no path argument, unlike every other
	// command below -- handled separately, before the generic
	// len(os.Args) < 3 check that every path-taking command relies on.
	if cmd == "profile-settings" {
		if err := runProfileSettings(); err != nil {
			fmt.Fprintf(os.Stderr, "profile-settings failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if len(os.Args) < 3 {
		usage()
		os.Exit(1)
	}
	path := os.Args[2]

	switch cmd {
	case "capture":
		if err := runCapture(path); err != nil {
			fmt.Fprintf(os.Stderr, "capture failed: %v\n", err)
			os.Exit(1)
		}
	case "show":
		if err := runShow(path); err != nil {
			fmt.Fprintf(os.Stderr, "show failed: %v\n", err)
			os.Exit(1)
		}
	case "restore":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: continuum-cli restore <path-to-session.yaml> <entity-persistent-id>")
			os.Exit(1)
		}
		entityID := os.Args[3]
		if err := runRestore(path, entityID); err != nil {
			fmt.Fprintf(os.Stderr, "restore failed: %v\n", err)
			os.Exit(1)
		}
	case "restore-all":
		if err := runRestoreAll(path); err != nil {
			fmt.Fprintf(os.Stderr, "restore-all failed: %v\n", err)
			os.Exit(1)
		}
	case "set-favorite":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "usage: continuum-cli set-favorite <path-to-session.yaml> <workspace-persistent-id>")
			os.Exit(1)
		}
		wsID := os.Args[3]
		if err := runSetFavorite(path, wsID); err != nil {
			fmt.Fprintf(os.Stderr, "set-favorite failed: %v\n", err)
			os.Exit(1)
		}
	case "lazy-restore":
		if err := runLazyRestore(path); err != nil {
			fmt.Fprintf(os.Stderr, "lazy-restore failed: %v\n", err)
			os.Exit(1)
		}
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage:")
	fmt.Fprintln(os.Stderr, "  continuum-cli capture <path-to-write.yaml>")
	fmt.Fprintln(os.Stderr, "  continuum-cli show <path-to-read.yaml>")
	fmt.Fprintln(os.Stderr, "  continuum-cli restore <path-to-read.yaml> <entity-persistent-id>")
	fmt.Fprintln(os.Stderr, "  continuum-cli restore-all <path-to-read.yaml>")
	fmt.Fprintln(os.Stderr, "  continuum-cli set-favorite <path-to-read.yaml> <workspace-persistent-id>")
	fmt.Fprintln(os.Stderr, "  continuum-cli lazy-restore <path-to-read.yaml>")
	fmt.Fprintln(os.Stderr, "  continuum-cli profile-settings")
}

func runProfileSettings() error {
	m, err := tui.NewModel()
	if err != nil {
		return err
	}
	p := tea.NewProgram(m)
	_, err = p.Run()
	return err
}

func runCapture(path string) error {
	client := &niri.Client{}
	ctx := context.Background()

	s, err := session.CaptureLive(ctx, client)
	if err != nil {
		return err
	}

	if err := session.Save(s, path); err != nil {
		return err
	}

	totalEntities := 0
	for _, ws := range s.Workspaces {
		totalEntities += len(ws.AllEntities())
	}

	fmt.Printf("Captured %d workspace(s), %d window(s) -> %s\n", len(s.Workspaces), totalEntities, path)
	fmt.Printf("session_id: %s\n", s.SessionID)
	return nil
}

func runShow(path string) error {
	s, err := session.Load(path)
	if err != nil {
		return err
	}

	fmt.Printf("session_id: %s  compositor: %s  schema_version: %d\n", s.SessionID, s.Compositor, s.SchemaVersion)
	fmt.Printf("created_at: %s\n", s.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("updated_at: %s\n", s.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
	fmt.Println()

	for _, ws := range s.Workspaces {
		fmt.Printf("workspace %s  output=%q  favorite=%v\n", ws.PersistentID, ws.OutputHint, ws.IsFavorite)
		if len(ws.Columns) == 0 {
			fmt.Println("  (no columns)")
		}
		for _, col := range ws.Columns {
			widthStr := "unknown"
			if col.WidthFraction != nil {
				widthStr = fmt.Sprintf("%.1f%%", *col.WidthFraction*100)
			}
			fmt.Printf("  column %s  idx=%d  width=%s\n", col.PersistentID, col.ColumnIndex, widthStr)
			for _, ent := range col.Entities {
				heightStr := "unknown"
				if ent.HeightFraction != nil {
					heightStr = fmt.Sprintf("%.1f%%", *ent.HeightFraction*100)
				}
				fmt.Printf("    entity %s  row=%d  height=%s  app_id=%q  title=%q  (last seen as niri win#%d)\n",
					ent.PersistentID, ent.RowInColumn, heightStr, ent.AppID, ent.Title, ent.LastSeen.NiriWindowID)
				fmt.Printf("      launch.command=%v\n", ent.Launch.Command)
				if ent.ProviderMetadata.CWDConfidence != "" {
					fmt.Printf("      cwd=%q  confidence=%s\n", ent.ProviderMetadata.CWD, ent.ProviderMetadata.CWDConfidence)
				}
			}
		}
	}
	return nil
}

func runRestore(path, entityID string) error {
	s, err := session.Load(path)
	if err != nil {
		return err
	}

	var target *session.Entity
	var idxHint uint8
	var outputHint string
	for wi := range s.Workspaces {
		ws := &s.Workspaces[wi]
		for ci := range ws.Columns {
			col := &ws.Columns[ci]
			for ei := range col.Entities {
				if col.Entities[ei].PersistentID == entityID {
					target = &col.Entities[ei]
					idxHint = ws.IdxHint
					outputHint = ws.OutputHint
				}
			}
		}
	}
	if target == nil {
		return fmt.Errorf("no entity with persistent_id %q found in %s", entityID, path)
	}

	fmt.Printf("Restoring entity %s (app_id=%q) -> target workspace idx=%d output=%q\n", target.PersistentID, target.AppID, idxHint, outputHint)
	fmt.Printf("Launch command: %v\n", target.Launch.Command)
	fmt.Println("LAUNCHING...")

	client := &niri.Client{}
	result := restore.Reconcile(context.Background(), client, *target, idxHint, outputHint, 10*time.Second)

	fmt.Println()
	fmt.Printf("RESULT: %s\n", result.State)
	if result.NiriWindowID != 0 {
		fmt.Printf("  matched niri window id: %d\n", result.NiriWindowID)
	}
	if result.Reason != "" {
		fmt.Printf("  reason: %s\n", result.Reason)
	}
	if len(result.ConflictingAppIDs) > 0 {
		fmt.Printf("  unrelated apps also present on target workspace: %v\n", result.ConflictingAppIDs)
	}

	if result.State != restore.StateRestored && result.State != restore.StateAlreadyPresent {
		os.Exit(1)
	}
	return nil
}

// runRestoreAll restores every entity in the session, one at a time
// (Part 13's sequential principle -- never launched in parallel), in
// ascending workspace-idx order (see design doc Part 9 addendum: while
// Niri's own trailing-empty-workspace mechanism protects against a USER
// getting ahead of restoration, a programmatic batch like this one could
// still ask Niri to address a workspace idx that doesn't exist yet if we
// went out of order).
//
// One entity failing does not stop the run (design doc Part 15): we
// collect every result and print a summary at the end, rather than
// aborting on the first problem.
func runRestoreAll(path string) error {
	s, err := session.Load(path)
	if err != nil {
		return err
	}

	// Sort a copy of the workspace list by IdxHint ascending -- don't
	// mutate s.Workspaces itself, we still want it in its original order
	// for anything else that might use it.
	workspaces := make([]session.Workspace, len(s.Workspaces))
	copy(workspaces, s.Workspaces)
	sort.Slice(workspaces, func(i, j int) bool { return workspaces[i].IdxHint < workspaces[j].IdxHint })

	client := &niri.Client{}
	ctx := context.Background()

	counts := map[restore.State]int{}
	total := 0

	for _, ws := range workspaces {
		if len(ws.Columns) == 0 {
			continue
		}
		fmt.Printf("\n=== workspace idx=%d (%s) ===\n", ws.IdxHint, ws.PersistentID)

		for _, col := range ws.Columns {
			if len(col.Entities) == 0 {
				continue
			}
			var results []restore.Result
			for _, ent := range col.Entities {
				total++
				fmt.Printf("\n--- entity %s (app_id=%q) ---\n", ent.PersistentID, ent.AppID)

				result := restore.Reconcile(ctx, client, ent, ws.IdxHint, ws.OutputHint, 10*time.Second)
				counts[result.State]++

				fmt.Printf("RESULT: %s\n", result.State)
				if result.NiriWindowID != 0 {
					fmt.Printf("  matched niri window id: %d\n", result.NiriWindowID)
				}
				if result.Reason != "" {
					fmt.Printf("  reason: %s\n", result.Reason)
				}
				if len(result.ConflictingAppIDs) > 0 {
					fmt.Printf("  unrelated apps also present on target workspace: %v\n", result.ConflictingAppIDs)
				}
				results = append(results, result)
			}
			// Phase 7: reconstruct column topology (merge entities that
			// were captured sharing a column but restore placed
			// separately, per niri's own placement mechanics). Enabled
			// independently of sizing below -- topology merging has its
			// own, separately-verified safety check (live adjacency
			// confirmation) and doesn't carry the same compounding-percentage
			// regression that sizing does.
			restore.MergeColumnTopology(ctx, client, col, results)

			// Phase 7: apply width/height, now with measure-and-correct
			// calibration (see ApplyColumnLayout's doc comment) rather
			// than the earlier blind single-request approach that caused
			// a confirmed, visible regression.
			restore.ApplyColumnLayout(ctx, client, col, results, ws.OutputHint)
		}
	}

	fmt.Printf("\n=== summary: %d entities processed ===\n", total)
	for _, state := range []restore.State{
		restore.StateRestored, restore.StateAlreadyPresent, restore.StateAmbiguous,
		restore.StatePartiallyRestored, restore.StateFailed,
	} {
		if counts[state] > 0 {
			fmt.Printf("  %-20s %d\n", state, counts[state])
		}
	}

	return nil
}

// runSetFavorite marks exactly one workspace as the startup/favorite

// runSetFavorite marks exactly one workspace as the startup/favorite
// workspace (Part 10 of the requirements doc), clearing the flag on any
// others.
//
// CURRENT LIMITATION, deliberate: only the workspace at idx_hint==1 can be
// marked favorite right now. Niri only guarantees a workspace at position
// 1 exists immediately at login -- workspace 2+ don't exist until
// something is placed in the one before it (the same trailing-empty-
// workspace mechanism documented in the design doc, Part 9 addendum).
// Marking any other saved workspace as favorite would be a request we
// cannot honor at actual login time, so we reject it now with a clear
// explanation rather than accept a configuration that fails silently
// later. Named/persistent niri workspaces (configured directly in niri's
// own config, immune to this positional restriction) could lift this
// limitation in the future -- not yet supported here.
func runSetFavorite(path, workspaceID string) error {
	s, err := session.Load(path)
	if err != nil {
		return err
	}

	var target *session.Workspace
	for i := range s.Workspaces {
		if s.Workspaces[i].PersistentID == workspaceID {
			target = &s.Workspaces[i]
		}
	}
	if target == nil {
		return fmt.Errorf("no workspace with persistent_id %q found in %s", workspaceID, path)
	}
	if target.IdxHint != 1 {
		return fmt.Errorf(
			"workspace %s has idx_hint=%d, but only the workspace at idx=1 can be marked favorite right now -- "+
				"niri only guarantees a workspace at position 1 exists immediately at login; support for "+
				"named/persistent workspaces at other positions is a future enhancement",
			workspaceID, target.IdxHint,
		)
	}

	for i := range s.Workspaces {
		s.Workspaces[i].IsFavorite = s.Workspaces[i].PersistentID == workspaceID
	}

	if err := session.Save(s, path); err != nil {
		return err
	}
	fmt.Printf("Marked workspace %s (idx=%d) as favorite.\n", workspaceID, target.IdxHint)
	return nil
}

// runLazyRestore is Phase 6's core behavior: restore the favorite
// workspace immediately, then wait for the user to actually navigate to
// each other saved workspace before restoring it -- rather than
// restoring everything at once (see design doc Part 9, the whole reason
// this phase exists: avoiding a login-time application launch storm).
//
// This is deliberately NOT the full daemon (Part 18) yet -- no systemd
// integration, no crash recovery, no persisted restore-state across runs.
// It's a foreground process you run once, that proves the lazy-loading
// mechanism itself works, per this phase's acceptance gate. Ctrl-C to
// stop.
func runLazyRestore(path string) error {
	s, err := session.Load(path)
	if err != nil {
		return err
	}

	client := &niri.Client{}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// wsKey identifies a saved workspace by (output, idx) TOGETHER, not idx
	// alone. THIS MATTERS: confirmed via real multi-monitor testing that
	// two different outputs can each have a workspace at the same idx
	// (each monitor has its own independent, 1-indexed stack). Keying on
	// bare idx alone caused two real, confirmed bugs: one saved workspace
	// silently overwriting another in the lookup map, and a workspace
	// being wrongly treated as "already attempted" just because some OTHER
	// monitor's same-idx workspace had already been restored.
	type wsKey struct {
		output string
		idx    uint8
	}

	// mergeKey correlates a saved workspace against a fresh capture by
	// MONITOR IDENTITY, not by raw output name -- CONFIRMED BUG, found via
	// real testing: correlating by wsKey{output, idx} (the connector
	// name) broke the moment niri renamed a connector, even WITHIN a
	// single boot, not just across separate reboots. wsKey itself is left
	// unchanged above, since `attempted` (below) is populated and checked
	// entirely from the SAVED file's own OutputHint within a single run
	// and never crosses a rename boundary -- this key is specifically for
	// the auto-capture MERGE, which correlates saved-vs-fresh data
	// exactly where a rename can occur between the two.
	type mergeKey struct {
		monitorID string
		idx       uint8
	}

	// In-memory only, for this run -- tracks which saved workspaces (by
	// (output, idx)) we've already attempted, so revisiting an
	// already-handled workspace doesn't re-trigger launches for apps that
	// can't safely report ALREADY_PRESENT (see design doc's Reconciliation
	// limitation).
	attempted := make(map[wsKey]bool)

	// refreshMonitorResolution re-resolves every saved monitor against the
	// CURRENT live output set, in BOTH directions -- CONFIRMED NECESSARY
	// to keep refreshing, not just compute once: a connector rename can
	// happen at any point during a long-running daemon session (confirmed
	// directly, more than once, in real testing), so a one-time
	// resolution at startup isn't sufficient on its own. Called here at
	// startup, and again on every auto-capture tick alongside the
	// existing liveKeyByID refresh -- the same self-healing cadence
	// already used elsewhere, applied consistently here too.
	//
	// resolvedConnectorByMonitorID: saved Monitor.PersistentID -> its
	// CURRENT live connector name (only present when a live match was
	// found). Used by resolveOutput (for placement) and the
	// missing-monitor warning below.
	//
	// monitorIDByLiveConnector: the reverse -- a CURRENT live connector
	// name -> the saved Monitor.PersistentID it corresponds to. Used by
	// refreshLiveKeys, so a live WorkspaceActivated event (which only
	// gives a connector name) can be translated into the SAME stable
	// MonitorID that byKey below is keyed on -- CONFIRMED BUG, found via
	// code review (not yet triggered by a test, since it requires a
	// rename to happen mid-session, which this project's own test
	// hardware does exhibit but not on every boot): without this, the
	// navigation-triggered restore path used the CURRENT connector name
	// to probe a map keyed by the STALE saved connector name -- the exact
	// same class of bug just fixed for the favorite-workspace restore
	// path, just in a different piece of code that a passing test didn't
	// happen to exercise.
	var resolvedConnectorByMonitorID map[string]string
	var monitorIDByLiveConnector map[string]string
	refreshMonitorResolution := func() {
		liveOutputs, _ := client.Outputs(ctx) // best-effort; nil handled fine by monitor.Resolve
		forward := make(map[string]string, len(s.Monitors))
		for _, mon := range s.Monitors {
			if match := monitor.Resolve(mon.Identity, liveOutputs); match.Status == monitor.Matched {
				forward[mon.PersistentID] = match.Connector
			}
		}
		reverse := make(map[string]string, len(forward))
		for monID, connector := range forward {
			reverse[connector] = monID
		}
		resolvedConnectorByMonitorID = forward
		monitorIDByLiveConnector = reverse
	}
	refreshMonitorResolution()

	// resolveOutput returns ws's CURRENT live connector, preferring its
	// resolved MonitorID over the stale saved OutputHint. Falls back to
	// OutputHint only when MonitorID is empty (e.g. a workspace with no
	// monitor info) or doesn't resolve to anything currently live.
	resolveOutput := func(ws session.Workspace) string {
		if ws.MonitorID != "" {
			if connector, ok := resolvedConnectorByMonitorID[ws.MonitorID]; ok {
				return connector
			}
		}
		return ws.OutputHint
	}

	// byKey is keyed by MONITOR IDENTITY (mergeKey), not raw output name
	// -- CONFIRMED BUG, found via code review: this used to be keyed by
	// wsKey{output: ws.OutputHint, idx}, which meant a live navigation
	// event (translated via refreshLiveKeys, which knows only the
	// CURRENT connector name) could never match an entry keyed by a
	// STALE saved connector name once a rename occurred. mergeKey{
	// monitorID, idx} sidesteps this: ws.MonitorID is stable regardless
	// of what the connector happens to be named right now.
	byKey := make(map[mergeKey]session.Workspace)
	for _, ws := range s.Workspaces {
		byKey[mergeKey{monitorID: ws.MonitorID, idx: ws.IdxHint}] = ws
	}

	// Warn about saved entities whose output isn't currently connected.
	// FIXED BUG, found via code review (same class as above): this used
	// to compare ws.OutputHint directly against a set of raw live
	// connector names, which would incorrectly report a monitor as
	// "disconnected" if it was simply renamed, not actually unplugged.
	// Now resolved through the same monitor-identity mapping as
	// everything else: a workspace is only "stranded" if its MonitorID
	// genuinely doesn't resolve to anything live right now.
	//
	// CONFIRMED gap, still not fixed here: niri never creates workspace
	// slots for a disconnected monitor at all, so there is no live
	// WorkspaceActivated event that could ever trigger these entities --
	// they are silently unreachable for the rest of this run, with no
	// error otherwise beyond this warning. We also don't watch for a
	// monitor being connected mid-run (a real, separate gap -- our
	// workspace lookup is built once, at startup, and only refreshed on
	// the auto-capture cadence).
	missingCounts := make(map[string]int)
	for _, ws := range s.Workspaces {
		allEnts := ws.AllEntities()
		if len(allEnts) == 0 {
			continue
		}
		stillMissing := true
		if ws.MonitorID != "" {
			if _, ok := resolvedConnectorByMonitorID[ws.MonitorID]; ok {
				stillMissing = false
			}
		} else if ws.OutputHint == "" {
			stillMissing = false // no monitor info at all -- nothing to report as missing
		}
		if stillMissing {
			label := ws.OutputHint
			if label == "" {
				label = ws.MonitorID
			}
			missingCounts[label] += len(allEnts)
		}
	}
	if len(missingCounts) > 0 {
		fmt.Println("\nnote: some saved entities reference a monitor that isn't currently connected:")
		for label, count := range missingCounts {
			fmt.Printf("  %q: %d entit(y/ies) not restored yet.\n", label, count)
		}
		fmt.Println("  If you'll reconnect this monitor later, no action needed -- they'll restore automatically once you do and navigate to that workspace.")
		fmt.Println("  If you won't be using this monitor again, run `continuum-cli profile-settings` to update your profile settings to ignore this monitor.")
	} else {
		fmt.Printf("  (output check: %d monitor(s) resolved, no stranded saved entities found)\n", len(resolvedConnectorByMonitorID))
	}

	restoreWorkspace := func(ws session.Workspace) {
		key := wsKey{output: ws.OutputHint, idx: ws.IdxHint}
		if attempted[key] {
			return
		}
		attempted[key] = true
		if len(ws.Columns) == 0 {
			return
		}

		resolvedOutput := resolveOutput(ws)
		fmt.Printf("\n=== restoring workspace idx=%d output=%q (%s) ===\n", ws.IdxHint, resolvedOutput, ws.PersistentID)

		// Latency: entity launches within this workspace are grouped by
		// app_id and run CONCURRENTLY across groups, one goroutine per
		// distinct app_id. Entities that SHARE an app_id stay strictly
		// serialized against each other, in their original order, inside
		// the same goroutine -- unchanged from the old fully-sequential
		// behavior for that case.
		//
		// WHY THIS IS SAFE (see the impact analysis this was built from):
		// restore.Entity's baseline-diffing and settle-window disambiguation
		// (internal/restore/restore.go) identify a freshly-launched window
		// by filtering live "window appeared" events down to ent.AppID.
		// Two DIFFERENT app_ids launched at the same moment can never be
		// mistaken for each other, because each goroutine's event filter
		// only ever accepts windows matching its OWN entity's app_id --
		// the ambiguity this design guards against (two windows of the
		// SAME app appearing close together) is exactly the case kept
		// serialized here, so that protection is completely unchanged.
		//
		// Per-entity output is buffered and printed in one Fprintf per
		// entity (not built up across the whole goroutine) so concurrent
		// groups don't interleave line-by-line into unreadable logs --
		// each entity's block still prints atomically, just possibly
		// interleaved with OTHER entities' whole blocks, which stays
		// readable.
		allResults := make([][]restore.Result, len(ws.Columns))
		for ci, col := range ws.Columns {
			allResults[ci] = make([]restore.Result, len(col.Entities))
		}

		type entitySlot struct {
			colIdx, entIdx int
			ent            session.Entity
		}
		byAppID := make(map[string][]entitySlot)
		for ci, col := range ws.Columns {
			for ei, ent := range col.Entities {
				byAppID[ent.AppID] = append(byAppID[ent.AppID], entitySlot{ci, ei, ent})
			}
		}

		var wg sync.WaitGroup
		for appID, slots := range byAppID {
			wg.Add(1)
			go func(appID string, slots []entitySlot) {
				defer wg.Done()
				for _, s := range slots {
					var out strings.Builder
					fmt.Fprintf(&out, "\n--- entity %s (app_id=%q) ---\n", s.ent.PersistentID, s.ent.AppID)
					result := restore.Reconcile(ctx, client, s.ent, ws.IdxHint, resolvedOutput, 10*time.Second)
					fmt.Fprintf(&out, "RESULT: %s\n", result.State)
					if result.Reason != "" {
						fmt.Fprintf(&out, "  reason: %s\n", result.Reason)
					}
					fmt.Print(out.String())
					allResults[s.colIdx][s.entIdx] = result
				}
			}(appID, slots)
		}
		wg.Wait()

		// Column topology/sizing stays exactly as before: strictly per
		// column, in order, and only after EVERY entity across every
		// app_id group has finished reconciling above.
		for ci, col := range ws.Columns {
			if len(col.Entities) == 0 {
				continue
			}
			results := allResults[ci]
			// Phase 7: reconstruct column topology -- see the matching
			// comment in runRestoreAll for the full reasoning. Enabled
			// independently of sizing below.
			restore.MergeColumnTopology(ctx, client, col, results)

			// Phase 7: apply width/height, now with measure-and-correct
			// calibration -- see the matching comment in runRestoreAll.
			restore.ApplyColumnLayout(ctx, client, col, results, resolvedOutput)
		}
	}

	// --- immediate favorite-workspace restore ("login") ---
	for _, ws := range s.Workspaces {
		if ws.IsFavorite {
			restoreWorkspace(ws)
		}
	}

	// --- live workspace id -> mergeKey tracking, needed to interpret
	// WorkspaceActivated events (which give only a niri workspace id) ---
	// FIXED BUG, found via code review: this used to map to
	// wsKey{output: <current live connector name>, idx}, then probe
	// byKey (keyed by the STALE saved connector name) -- these could
	// never match once a rename occurred, since one side always used the
	// current name and the other the name from whenever it was captured.
	// Now translates the live connector through monitorIDByLiveConnector
	// (kept fresh by refreshMonitorResolution) into the SAME stable
	// MonitorID that byKey above is keyed on.
	liveKeyByID := make(map[uint64]mergeKey)
	refreshLiveKeys := func(wss []niri.Workspace) {
		for _, w := range wss {
			output := ""
			if w.Output != nil {
				output = *w.Output
			}
			liveKeyByID[w.ID] = mergeKey{monitorID: monitorIDByLiveConnector[output], idx: w.Idx}
		}
	}

	// FIXED BUG, confirmed via real testing: this initial seed used to be
	// a single, unchecked attempt -- if client.Workspaces(ctx) returned an
	// error OR an empty/incomplete list (confirmed to happen for real,
	// right after a reboot -- the same underlying query that also
	// produced a bogus "monitor disconnected" warning for both real,
	// connected monitors in the same run), liveKeyByID stayed empty with
	// NO indication anything was wrong. Since the ONLY other way this map
	// gets updated is via WorkspacesChanged events -- which niri has no
	// reason to ever emit again if its own workspace list was already
	// stable by that point -- a single bad response here could silently
	// break EVERY future WorkspaceActivated lookup for the rest of the
	// daemon's lifetime: navigating anywhere would do nothing, with
	// nothing printed to explain why. Confirmed directly: navigating to
	// two different real, populated workspaces produced no restore at
	// all. Retry a few times with a short delay before giving up, since a
	// transient hiccup right after boot is exactly the kind of thing a
	// brief retry should paper over.
	const initialLiveKeySeedAttempts = 3
	seeded := false
	for attempt := 1; attempt <= initialLiveKeySeedAttempts; attempt++ {
		wss, err := client.Workspaces(ctx)
		if err == nil && len(wss) > 0 {
			refreshLiveKeys(wss)
			seeded = true
			break
		}
		if attempt < initialLiveKeySeedAttempts {
			fmt.Printf("  (startup: workspace query came back empty or failed, retrying (%d/%d))\n", attempt, initialLiveKeySeedAttempts)
			time.Sleep(1 * time.Second)
		}
	}
	if !seeded {
		fmt.Println("  (warning: could not reliably read live workspaces at startup -- workspace-activation-triggered restore may not work correctly until the next periodic refresh)")
	}

	fmt.Println("\nWaiting for workspace activations (Ctrl-C to stop)...")

	events, errs, err := client.EventStream(ctx)
	if err != nil {
		return fmt.Errorf("starting event stream: %w", err)
	}

	// Periodic auto-capture (Phase 9): keeps the saved session file
	// reasonably fresh while this daemon runs, rather than only ever
	// reflecting whatever was true at the last manual `capture`. This
	// intentionally does NOT affect `s`/`byKey`/`attempted` above -- this
	// run keeps restoring against the ORIGINAL session it started with;
	// auto-capture only updates the FILE, for whatever restores NEXT
	// login, not this one.
	//
	// 5 minutes is a first guess, not empirically validated -- similar in
	// spirit to settleWindow in internal/restore, worth revisiting if it
	// turns out too coarse (losing more work than feels right) or
	// unnecessarily frequent.
	const autoCaptureInterval = 5 * time.Minute
	autoCaptureTicker := time.NewTicker(autoCaptureInterval)
	defer autoCaptureTicker.Stop()

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				fmt.Println("event stream closed.")
				return nil
			}
			switch ev.Kind {
			case niri.EventWorkspacesChanged:
				// Workspace layout changed (created/pruned/renumbered) --
				// refresh our id->key map so subsequent activations are
				// interpreted correctly.
				refreshLiveKeys(ev.WorkspacesChanged.Workspaces)
			case niri.EventWorkspaceActivated:
				liveID := ev.WorkspaceActivated.ID
				key, ok := liveKeyByID[liveID]
				if !ok {
					// DIAGNOSTIC ADDED, confirmed necessary via real
					// testing: this lookup failing was previously
					// completely silent -- confirmed directly that
					// navigating to real, populated saved workspaces
					// multiple times produced no restore and no error at
					// all. Print exactly what's missing so the next test
					// gives definitive evidence instead of more
					// inference.
					fmt.Printf("  (workspace activated: live id=%d has no known monitor mapping -- liveKeyByID may be stale, or this workspace was just created and hasn't been learned yet; nothing to restore)\n", liveID)
					continue
				}
				ws, ok := byKey[key]
				if !ok {
					fmt.Printf("  (workspace activated: live id=%d mapped to monitor_id=%q idx=%d, but no saved workspace matches that key -- nothing to restore)\n", liveID, key.monitorID, key.idx)
					continue
				}
				restoreWorkspace(ws)
			}

		case <-autoCaptureTicker.C:
			// Self-healing safety net: re-verify/refresh liveKeyByID here
			// too, not just at startup. Defends against the SAME class of
			// silent failure the startup-retry fix above addresses, in
			// case some other, future cause produces an empty/incomplete
			// workspace query at some point mid-run -- rather than
			// leaving workspace-activation-triggered restore silently
			// broken for the rest of the session, this recovers it within
			// one auto-capture interval.
			//
			// refreshMonitorResolution FIRST, then refreshLiveKeys --
			// refreshLiveKeys depends on monitorIDByLiveConnector being
			// current, since that's what translates a live connector name
			// into the stable MonitorID liveKeyByID's entries are keyed
			// on.
			refreshMonitorResolution()
			if wss, err := client.Workspaces(ctx); err == nil && len(wss) > 0 {
				refreshLiveKeys(wss)
			}

			fresh, err := session.CaptureLive(ctx, client)
			if err != nil {
				fmt.Printf("  (auto-capture failed: %v)\n", err)
				continue
			}
			if fresh.IsEmpty() {
				// See session.IsEmpty's doc comment -- refuse to overwrite
				// a known-good session file with an accidentally empty
				// capture, e.g. from a transient niri IPC hiccup.
				fmt.Println("  (auto-capture skipped: came back empty, keeping existing session file)")
				continue
			}

			// FIXED BUG, confirmed via real testing: correlating old vs.
			// fresh MONITORS and WORKSPACES by raw output NAME
			// (wsKey{output, idx}) broke as soon as niri renamed a
			// connector -- confirmed directly to happen even WITHIN a
			// single boot on this project's own hardware, not just
			// across separate reboots. Fixed by correlating on MONITOR
			// IDENTITY instead: if an OLD saved monitor still resolves
			// to something live (per resolvedConnectorByMonitorID,
			// refreshed just above), carry its PersistentID forward onto
			// the corresponding FRESH monitor entry (rather than letting
			// it get a brand-new random ID) -- so Workspace.MonitorID
			// stays stable across capture cycles for the SAME physical
			// hardware, however its connector happens to be named this
			// time. Reuses the SAME resolution this tick already
			// computed for liveKeyByID above, rather than resolving the
			// same monitors against the same live outputs a second time.
			idRemap := make(map[string]string, len(fresh.Monitors))
			for i := range fresh.Monitors {
				if oldID, ok := monitorIDByLiveConnector[fresh.Monitors[i].CapturedConnector]; ok {
					idRemap[fresh.Monitors[i].PersistentID] = oldID
					fresh.Monitors[i].PersistentID = oldID
				}
			}
			for i := range fresh.Workspaces {
				if preservedID, ok := idRemap[fresh.Workspaces[i].MonitorID]; ok {
					fresh.Workspaces[i].MonitorID = preservedID
				}
			}

			// Preserve any OLD monitor that isn't currently live at all
			// (e.g. a disconnected external display) -- same "don't
			// silently delete what's just not live right now" principle
			// already applied to workspaces below.
			freshMonitorIDs := make(map[string]bool, len(fresh.Monitors))
			for _, m := range fresh.Monitors {
				freshMonitorIDs[m.PersistentID] = true
			}
			for _, oldMon := range s.Monitors {
				if !freshMonitorIDs[oldMon.PersistentID] {
					fresh.Monitors = append(fresh.Monitors, oldMon)
				}
			}

			// MERGE, don't replace. CaptureLive only ever reflects what's
			// CURRENTLY LIVE -- it has no awareness of saved workspaces
			// niri hasn't even created yet. Confirmed as a real risk: if
			// the user stays on one workspace (e.g. the favorite) for
			// more than a couple of auto-capture cycles without visiting
			// anything else, a naive overwrite would silently DELETE
			// every not-yet-restored saved workspace from the file --
			// they were never live, so a bare live-only capture simply
			// wouldn't include them. Preserve any saved workspace we
			// haven't attempted restoring yet THIS RUN (not in
			// `attempted`, still keyed by the saved file's own
			// OutputHint -- see wsKey/mergeKey's comments above for why
			// that one doesn't need to change) and that isn't already
			// reflected in the fresh live capture (now correlated by
			// MonitorID, not output name).
			freshKeys := make(map[mergeKey]bool, len(fresh.Workspaces))
			for _, ws := range fresh.Workspaces {
				freshKeys[mergeKey{monitorID: ws.MonitorID, idx: ws.IdxHint}] = true
			}
			for _, ws := range s.Workspaces {
				attemptedKey := wsKey{output: ws.OutputHint, idx: ws.IdxHint}
				mKey := mergeKey{monitorID: ws.MonitorID, idx: ws.IdxHint}
				if attempted[attemptedKey] || freshKeys[mKey] {
					continue
				}
				fresh.Workspaces = append(fresh.Workspaces, ws)
			}

			// Also preserve the `favorite` flag on workspaces that WERE
			// freshly re-captured (e.g. the favorite workspace itself,
			// actively in use). CONFIRMED BUG, found via real testing:
			// CaptureLive always builds every workspace with
			// IsFavorite=false, since it only knows about live compositor
			// state, not user-set metadata from a prior `set-favorite`
			// call -- without this, every auto-capture cycle silently
			// erased the favorite designation, causing the favorite
			// workspace to stop auto-restoring on the NEXT run entirely.
			// Now correlated by (MonitorID, idx), not (output, idx) --
			// CONFIRMED directly (real test data) that the old
			// output-based correlation silently lost the favorite flag
			// the moment the connector renamed mid-session.
			originalFavorite := make(map[mergeKey]bool, len(s.Workspaces))
			for _, ws := range s.Workspaces {
				if ws.IsFavorite {
					originalFavorite[mergeKey{monitorID: ws.MonitorID, idx: ws.IdxHint}] = true
				}
			}
			for i := range fresh.Workspaces {
				key := mergeKey{monitorID: fresh.Workspaces[i].MonitorID, idx: fresh.Workspaces[i].IdxHint}
				if originalFavorite[key] {
					fresh.Workspaces[i].IsFavorite = true
				}
			}

			if err := session.Save(fresh, path); err != nil {
				fmt.Printf("  (auto-capture: failed to save: %v)\n", err)
				continue
			}
			total := 0
			for _, ws := range fresh.Workspaces {
				total += len(ws.AllEntities())
			}
			fmt.Printf("  (auto-captured: %d workspace(s), %d entit(y/ies) -> %s)\n", len(fresh.Workspaces), total, path)

		case _, ok := <-errs:
			if !ok {
				continue
			}
			// Non-fatal parse errors -- keep going rather than abort the
			// whole lazy-restore run over one bad event line.

		case <-ctx.Done():
			fmt.Println("\nstopping (signal received)...")
			return nil
		}
	}
}
