package niri

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
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

// EventStream starts `niri msg --json event-stream` as a long-running
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
