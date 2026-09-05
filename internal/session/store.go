package session

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"continuum-wm/internal/idgen"
	"continuum-wm/internal/niri"
	"continuum-wm/internal/procinfo"
)

// Save writes the session to path as YAML. It always updates UpdatedAt
// before writing.
func Save(s *Session, path string) error {
	s.UpdatedAt = time.Now().UTC()

	data, err := yaml.Marshal(s)
	if err != nil {
		return fmt.Errorf("marshaling session to YAML: %w", err)
	}

	// 0600: session files can reveal working directories, open documents,
	// etc. -- treat them as user-private by default.
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing session file %s: %w", path, err)
	}
	return nil
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
// with each other (state can change in between). CaptureLive does its best
// to associate windows with the workspaces list it just read, but treat
// this capture as best-effort, matching the same caveat noted in
// internal/niri/client.go.
func CaptureLive(ctx context.Context, client *niri.Client) (*Session, error) {
	niriWorkspaces, err := client.Workspaces(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading workspaces: %w", err)
	}
	niriWindows, err := client.Windows(ctx)
	if err != nil {
		return nil, fmt.Errorf("reading windows: %w", err)
	}

	now := time.Now().UTC()

	s := &Session{
		SchemaVersion: SchemaVersion,
		SessionID:     idgen.New("session"),
		CreatedAt:     now,
		UpdatedAt:     now,
		Compositor:    "niri",
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
	for _, w := range niriWindows {
		if w.WorkspaceID == nil {
			continue // a window with no workspace isn't something we can place; skip rather than guess
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
				ent.Launch.Command = resolveLaunchCommand(*win.AppID, procCmdline)

				cwd, confidence := resolveCWD(*win.AppID, *win.PID, windowCountByPID[*win.PID] > 1)
				ent.ProviderMetadata.CWD = cwd
				ent.ProviderMetadata.CWDConfidence = confidence
			}
			ws.Entities = append(ws.Entities, ent)
		}

		s.Workspaces = append(s.Workspaces, ws)
	}

	return s, nil
}

// resolveLaunchCommand decides what command to save for relaunching this
// entity. It prefers a plain, $PATH-resolvable command name derived from
// app_id over the raw /proc/<pid>/cmdline capture, falling back to the
// latter only when the former isn't usable.
//
// WHY: confirmed via real testing that /proc/<pid>/cmdline on NixOS often
// resolves to a content-addressed Nix store path (e.g.
// "/nix/store/f0328rw.../bin/.ghostty-wrapped"), which becomes invalid the
// moment a system rebuild changes that package's derivation hash -- a
// saved session could silently stop being restorable after a routine
// `nixos-rebuild switch`.
//
// This mirrors a design choice found in nirinit (a comparable, existing
// niri session tool -- see docs/design doc risk notes): it never captures
// an absolute path at all, defaulting instead to the literal app_id
// (resolved via $PATH at spawn time) with a config override for apps whose
// app_id doesn't map to a real binary name (PWAs, Flatpaks, etc.).
//
// We store the PLAIN command name here, not the absolute path LookPath
// resolves to -- so that $PATH (and on NixOS, indirections like
// /run/current-system/sw/bin/...) gets re-resolved fresh at restore time,
// rather than baking in whatever happened to be true at capture time.
func resolveLaunchCommand(appID string, procCmdline []string) []string {
	if candidate := commandNameFromAppID(appID); candidate != "" {
		if _, err := exec.LookPath(candidate); err == nil {
			return []string{candidate}
		}
	}
	// Fall back to whatever we captured from /proc -- still useful for
	// apps whose app_id doesn't cleanly map to a binary name, even though
	// it carries the Nix-store fragility risk described above. A future
	// phase (Part 11 / Application Providers) is the right place for a
	// user-configurable override map, similar to nirinit's `launch` config
	// section, for the cases this heuristic can't resolve.
	return procCmdline
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
	"konsole":               true,
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
