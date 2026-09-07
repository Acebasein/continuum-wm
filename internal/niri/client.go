package niri

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
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

	wrapped := append([]string{"setsid", "--fork", "--"}, command...)
	cmd := exec.Command(wrapped[0], wrapped[1:]...)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Dir = workDir
	// No SysProcAttr.Setsid here -- `setsid --fork` already handles full
	// session detachment; adding our own on top is redundant at best and
	// avoided for clarity, not because it was confirmed harmful.

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
