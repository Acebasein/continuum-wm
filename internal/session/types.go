// Package session defines Continuum-WM's persistent, on-disk session
// model, and how to save/load it.
//
// Phase 2 scope, deliberately: this captures WHICH windows exist, WHAT app
// they belong to, and WHICH workspace they're in -- the minimum needed to
// prove capture + persist + reload works correctly. It does NOT yet
// capture layout/size details (Part 10 of the design doc) or
// application-specific metadata like terminal working directory (Part 6) --
// those are later phases, once we've properly analyzed the real data
// rather than guessing at a schema for it now.
package session

import "time"

// SchemaVersion is written into every saved session file. Bump this and
// add explicit migration logic whenever the schema shape changes, so old
// session files never silently misparse under a newer version of
// Continuum-WM.
const SchemaVersion = 1

// Session is the root of a saved Continuum-WM session.
type Session struct {
	SchemaVersion int       `yaml:"schema_version"`
	SessionID     string    `yaml:"session_id"`
	CreatedAt     time.Time `yaml:"created_at"`
	UpdatedAt     time.Time `yaml:"updated_at"`
	Compositor    string    `yaml:"compositor"`

	Workspaces []Workspace `yaml:"workspaces"`
}

// Workspace is one saved workspace and the entities (windows) it contains.
type Workspace struct {
	// PersistentID is OUR identity for this workspace, generated once at
	// first capture. Never derived from niri's own workspace id.
	PersistentID string `yaml:"persistent_id"`

	// OutputHint is best-effort ("which monitor was this on") -- see the
	// design doc's note that monitor configuration can change between
	// sessions, so this is a hint for restore, not a guarantee.
	OutputHint string `yaml:"output_hint,omitempty"`

	// IsFavorite marks the startup workspace (Part 10 of the requirements
	// doc / Part 9 of the design doc). Not used yet in Phase 2 -- captured
	// now so the field exists and defaults sensibly before Phase 6 needs it.
	IsFavorite bool `yaml:"is_favorite"`

	Entities []Entity `yaml:"entities"`
}

// Entity is one saved window.
type Entity struct {
	// PersistentID is OUR identity for this window, generated once at
	// first capture. Never derived from niri's window id or the process's
	// PID -- both are runtime-only (see design doc, section on identity).
	PersistentID string `yaml:"persistent_id"`

	AppID string `yaml:"app_id"`
	Title string `yaml:"title"`

	// LastSeen is advisory/debugging information only. It must never be
	// treated as identity across a restart -- niri assigns new window IDs
	// every session.
	LastSeen LastSeen `yaml:"last_seen"`
}

// LastSeen records what we observed at capture time, for debugging only.
type LastSeen struct {
	NiriWindowID uint64    `yaml:"niri_window_id"`
	CapturedAt   time.Time `yaml:"captured_at"`
}
