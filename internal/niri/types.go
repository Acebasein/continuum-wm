// Package niri contains types and helpers for talking to a running Niri
// compositor over its `niri msg` JSON IPC.
//
// IMPORTANT: Niri's own docs state that new fields and new event variants
// will be added over time, in patch releases. This package is written to
// tolerate that: unknown JSON fields are ignored by default (Go's
// encoding/json already does this), and unknown *event variants* are
// explicitly handled as a distinct, non-fatal case rather than a parse
// error. See events.go.
package niri

// Workspace mirrors niri-ipc's Workspace struct.
//
// id is a durable-within-a-running-compositor identifier (NOT persistent
// across restarts of niri itself, and NEVER to be treated as Continuum-WM's
// own persistent session identity -- see the project's core identity rule).
type Workspace struct {
	ID             uint64  `json:"id"`
	Idx            uint8   `json:"idx"`
	Name           *string `json:"name"`
	Output         *string `json:"output"`
	IsUrgent       bool    `json:"is_urgent"`
	IsActive       bool    `json:"is_active"`
	IsFocused      bool    `json:"is_focused"`
	ActiveWindowID *uint64 `json:"active_window_id"`
}

// WindowLayout mirrors niri-ipc's WindowLayout struct. Fields here are the
// ones Phase 1 needs to simply observe and print -- Phase 7 will decide how
// much of this is meaningfully restorable (see the project design doc,
// Part 10: exact pixel restoration is NOT assumed to be guaranteed).
type WindowLayout struct {
	// PosInScrollingLayout is the window's (column, position-in-column)
	// coordinate in niri's scrolling layout, when applicable.
	PosInScrollingLayout *[2]uint64 `json:"pos_in_scrolling_layout"`
	// TileSize is (width, height) of the tile, in logical pixels, as a
	// floating point pair -- niri reports this as f64 tuple.
	TileSize [2]float64 `json:"tile_size"`
	// WindowSize is (width, height) of the window itself, in logical pixels.
	WindowSize [2]int64 `json:"window_size"`
}

// Window mirrors niri-ipc's Window struct.
type Window struct {
	ID          uint64        `json:"id"`
	Title       *string       `json:"title"`
	AppID       *string       `json:"app_id"`
	PID         *int32        `json:"pid"`
	WorkspaceID *uint64       `json:"workspace_id"`
	IsFocused   bool          `json:"is_focused"`
	IsFloating  bool          `json:"is_floating"`
	IsUrgent    bool          `json:"is_urgent"`
	Layout      *WindowLayout `json:"layout"`
}
