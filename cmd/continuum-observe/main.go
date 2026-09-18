// continuum-observe is the Phase 1 experiment for Continuum-WM.
//
// It does exactly one thing: prove that we can reliably read Niri's live
// state and event stream from Go, continuously, without drift or crashes.
//
// It NEVER calls any mutating `niri msg action ...` command. There is no
// code path in this file capable of changing compositor state -- that's
// intentional, not an oversight, per the project's Phase 1 acceptance gate.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"continuum-wm/internal/niri"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := &niri.Client{}

	if err := printBaseline(ctx, client); err != nil {
		fmt.Fprintf(os.Stderr, "baseline read failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "(are you running this inside a Niri session? is `niri` on PATH?)")
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("--- watching live event stream (Ctrl-C to stop) ---")
	fmt.Println()

	events, errs, err := client.EventStream(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting event stream failed: %v\n", err)
		os.Exit(1)
	}

	eventCount := 0
	unknownCount := 0
	errCount := 0

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				fmt.Println("event stream closed.")
				printSummary(eventCount, unknownCount, errCount)
				return
			}
			eventCount++
			if ev.Kind == niri.EventUnknown {
				unknownCount++
			}
			printEvent(ev)

		case err, ok := <-errs:
			if !ok {
				continue // errs channel closes alongside events; let events drive the loop
			}
			errCount++
			fmt.Fprintf(os.Stderr, "[%s] PARSE ERROR: %v\n", timestamp(), err)

		case <-ctx.Done():
			fmt.Println("\nstopping (signal received)...")
			printSummary(eventCount, unknownCount, errCount)
			return
		}
	}
}

func printBaseline(ctx context.Context, c *niri.Client) error {
	fmt.Println("--- baseline snapshot ---")

	workspaces, err := c.Workspaces(ctx)
	if err != nil {
		return fmt.Errorf("reading workspaces: %w", err)
	}
	windows, err := c.Windows(ctx)
	if err != nil {
		return fmt.Errorf("reading windows: %w", err)
	}

	fmt.Printf("%d workspace(s):\n", len(workspaces))
	for _, ws := range workspaces {
		name := "<unnamed>"
		if ws.Name != nil {
			name = *ws.Name
		}
		output := "<no output>"
		if ws.Output != nil {
			output = *ws.Output
		}
		active := ""
		if ws.IsActive {
			active = " [active]"
		}
		focused := ""
		if ws.IsFocused {
			focused = " [focused]"
		}
		fmt.Printf("  ws#%d idx=%d name=%s output=%s%s%s\n",
			ws.ID, ws.Idx, name, output, active, focused)
	}

	fmt.Printf("%d window(s):\n", len(windows))
	for _, w := range windows {
		title := "<no title>"
		if w.Title != nil {
			title = *w.Title
		}
		appID := "<no app_id>"
		if w.AppID != nil {
			appID = *w.AppID
		}
		wsID := "<none>"
		if w.WorkspaceID != nil {
			wsID = fmt.Sprintf("%d", *w.WorkspaceID)
		}
		fmt.Printf("  win#%d app_id=%s title=%q workspace=%s\n",
			w.ID, appID, title, wsID)
	}

	return nil
}

func printEvent(ev niri.Event) {
	ts := timestamp()
	switch ev.Kind {
	case niri.EventWindowOpenedOrChanged:
		w := ev.WindowOpenedOrChanged.Window
		appID := "<no app_id>"
		if w.AppID != nil {
			appID = *w.AppID
		}
		title := "<no title>"
		if w.Title != nil {
			title = *w.Title
		}
		fmt.Printf("[%s] WindowOpenedOrChanged  win#%d app_id=%s title=%q\n", ts, w.ID, appID, title)

	case niri.EventWindowClosed:
		fmt.Printf("[%s] WindowClosed            win#%d\n", ts, ev.WindowClosed.ID)

	case niri.EventWorkspaceActivated:
		fmt.Printf("[%s] WorkspaceActivated      ws#%d focused=%v\n", ts, ev.WorkspaceActivated.ID, ev.WorkspaceActivated.Focused)

	case niri.EventWorkspacesChanged:
		fmt.Printf("[%s] WorkspacesChanged       (%d workspaces)\n", ts, len(ev.WorkspacesChanged.Workspaces))

	case niri.EventWindowsChanged:
		fmt.Printf("[%s] WindowsChanged          (%d windows)\n", ts, len(ev.WindowsChanged.Windows))

	case niri.EventUnknown:
		fmt.Printf("[%s] UNKNOWN EVENT VARIANT   tag=%q payload=%s\n", ts, ev.RawTagName, string(ev.RawPayload))

	default:
		fmt.Printf("[%s] %-24s(no typed payload in Phase 1)\n", ts, ev.Kind)
	}
}

func printSummary(total, unknown, errs int) {
	fmt.Println()
	fmt.Println("--- session summary ---")
	fmt.Printf("events observed: %d\n", total)
	fmt.Printf("unknown variants: %d\n", unknown)
	fmt.Printf("parse errors: %d\n", errs)
	if errs > 0 {
		fmt.Println("FAIL: parse errors occurred -- see Phase 1 acceptance gate.")
	} else {
		fmt.Println("No parse errors observed this session.")
	}
}

func timestamp() string {
	return time.Now().Format("15:04:05.000")
}
