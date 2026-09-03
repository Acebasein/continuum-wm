package niri

import (
	"encoding/json"
	"fmt"
)

// EventKind identifies which variant an Event is, without requiring the
// caller to already know the full set of variants niri might send.
type EventKind string

const (
	EventWorkspacesChanged            EventKind = "WorkspacesChanged"
	EventWorkspaceActivated           EventKind = "WorkspaceActivated"
	EventWorkspaceActiveWindowChanged EventKind = "WorkspaceActiveWindowChanged"
	EventWorkspaceUrgencyChanged      EventKind = "WorkspaceUrgencyChanged"
	EventWindowsChanged               EventKind = "WindowsChanged"
	EventWindowOpenedOrChanged        EventKind = "WindowOpenedOrChanged"
	EventWindowClosed                 EventKind = "WindowClosed"
	EventWindowFocusChanged           EventKind = "WindowFocusChanged"
	EventWindowUrgencyChanged         EventKind = "WindowUrgencyChanged"
	EventWindowLayoutsChanged         EventKind = "WindowLayoutsChanged"
	EventOverviewOpenedOrClosed       EventKind = "OverviewOpenedOrClosed"
	EventConfigLoaded                 EventKind = "ConfigLoaded"
	EventScreenshotCaptured           EventKind = "ScreenshotCaptured"
  EventKeyboardLayoutsChanged       EventKind = "KeyboardLayoutsChanged"
  EventWindowFocusTimestampChanged  EventKind = "WindowFocusTimestampChanged"

	// EventUnknown is deliberately NOT an error. Niri's own docs promise new
	// variants will be added over time (a real one, "CastsChanged", already
	// broke at least one downstream tool that parsed strictly). Phase 1's
	// whole purpose is to prove we can observe reliably -- so an unknown
	// variant must be a visible, loggable, non-fatal case, never a crash.
	EventUnknown EventKind = "Unknown"
)

// Event is a parsed niri event-stream line. Exactly one of the typed
// payload fields is populated, matching Kind. RawPayload always contains
// the original payload bytes, which is useful both for debugging and as a
// fallback for variants we haven't modeled yet.
type Event struct {
	Kind       EventKind
	RawTagName string // the literal JSON key niri used, e.g. "CastsChanged"
	RawPayload json.RawMessage

	WorkspacesChanged     *EvtWorkspacesChanged
	WorkspaceActivated    *EvtWorkspaceActivated
	WindowsChanged        *EvtWindowsChanged
	WindowOpenedOrChanged *EvtWindowOpenedOrChanged
	WindowClosed          *EvtWindowClosed
}

type EvtWorkspacesChanged struct {
	Workspaces []Workspace `json:"workspaces"`
}

type EvtWorkspaceActivated struct {
	ID      uint64 `json:"id"`
	Focused bool   `json:"focused"`
}

type EvtWindowsChanged struct {
	Windows []Window `json:"windows"`
}

type EvtWindowOpenedOrChanged struct {
	Window Window `json:"window"`
}

type EvtWindowClosed struct {
	ID uint64 `json:"id"`
}

// ParseEvent decodes a single line of `niri msg --json event-stream`
// output. Events are externally tagged: {"WindowClosed": {"id": 42}}. We
// therefore first unmarshal into a map with exactly one key to find out
// which variant we're looking at, then decode just that variant's payload.
//
// This function never returns an error for an unrecognized variant name --
// only for genuinely malformed JSON. That distinction matters: malformed
// JSON is a real problem worth surfacing loudly, but an unrecognized (new)
// variant is expected, normal, forward-compatible behavior.
func ParseEvent(line []byte) (Event, error) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(line, &wrapper); err != nil {
		return Event{}, fmt.Errorf("malformed event JSON: %w", err)
	}
	if len(wrapper) != 1 {
		return Event{}, fmt.Errorf("expected exactly one top-level key in event, got %d", len(wrapper))
	}

	var tag string
	var payload json.RawMessage
	for k, v := range wrapper {
		tag = k
		payload = v
	}

	ev := Event{
		Kind:       EventKind(tag),
		RawTagName: tag,
		RawPayload: payload,
	}

	switch ev.Kind {
	case EventWorkspacesChanged:
		var p EvtWorkspacesChanged
		if err := json.Unmarshal(payload, &p); err != nil {
			return Event{}, fmt.Errorf("parsing %s payload: %w", tag, err)
		}
		ev.WorkspacesChanged = &p

	case EventWorkspaceActivated:
		var p EvtWorkspaceActivated
		if err := json.Unmarshal(payload, &p); err != nil {
			return Event{}, fmt.Errorf("parsing %s payload: %w", tag, err)
		}
		ev.WorkspaceActivated = &p

	case EventWindowsChanged:
		var p EvtWindowsChanged
		if err := json.Unmarshal(payload, &p); err != nil {
			return Event{}, fmt.Errorf("parsing %s payload: %w", tag, err)
		}
		ev.WindowsChanged = &p

	case EventWindowOpenedOrChanged:
		var p EvtWindowOpenedOrChanged
		if err := json.Unmarshal(payload, &p); err != nil {
			return Event{}, fmt.Errorf("parsing %s payload: %w", tag, err)
		}
		ev.WindowOpenedOrChanged = &p

	case EventWindowClosed:
		var p EvtWindowClosed
		if err := json.Unmarshal(payload, &p); err != nil {
			return Event{}, fmt.Errorf("parsing %s payload: %w", tag, err)
		}
		ev.WindowClosed = &p

	case EventWorkspaceActiveWindowChanged, EventWorkspaceUrgencyChanged,
		EventWindowFocusChanged, EventWindowUrgencyChanged,
		EventWindowLayoutsChanged, EventOverviewOpenedOrClosed,
		EventConfigLoaded, EventScreenshotCaptured,
		EventKeyboardLayoutsChanged, EventWindowFocusTimestampChanged:
		// Recognized by name, but Phase 1 doesn't need a typed payload for
		// these yet -- RawPayload is still available if we need it later.

	default:
		// A genuinely unknown variant -- expected and safe. Reclassify so
		// callers can branch on it explicitly instead of silently matching
		// "default" in their own switch statements.
		ev.Kind = EventUnknown
	}

	return ev, nil
}
