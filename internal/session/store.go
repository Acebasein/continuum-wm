package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"continuum-wm/internal/desktopentry"
	"continuum-wm/internal/idgen"
	"continuum-wm/internal/niri"
	"continuum-wm/internal/procinfo"
)

// Save writes the session to path as YAML, atomically, and keeps one
// previous generation as a manual fallback.
//
// ATOMICITY: writes to a temp file in the same directory first, then
// renames it over path. A rename within the same filesystem is atomic on
// Linux -- the file at path is ALWAYS either the complete previous
// version or the complete new version, never a half-written one, even if
// this process is killed mid-write. This is what makes an explicit
// "capture completed successfully" flag in the schema unnecessary: the
// file's mere existence at a stable path already carries that guarantee
// structurally, rather than as a field a reader could misread or a writer
// could forget to set.
//
// PREVIOUS GENERATION: before the atomic rename, if a file already exists
// at path, it's preserved as path+".previous" (overwriting whatever was
// there before). This covers a DIFFERENT risk than atomicity: a capture
// can be perfectly well-formed YAML and still reflect a bad moment in
// time (e.g. a transient niri IPC hiccup returning stale/incomplete data)
// -- atomicity alone wouldn't protect against confidently overwriting a
// good session with a valid-but-wrong one. Keeping one generation back
// gives a manual recovery path (`cp path.previous path`) for that case.
func Save(s *Session, path string) error {
	s.UpdatedAt = time.Now().UTC()

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling session to YAML: %w", err)
	}

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("creating temp file for atomic write: %w", err)
	}
	tmpPath := tmp.Name()
	// If anything below fails before the final rename, clean up the temp
	// file rather than leaving it behind -- os.Remove on an already-
	// renamed-away path is a harmless no-op error, ignored deliberately.
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp session file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp session file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp session file: %w", err)
	}
	// 0600: session files can reveal working directories, open documents,
	// etc. -- treat them as user-private by default. CreateTemp defaults
	// to 0600 already, but set it explicitly rather than rely on that.
	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("setting permissions on temp session file: %w", err)
	}

	// Preserve the previous generation, best-effort -- if path doesn't
	// exist yet (first-ever capture), this is expected to fail and is not
	// treated as an error.
	if _, err := os.Stat(path); err == nil {
		_ = os.Rename(path, path+".previous")
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("renaming temp session file into place: %w", err)
	}
	return nil
}

// IsEmpty reports whether s has zero entities across every workspace --
// used by the auto-capture loop (see cmd/continuum-cli) to avoid
// overwriting a known-good session with an accidentally empty one from a
// transient niri IPC glitch. Not used by the explicit `capture` command,
// which should always honor exactly what the user asked for, empty or
// not.
func (s *Session) IsEmpty() bool {
	for _, ws := range s.Workspaces {
		if len(ws.Entities) > 0 {
			return false
		}
	}
	return true
}

// Load reads and parses a session file. It rejects files with a schema
// version it doesn't understand rather than guessing -- silently
// misinterpreting an incompatible schema is exactly the kind of "confident
// but wrong" behavior the project is designed to avoid.
func Load(path string) (*Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading session file %s: %w", path, err)
	}

	var s Session
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parsing session YAML: %w", err)
	}

	if s.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf(
			"session file has schema_version %d, this build of continuum-wm understands version %d -- refusing to load rather than guess",
			s.SchemaVersion, SchemaVersion,
		)
	}

	return &s, nil
}

// CaptureLive builds a brand-new Session from the current live Niri state.
//
// Phase 2 note: every workspace and window gets a FRESH persistent_id every
// time this runs. There is deliberately no "is this the same window as
// last capture" logic here yet -- that matching problem belongs to Phase 4
// / Part 7 of the design doc, and doing it half-correctly now would be
// worse than not doing it at all. Each capture is an honest, independent
// snapshot.
//
// Per niri's own IPC documentation, a separate Workspaces() call and a
// separate Windows() call are not guaranteed to be perfectly consistent
// with each other (state can change in between). CONFIRMED via real
// testing that this isn't just a theoretical risk: during a long-running
// daemon session with heavy restore activity across two monitors, a
// window's WorkspaceID referenced a workspace that no longer appeared in
// the just-fetched workspace list (almost certainly renumbered/recreated
// in the gap between the two calls) -- the OLD version of this function
// silently dropped such windows entirely, with no warning at all, since it
// only ever visits windows through a per-known-workspace lookup. This
// caused real data loss: apps that were captured correctly one cycle
// vanished from the very next auto-capture, despite still running.
//
// Fixed by detecting this inconsistency (an "orphaned" window whose
// WorkspaceID matches no workspace in this capture) and retrying the
// whole two-query capture once -- a fresh pair of queries is likely to
// land consistently. If orphans are STILL present after the retry, they
// are preserved (not silently dropped) under a clearly-marked recovery
// workspace, with a warning printed, so the person running this can see
// something is off rather than silently losing data.
func CaptureLive(ctx context.Context, client *niri.Client) (*Session, error) {
	const maxAttempts = 2
	var s *Session
	var orphaned []niri.Window
	var err error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		s, orphaned, err = captureOnce(ctx, client)
		if err != nil {
			return nil, err
		}
		if len(orphaned) == 0 {
			return s, nil
		}
		if attempt < maxAttempts {
			fmt.Printf("  (capture: %d window(s) referenced a workspace not in this snapshot -- retrying capture once)\n", len(orphaned))
		}
	}

	// Still inconsistent after a retry -- preserve the orphaned windows
	// rather than silently losing them, and say so plainly.
	fmt.Printf("  (capture: %d window(s) still could not be attributed to any workspace after retrying -- preserving them under a recovery workspace)\n", len(orphaned))
	recovery := Workspace{
		PersistentID: idgen.New("ws-recovery"),
		OutputHint:   "",
		IdxHint:      0,
	}
	for _, win := range orphaned {
		if win.AppID == nil || *win.AppID == "" {
			continue
		}
		ent := Entity{
			PersistentID: idgen.New("ent"),
			AppID:        *win.AppID,
			LastSeen:     LastSeen{NiriWindowID: win.ID, CapturedAt: time.Now().UTC()},
		}
		if win.Title != nil {
			ent.Title = *win.Title
		}
		recovery.Entities = append(recovery.Entities, ent)
	}
	if len(recovery.Entities) > 0 {
		s.Workspaces = append(s.Workspaces, recovery)
	}
	return s, nil
}

// captureOnce performs a single capture attempt, returning both the
// resulting Session and any windows that couldn't be attributed to a
// workspace in this same attempt (see CaptureLive's doc comment for why
// that can happen).
func captureOnce(ctx context.Context, client *niri.Client) (*Session, []niri.Window, error) {
	niriWorkspaces, err := client.Workspaces(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading workspaces: %w", err)
	}
	niriWindows, err := client.Windows(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("reading windows: %w", err)
	}

	now := time.Now().UTC()

	// Loaded once per capture, not per-window -- scanning the filesystem
	// for every window would be wasteful, and desktop entries don't change
	// mid-capture.
	desktopEntries := desktopentry.Load()

	s := &Session{
		SchemaVersion: SchemaVersion,
		SessionID:     idgen.New("session"),
		CreatedAt:     now,
		UpdatedAt:     now,
		Compositor:    "niri",
	}

	validWorkspaceIDs := make(map[uint64]bool, len(niriWorkspaces))
	for _, nw := range niriWorkspaces {
		validWorkspaceIDs[nw.ID] = true
	}

	// Index windows by their niri workspace_id so we can group them.
	windowsByWorkspace := make(map[uint64][]niri.Window)
	// Count how many windows share each PID. A PID shared by more than one
	// window means we cannot safely attribute a /proc/<pid>/cwd reading to
	// any single one of them -- confirmed to happen in practice with
	// GNOME/GTK single-instance apps (Nautilus opening a second window
	// reuses the same PID), and possibly Ghostty. This check makes CWD
	// capture safe by construction: it doesn't need to know in advance
	// which apps share processes, it discovers it from the live data.
	windowCountByPID := make(map[int32]int)
	var orphaned []niri.Window
	for _, w := range niriWindows {
		if w.WorkspaceID == nil {
			continue // a window with no workspace isn't something we can place; skip rather than guess
		}
		if !validWorkspaceIDs[*w.WorkspaceID] {
			// This window's workspace disappeared between the Workspaces()
			// and Windows() calls above -- see CaptureLive's doc comment.
			orphaned = append(orphaned, w)
			continue
		}
		windowsByWorkspace[*w.WorkspaceID] = append(windowsByWorkspace[*w.WorkspaceID], w)
		if w.PID != nil {
			windowCountByPID[*w.PID]++
		}
	}

	for _, nw := range niriWorkspaces {
		ws := Workspace{
			PersistentID: idgen.New("ws"),
			IdxHint:      nw.Idx,
			IsFavorite:   false, // Phase 2 doesn't set this yet; Phase 6 will
		}
		if nw.Output != nil {
			ws.OutputHint = *nw.Output
		}

		for _, win := range windowsByWorkspace[nw.ID] {
			// Skip windows with no app_id at all -- Phase 1 showed us these
			// are typically transient system popups, not real restorable
			// application windows (see design doc discussion of the "som"
			// / "Mount" windows observed during testing).
			if win.AppID == nil || *win.AppID == "" {
				continue
			}

			ent := Entity{
				PersistentID: idgen.New("ent"),
				AppID:        *win.AppID,
				LastSeen: LastSeen{
					NiriWindowID: win.ID,
					CapturedAt:   now,
				},
			}
			if win.Title != nil {
				ent.Title = *win.Title
			}
			if win.PID != nil {
				// Best-effort only -- see internal/procinfo. The PID is
				// used here transiently and is never itself persisted.
				var procCmdline []string
				if cmd, err := procinfo.ReadCmdline(*win.PID); err == nil {
					procCmdline = cmd
				}
				ent.Launch.Command = resolveLaunchCommand(*win.AppID, procCmdline, desktopEntries)

				cwd, confidence := resolveCWD(*win.AppID, *win.PID, windowCountByPID[*win.PID] > 1)
				ent.ProviderMetadata.CWD = cwd
				ent.ProviderMetadata.CWDConfidence = confidence
			}
			ws.Entities = append(ws.Entities, ent)
		}

		s.Workspaces = append(s.Workspaces, ws)
	}

	return s, orphaned, nil
}

// resolveLaunchCommand decides what command to save for relaunching this
// entity. Tried in order, first success wins:
//
//  1. An XDG .desktop file matching app_id (or its StartupWMClass) -- see
//     internal/desktopentry. This is the most reliable source: it's the
//     SAME data app launchers like Rofi/Wofi use, it never looks at a
//     PID at all (sidestepping the XWayland-proxy-PID problem confirmed
//     with ONLYOFFICE), and it can produce launch commands the other
//     strategies below have no way to discover (special flags, wrapper
//     scripts, etc.).
//  2. A plain, $PATH-resolvable command name derived from app_id.
//  3. The raw /proc/<pid>/cmdline capture, as a last resort.
//
// WHY (2) AND (3) STILL EXIST, given (1) is better: not every running app
// necessarily has a discoverable .desktop file (some are launched
// directly from a script or built from source without installing one),
// so this stays a layered fallback rather than a hard requirement.
//
// WHY NOT JUST /proc/<pid>/cmdline: confirmed via real testing that this
// on NixOS often resolves to a content-addressed Nix store path (e.g.
// "/nix/store/f0328rw.../bin/.ghostty-wrapped"), which becomes invalid the
// moment a system rebuild changes that package's derivation hash -- a
// saved session could silently stop being restorable after a routine
// `nixos-rebuild switch`. Separately, confirmed with ONLYOFFICE that the
// PID niri reports for an XWayland/X11 window can belong to the
// xwayland-satellite bridge process, not the app itself, making
// /proc-based capture read the WRONG process's command line entirely.
//
// Strategy (2) mirrors a design choice found in nirinit (a comparable,
// existing niri session tool -- see docs/design doc risk notes): it never
// captures an absolute path at all, defaulting instead to the literal
// app_id (resolved via $PATH at spawn time). We store the PLAIN command
// name here, not the absolute path LookPath resolves to -- so that $PATH
// (and on NixOS, indirections like /run/current-system/sw/bin/...) gets
// re-resolved fresh at restore time, rather than baking in whatever
// happened to be true at capture time.
func resolveLaunchCommand(appID string, procCmdline []string, desktopEntries *desktopentry.Index) []string {
	if desktopEntries != nil {
		if entry := desktopEntries.Lookup(appID); entry != nil {
			if cmd := entry.LaunchCommand(); len(cmd) > 0 {
				return sanitizeNixStorePath(cmd)
			}
		}
	}

	if candidate := commandNameFromAppID(appID); candidate != "" {
		if _, err := exec.LookPath(candidate); err == nil {
			return []string{candidate}
		}
	}

	// Last resort -- still carries the risks described above. A future
	// phase (Part 11 / Application Providers) is the right place for a
	// user-configurable override map, similar to nirinit's `launch` config
	// section, for the cases none of the above can resolve.
	return procCmdline
}

// sanitizeNixStorePath guards against the exact NixOS fragility problem
// this project has already hit once (see the raw-cmdline case above),
// showing up again through a different source: confirmed that on NixOS,
// a .desktop file's Exec= line can itself bake in a raw, versioned Nix
// store path (e.g. "/nix/store/f0328rw.../bin/ghostty"), which becomes
// invalid the moment a system rebuild changes that package's derivation
// hash -- exactly as fragile as the /proc/cmdline case, just from a
// different source. If argv[0] looks like a Nix store path, try the
// plain basename ("ghostty") via $PATH first; only keep the absolute
// path if the plain name doesn't resolve at all.
func sanitizeNixStorePath(cmd []string) []string {
	if len(cmd) == 0 || !strings.HasPrefix(cmd[0], "/nix/store/") {
		return cmd
	}
	base := filepath.Base(cmd[0])
	if _, err := exec.LookPath(base); err == nil {
		sanitized := append([]string{base}, cmd[1:]...)
		return sanitized
	}
	// Not resolvable via $PATH -- keep the absolute path as a last resort,
	// since something (even if fragile) is better than nothing.
	return cmd
}

// commandNameFromAppID applies a simple heuristic: many app_ids follow
// reverse-DNS notation (e.g. "com.mitchellh.ghostty", "org.gnome.Nautilus"),
// where the last segment, lowercased, is usually the actual binary name.
// Plain app_ids (e.g. "firefox", "kitty") pass through unchanged. This is
// a heuristic, not a guarantee -- callers must still verify with
// exec.LookPath before trusting it, which resolveLaunchCommand does.
func commandNameFromAppID(appID string) string {
	if appID == "" {
		return ""
	}
	parts := strings.Split(appID, ".")
	last := parts[len(parts)-1]
	return strings.ToLower(last)
}

// shellCommandNames is a minimal, extensible allowlist of process names
// (as reported by /proc/<pid>/comm) we recognize as an actual login/
// interactive shell -- as opposed to a terminal emulator's own internal
// helper processes.
//
// WHY THIS EXISTS: confirmed via real testing that Kitty spawns a second
// child process alongside the user's shell -- a "kitten __atexit__" helper,
// apparently used for Kitty's own exit-cleanup handling. A naive "does this
// window's process have exactly one child" check sees TWO children (the
// real shell, plus this helper) and incorrectly concludes the CWD is
// ambiguous, when it isn't. Filtering to recognized shell names before
// counting fixes this without weakening the original safety guarantee: an
// unrecognized shell (e.g. nushell, elvish -- not yet in this list) still
// correctly falls back to "unknown" rather than being guessed at, which is
// the right default per the project's core principle, even though it means
// we under-detect for shells we haven't added yet. Extend this list as
// needed.
var shellCommandNames = map[string]bool{
	"bash": true,
	"zsh":  true,
	"fish": true,
	"sh":   true,
	"dash": true,
	"ksh":  true,
	"tcsh": true,
	"csh":  true,
}

// filterShells narrows a list of child PIDs down to the ones that look
// like an actual shell process, per shellCommandNames.
func filterShells(pids []int32) []int32 {
	var shells []int32
	for _, pid := range pids {
		if comm, err := procinfo.Comm(pid); err == nil && shellCommandNames[comm] {
			shells = append(shells, pid)
		}
	}
	return shells
}

// ResolveTerminalCWD resolves pid's working directory using the same
// child-shell logic CaptureLive uses during a normal capture (see
// resolveCWD) -- exposed for the restore package to use as a POST-MATCH
// VERIFICATION signal (design doc Part 7): after tentatively matching a
// newly launched window to a saved entity, we can check whether the new
// window's actual cwd agrees with what we saved, before trusting the
// match enough to place it.
//
// This assumes pid is not currently shared with another window, which is
// reasonable for a window we just identified via baseline-diffing (see
// restore.Entity) -- but note this assumption would need revisiting if we
// ever restore an app that opens multiple windows from a single launch
// command.
func ResolveTerminalCWD(appID string, pid int32) (string, CWDConfidence) {
	return resolveCWD(appID, pid, false)
}

// IsKnownTerminal reports whether appID is one we recognize as a terminal
// emulator -- see terminalAppIDs.
func IsKnownTerminal(appID string) bool {
	return terminalAppIDs[appID]
}

// terminalAppIDs is a minimal, hardcoded allowlist of app_ids we currently
// know are terminal emulators. This is a temporary stand-in for the real
// Application Provider architecture (design doc, Part 11 / Phase 8) --
// good enough to scope CWD capture correctly today, without pretending to
// be a general "detect any terminal" mechanism. Extend this list as we
// test against more terminals.
var terminalAppIDs = map[string]bool{
	"com.mitchellh.ghostty": true,
	"kitty":                 true,
	"alacritty":             true,
	"foot":                  true,
	"org.gnome.Ptyxis":      true,
	"org.gnome.Terminal":    true,
	"org.kde.konsole":        true,
	"xterm":                 true,
}

// resolveCWD attempts to determine an entity's working directory. It is
// deliberately conservative: it only attempts this for known terminal
// app_ids, and it never falls back to a guess when it isn't confident.
//
// Why not just read /proc/<pid>/cwd directly (as Phase 4's first version
// did)? Confirmed experimentally: terminal emulators like Ghostty and
// Kitty run the user's shell as a CHILD process. The terminal emulator's
// OWN cwd reflects wherever it was launched from (e.g. the user's home
// directory) and does not update as the user navigates inside the shell.
// Reading it directly silently produces a plausible-looking but WRONG
// answer -- worse than reporting "unknown," since it looks trustworthy.
// The fix is to find the terminal's child process (the shell) and read
// THAT process's cwd instead.
func resolveCWD(appID string, windowPID int32, pidSharedAcrossWindows bool) (string, CWDConfidence) {
	if !terminalAppIDs[appID] {
		// Not a terminal -- CWD isn't a meaningful concept for e.g. a
		// browser or office app in the way this project uses it. Leave it
		// unset entirely rather than reporting a number that doesn't mean
		// anything.
		return "", ""
	}

	if pidSharedAcrossWindows {
		// Confirmed with Ghostty: multiple windows can share one process.
		// We cannot tell which window a given directory belongs to, so we
		// don't even attempt the read -- see design doc, Part 6's core
		// rule against guessing.
		return "", CWDUnknown
	}

	children, err := procinfo.Children(windowPID)
	if err != nil {
		return "", CWDUnknown
	}
	shells := filterShells(children)

	switch len(shells) {
	case 1:
		// The expected, common case: one terminal window, one identifiable
		// foreground shell child (ignoring any other helper processes the
		// terminal emulator may have spawned alongside it -- see
		// shellCommandNames above). This is the reading we actually trust.
		if cwd, err := procinfo.ReadCwd(shells[0]); err == nil {
			return cwd, CWDHigh
		}
		return "", CWDUnknown

	case 0:
		// No recognized shell child found -- either the terminal has no
		// children at all (unusual), or its child uses a shell we don't
		// recognize yet (see shellCommandNames). We deliberately do NOT
		// fall back to reading the window's own PID here: we've confirmed
		// (the original Kitty bug) that a terminal emulator's own cwd
		// reflects wherever it was launched from, not where the user has
		// navigated to -- falling back to it would reintroduce exactly the
		// false-confidence bug we just fixed, just for a different trigger
		// condition. Honest "unknown" is the correct outcome here.
		return "", CWDUnknown

	default:
		// More than one recognized shell child -- we don't know which one
		// is the actual foreground shell the user cares about. Don't guess.
		return "", CWDUnknown
	}
}
