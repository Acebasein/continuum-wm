package niri

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// Client talks to niri exclusively via the `niri msg` CLI wrapper around
// the IPC socket. Phase 1 deliberately does not talk to $NIRI_SOCKET
// directly -- see the project design doc, Part 17/"First Implementation
// Step": we want to validate the *data shape* before also taking on
// transport-level complexity. Swapping to a direct socket connection later
// only requires changes inside this file.
type Client struct {
	// NiriBin is the path to the niri binary. Defaults to "niri", resolved
	// via $PATH, if empty.
	NiriBin string
}

func (c *Client) bin() string {
	if c.NiriBin == "" {
		return "niri"
	}
	return c.NiriBin
}

// Output mirrors the relevant fields of niri-ipc's Output struct.
// Confirmed via real `niri msg --json outputs` output that the top-level
// JSON is an OBJECT keyed by output name (e.g. {"eDP-2": {...}}), not an
// array like Workspaces/Windows -- different shape, handled accordingly
// in Outputs() below.
// Output is niri's own reported state for one display output.
//
// Name is the CURRENT, VOLATILE connector name (e.g. "eDP-1", "eDP-2") --
// confirmed via direct testing that this can change even WITHIN a single
// boot on some hardware/desktop-shell combinations, not just across
// separate reboots. Per the persistent-monitor-identity design, Name must
// never be treated as a durable identity by callers -- it's a
// current-session routing handle only. The Make/Model/Serial/
// PhysicalWidthMM/PhysicalHeightMM fields below are what should actually
// be persisted as identity evidence; Name is diagnostic/runtime-only.
type Output struct {
	Name    string         `json:"name"`
	Logical *LogicalRegion `json:"logical"`

	// Hardware identity evidence -- see MonitorIdentity in the session
	// package for how these get persisted and matched against on restore.
	Make   string  `json:"make"`
	Model  string  `json:"model"`
	Serial *string `json:"serial"` // often null, esp. on internal laptop panels -- confirmed directly on this project's own test hardware

	// PhysicalSizeMM is [width, height] in millimeters, as niri reports
	// it -- used as supporting identity evidence when Serial is
	// unavailable (see the design doc's matching-priority discussion).
	PhysicalSizeMM [2]int `json:"physical_size"`
}

// LogicalRegion is an output's logical (post-scale) position and size --
// the numbers that matter for our width/height fraction math, confirmed
// experimentally to match what niri's own set-column-width/
// set-window-height percentage arguments are computed against.
type LogicalRegion struct {
	X      int     `json:"x"`
	Y      int     `json:"y"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	Scale  float64 `json:"scale"`
}

// Outputs runs `niri msg --json outputs` and returns a map keyed by output
// name (e.g. "eDP-2", "HDMI-A-3").
func (c *Client) Outputs(ctx context.Context) (map[string]Output, error) {
	out, err := exec.CommandContext(ctx, c.bin(), "msg", "--json", "outputs").Output()
	if err != nil {
		return nil, fmt.Errorf("niri msg --json outputs: %w", err)
	}
	var outputs map[string]Output
	if err := json.Unmarshal(out, &outputs); err != nil {
		return nil, fmt.Errorf("parsing outputs JSON: %w", err)
	}
	return outputs, nil
}

// Workspaces runs `niri msg --json workspaces` once and returns the result.
//
// NOTE: niri's own IPC documentation warns that separate requests like
// Workspaces and Windows are NOT guaranteed to be consistent with each
// other -- state can change between the two calls. Treat a Workspaces()+
// Windows() pair as a best-effort snapshot, not an atomic one. For a
// guaranteed-consistent view, prefer the event stream's initial
// WorkspacesChanged/WindowsChanged events (see EventStream below), which
// niri sends as an atomic "current state" before any incremental updates.
func (c *Client) Workspaces(ctx context.Context) ([]Workspace, error) {
	out, err := exec.CommandContext(ctx, c.bin(), "msg", "--json", "workspaces").Output()
	if err != nil {
		return nil, fmt.Errorf("niri msg --json workspaces: %w", err)
	}
	var ws []Workspace
	if err := json.Unmarshal(out, &ws); err != nil {
		return nil, fmt.Errorf("parsing workspaces JSON: %w", err)
	}
	return ws, nil
}

// Windows runs `niri msg --json windows` once and returns the result.
// See the consistency note on Workspaces above.
func (c *Client) Windows(ctx context.Context) ([]Window, error) {
	out, err := exec.CommandContext(ctx, c.bin(), "msg", "--json", "windows").Output()
	if err != nil {
		return nil, fmt.Errorf("niri msg --json windows: %w", err)
	}
	var wins []Window
	if err := json.Unmarshal(out, &wins); err != nil {
		return nil, fmt.Errorf("parsing windows JSON: %w", err)
	}
	return wins, nil
}

// MoveWindowToMonitor moves the window with the given id to the output
// (monitor) named by outputName.
//
// WHY THIS MATTERS (confirmed via real multi-monitor testing): niri's own
// docs state that a bare index reference (used by MoveWindowToWorkspace)
// "refers to whichever workspace currently happens to be at this position
// on the focused monitor" -- but confirmed EXPERIMENTALLY that this
// applies to the no-window-id default case, NOT when --window-id is given
// explicitly. When --window-id is given, the index resolves against THAT
// WINDOW'S OWN current output, regardless of which monitor is globally
// focused. This means: to correctly place a window onto a specific saved
// (output, idx) pair, we must first ensure the window is actually ON that
// output -- via this action -- before issuing MoveWindowToWorkspace with
// a bare idx. Skipping this step risks silently landing a window on the
// wrong monitor's workspace of the same index, with no error at all.
//
// Verified against `niri msg action move-window-to-monitor --help`: the
// flag is `--id` here, NOT `--window-id` like MoveWindowToWorkspace uses
// -- confirmed inconsistent naming between the two actions, don't assume
// they match.
func (c *Client) MoveWindowToMonitor(ctx context.Context, windowID uint64, outputName string) error {
	args := []string{
		"msg", "action", "move-window-to-monitor",
		"--id", fmt.Sprintf("%d", windowID),
		outputName,
	}
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action move-window-to-monitor failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// ConsumeWindowIntoColumn consumes the window immediately to the right of
// the FOCUSED column into that column. CONFIRMED VIA REAL TESTING: this
// action has NO targeting option whatsoever -- not even the --id-less
// "acts on focused window" pattern SetColumnWidth uses; it operates purely
// on "whatever column is focused" + "whatever window is immediately to
// its right." Callers MUST verify live adjacency (via each window's
// pos_in_scrolling_layout) immediately before calling this, and must be
// prepared to NOT call it at all if adjacency can't be confirmed --
// guessing here risks silently merging the wrong window into a column.
func (c *Client) ConsumeWindowIntoColumn(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, c.bin(), "msg", "action", "consume-window-into-column").CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action consume-window-into-column failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// FocusWindow focuses the window with the given id. Confirmed necessary
// as a prerequisite for SetColumnWidth (see its doc comment) -- niri has
// no --id/--window-id option on set-column-width at all, unlike most
// other window-targeting actions, so focus is the ONLY way to target a
// specific column for width changes.
func (c *Client) FocusWindow(ctx context.Context, windowID uint64) error {
	args := []string{"msg", "action", "focus-window", "--id", fmt.Sprintf("%d", windowID)}
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action focus-window failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// SetColumnWidth changes the width of the FOCUSED column. change is
// passed through verbatim to niri (e.g. "50%" for a proportional width,
// or a bare number like "960" for a fixed pixel width) -- confirmed via
// niri's own documentation that these two forms are NOT interchangeable
// (proportional sizing includes the tile's border in the calculation,
// fixed-pixel sizing does not).
//
// CRITICAL, CONFIRMED VIA REAL TESTING: this action has NO window/column
// targeting option whatsoever -- unlike SetWindowHeight below, there is
// no --id here. It ALWAYS affects whatever column currently has focus,
// regardless of caller intent. Callers MUST call FocusWindow on a window
// in the intended column immediately before calling this, and should
// restore whatever was focused before once done, to avoid leaving the
// user's visible focus somewhere they didn't put it.
func (c *Client) SetColumnWidth(ctx context.Context, change string) error {
	args := []string{"msg", "action", "set-column-width", change}
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action set-column-width failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// SetWindowHeight changes the height of the window with the given id.
// change follows the same "50%" vs fixed-pixel-number rules as
// SetColumnWidth's change parameter.
//
// CONFIRMED VIA REAL TESTING: unlike SetColumnWidth, this action's --id
// option genuinely targets the specified window independent of which
// window currently has focus -- no focus-stealing needed here at all.
func (c *Client) SetWindowHeight(ctx context.Context, windowID uint64, change string) error {
	args := []string{"msg", "action", "set-window-height", "--id", fmt.Sprintf("%d", windowID), change}
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action set-window-height failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// MoveWindowToWorkspace moves the window with the given id to the
// workspace identified by reference (a workspace index or name, matching
// niri's own addressing scheme -- see design doc Part 10/17 for why exact
// pixel/geometry addressing isn't used here).
//
// focus=false means the user's current focus is left alone -- important
// for Continuum-WM, since restore actions happen in the background and
// should not yank focus away from whatever the person is actually doing.
//
// Verified against `niri msg action move-window-to-workspace --help` and
// `niri msg action focus-window --help` on 2026-09-03 -- see conversation
// history / commit message for the exact output. Do not assume these flags
// without re-checking if you're reading this after a niri upgrade.
func (c *Client) MoveWindowToWorkspace(ctx context.Context, windowID uint64, reference string, focus bool) error {
	args := []string{
		"msg", "action", "move-window-to-workspace",
		"--window-id", fmt.Sprintf("%d", windowID),
		"--focus", fmt.Sprintf("%t", focus),
		reference,
	}
	out, err := exec.CommandContext(ctx, c.bin(), args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("niri msg action move-window-to-workspace failed: %w (output: %s)", err, string(out))
	}
	return nil
}

// LaunchDetached starts command as a new, independent process -- NOT a
// child that continuum-cli waits on or is responsible for supervising.
// This matters because the launched application (e.g. Ghostty) should keep
// running after continuum-cli's restore command finishes; it should not be
// killed when our process exits, and it should not be tied to our
// process's stdin/stdout.
//
// workDir, if non-empty, sets the new process's initial working directory
// explicitly. THIS MATTERS: without it, a spawned process inherits
// continuum-cli's OWN current directory (standard Unix fork/exec
// behavior) -- confirmed experimentally to cause every restored terminal
// to open in whatever directory continuum-cli itself happened to be run
// from, regardless of each entity's saved CWD.
//
// If workDir is empty (no saved CWD is known/trusted for this entity --
// e.g. Ghostty, where we deliberately never guess per-window CWD), we
// still explicitly default to the user's home directory rather than
// leaving cmd.Dir unset. Confirmed this matters in practice: leaving it
// unset caused restored Ghostty windows to silently inherit whatever
// directory the OPERATOR happened to be running continuum-cli from at the
// time -- an accidental, invoker-dependent result, not a meaningful
// default. $HOME is at least predictable and doesn't depend on where the
// tool is invoked from, which also matters once this runs as a background
// daemon (Part 18) rather than something launched by hand from a shell.
//
// THE COMMAND IS WRAPPED WITH `setsid --fork --`. WHY THIS MATTERS,
// confirmed via real testing: some NixOS packages (confirmed with
// ONLYOFFICE) are distributed as a sandboxed FHS environment launched
// through bubblewrap with the flag --die-with-parent -- meaning the
// kernel kills the ENTIRE sandboxed process tree the instant its direct
// spawning parent exits. Our own syscall.Setsid (see detachedSysProcAttr)
// changes the new process's SESSION, but does not change which process
// bubblewrap is watching for this purpose -- continuum-cli itself remains
// that parent, so the app would open correctly and then be killed the
// moment continuum-cli's own process exited, which looked exactly like a
// silent crash until traced. `setsid --fork` genuinely forks an
// intermediary that exits immediately, so by the time the real
// application (and anything like bubblewrap underneath it) starts
// running, continuum-cli was never its parent at all. This is a general
// fix, not an ONLYOFFICE-specific one -- other NixOS packages using the
// same bwrap/--die-with-parent pattern (common for apps needing a fake
// FHS filesystem) would hit the identical bug.
func LaunchDetached(command []string, workDir string) (pid int32, err error) {
	if len(command) == 0 {
		return 0, fmt.Errorf("empty launch command")
	}
	if workDir == "" {
		if home, herr := os.UserHomeDir(); herr == nil {
			workDir = home
		}
		// If even UserHomeDir fails, workDir stays "" and cmd.Dir is left
		// unset below -- falling back to default inherited behavior as a
		// last resort, rather than failing the whole launch over it.
	}

	// CONFIRMED BUG, fixed here: this previously wrapped the target
	// command in an external `setsid --fork --` invocation and returned
	// THAT process's PID. setsid --fork's documented behavior is to fork
	// a NEW child (which becomes the session leader and execs the real
	// target), while the ORIGINAL setsid invocation -- whose PID was
	// being captured and returned here -- exits almost immediately once
	// the fork succeeds. This meant the PID our retry-duplication logic
	// checked for liveness (restore.go's processAlive) was essentially
	// guaranteed to already be dead moments after a successful launch,
	// completely independent of whether the real application was still
	// alive and simply slow -- confirmed as the root cause of duplicate
	// windows appearing under real load (a fresh reboot, several apps
	// starting at once).
	//
	// Fixed by using Go's own SysProcAttr.Setsid instead of an external
	// setsid binary: achieves the same session-detachment originally
	// needed (confirmed necessary so bubblewrap's --die-with-parent
	// doesn't kill ONLYOFFICE when continuum-cli exits) WITHOUT an
	// intermediate fork-and-exit process muddying the PID. cmd.Process.Pid
	// now tracks the actual launched command -- and any further
	// exec-based wrapper scripts (e.g. NixOS's own app wrappers, which
	// preserve PID across exec; only fork() changes PID, and a
	// conventional `exec real-binary "$@"` wrapper never forks).
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Dir = workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("starting %v: %w", command, err)
	}

	// Reap the process in the background once it exits, without blocking
	// our caller -- otherwise it becomes a zombie process once it exits,
	// since nothing else is waiting on it.
	go cmd.Wait()

	return int32(cmd.Process.Pid), nil
}

// subprocess and returns a channel of parsed events plus a channel of
// non-fatal parse errors (e.g. a single malformed line -- we keep reading
// rather than aborting the whole stream over one bad line). The returned
// error channel is NOT for unknown event variants; those come through as
// ordinary Event values with Kind == EventUnknown. It IS for genuine JSON
// parse failures, which are worth knowing about but shouldn't kill Phase 1's
// long-running observation session.
//
// The caller must consume both channels or the goroutine can block. Both
// channels are closed when the subprocess's stdout is closed (e.g. niri
// exits, or the context is canceled).
func (c *Client) EventStream(ctx context.Context) (<-chan Event, <-chan error, error) {
	cmd := exec.CommandContext(ctx, c.bin(), "msg", "--json", "event-stream")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("creating stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("starting niri msg event-stream: %w", err)
	}

	events := make(chan Event)
	errs := make(chan error)

	go func() {
		defer close(events)
		defer close(errs)
		defer cmd.Wait() // reap the process; errors from Wait aren't actionable here

		scanner := bufio.NewScanner(stdout)
		// niri event lines are small JSON objects, but be generous with the
		// buffer since WindowsChanged can list every open window.
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			// Copy the line -- scanner.Bytes() is reused on the next Scan().
			lineCopy := append([]byte(nil), line...)

			ev, perr := ParseEvent(lineCopy)
			if perr != nil {
				select {
				case errs <- perr:
				case <-ctx.Done():
					return
				}
				continue
			}
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
		if serr := scanner.Err(); serr != nil {
			select {
			case errs <- fmt.Errorf("reading event stream: %w", serr):
			case <-ctx.Done():
			}
		}
	}()

	return events, errs, nil
}
