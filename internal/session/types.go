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
//
// Bumped to 2 for Phase 7 (Layout/Size Restoration): Workspace.Entities
// (flat) is replaced by Workspace.Columns (nested), since real testing
// confirmed niri's scrolling layout genuinely groups windows into
// columns, and WIDTH IS A PROPERTY OF THE COLUMN, SHARED BY EVERY WINDOW
// IN IT -- not a per-window property. A flat entity list had no way to
// represent that correctly. Old (v1) session files are correctly rejected
// by Load() rather than silently misread under the new shape.
const SchemaVersion = 2

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

	// IdxHint is the workspace's position (1-based) on its output at
	// capture time, used as a best-effort niri workspace "reference" for
	// restore actions. IMPORTANT: niri's own documentation is explicit
	// that workspace index is NOT stable identity -- it refers to
	// "whichever workspace currently happens to be at this position,"
	// and can point to a different workspace entirely if workspaces have
	// been reordered since capture. This is an approximation, consistent
	// with the design doc's guidance to prefer a deterministic
	// approximation over pretending exact restoration is guaranteed.
	IdxHint uint8 `yaml:"idx_hint"`

	// IsFavorite marks the startup workspace (Part 10 of the requirements
	// doc / Part 9 of the design doc). Not used yet in Phase 2 -- captured
	// now so the field exists and defaults sensibly before Phase 6 needs it.
	IsFavorite bool `yaml:"is_favorite"`

	Columns []Column `yaml:"columns"`
}

// Column is one saved niri scrolling-layout column: an ordered group of
// entities stacked vertically, sharing one width.
//
// CONFIRMED VIA REAL TESTING (Phase 7 research), not assumed:
//   - Width is a property of the COLUMN, shared by every window inside it
//     -- manually resizing one window's width in a shared column resized
//     every other window in that column identically. There is no such
//     thing as two windows in the same column with different widths.
//   - Height is independent PER WINDOW within a column, and redistributes
//     as a zero-sum split of the column's available space when windows
//     are added/removed or explicitly resized (confirmed: growing one
//     window's height by exactly N logical pixels shrank the other
//     window in the same column by exactly N pixels).
//   - Column membership and relative order survive a cross-monitor move
//     (`move-column-to-monitor`) intact.
type Column struct {
	// PersistentID is OUR identity for this column, generated once at
	// capture. Not derived from niri's own column index, which is
	// positional and can shift as other columns are added/removed.
	PersistentID string `yaml:"persistent_id"`

	// ColumnIndex is the column's captured position (1-based, from niri's
	// pos_in_scrolling_layout[0]) within its workspace at capture time.
	// ADVISORY ONLY, like Workspace.IdxHint -- niri's own position numbers
	// are relative/positional, not stable identity. Used as a best-effort
	// ordering hint for restore (process columns in ascending order), not
	// as something to address a specific column by after the fact.
	ColumnIndex int `yaml:"column_index,omitempty"`

	// WidthFraction is this column's captured width, expressed as a
	// fraction of its output's LOGICAL width (matching exactly what niri's
	// own `set-column-width <N>%` action computes its percentage against
	// -- confirmed experimentally, including the small systematic offset
	// niri's own gap/border accounting introduces, which is expected and
	// not something we try to correct for).
	//
	// A nil pointer means we could not confidently determine this (e.g.
	// missing layout data) -- restore must treat this as "leave the
	// column's default width alone," never guess a value. This is the
	// same "when uncertain, do not guess" rule used throughout the
	// project (see CWDConfidence).
	WidthFraction *float64 `yaml:"width_fraction,omitempty"`

	// Entities are ordered by their captured row within this column
	// (ascending RowInColumn), top to bottom.
	Entities []Entity `yaml:"entities"`
}

// AllEntities returns every entity across every column in this workspace,
// in capture order (column then row) -- a convenience for callers that
// only need "every entity in this workspace" without needing
// column/width grouping (e.g. show, counting, lookup-by-id). Restore
// logic that needs to respect column structure (Phase 7) should iterate
// ws.Columns directly instead.
func (ws Workspace) AllEntities() []Entity {
	var all []Entity
	for _, col := range ws.Columns {
		all = append(all, col.Entities...)
	}
	return all
}

// Entity is one saved window.
type Entity struct {
	// PersistentID is OUR identity for this window, generated once at
	// first capture. Never derived from niri's window id or the process's
	// PID -- both are runtime-only (see design doc, section on identity).
	PersistentID string `yaml:"persistent_id"`

	AppID string `yaml:"app_id"`
	Title string `yaml:"title"`

	// Launch is best-effort, captured from /proc/<pid>/cmdline at the
	// moment of capture (see internal/procinfo). It may be empty if we
	// couldn't read it -- an entity with no launch command captured simply
	// cannot be restored yet, which is the correct, honest outcome rather
	// than guessing a command.
	Launch LaunchSpec `yaml:"launch,omitempty"`

	// ProviderMetadata holds app-specific captured data -- currently just
	// working directory, for terminal-like apps. See CWDConfidence: an
	// empty CWD with confidence "unknown" is a deliberate, honest outcome,
	// never a guess (design doc, Part 6).
	ProviderMetadata ProviderMetadata `yaml:"provider_metadata,omitempty"`

	// RowInColumn is this entity's captured row (1-based, from niri's
	// pos_in_scrolling_layout[1]) within its column at capture time.
	// ADVISORY, positional -- same caveat as Column.ColumnIndex. Used to
	// order restore within a column (top to bottom), not as stable
	// identity.
	RowInColumn int `yaml:"row_in_column,omitempty"`

	// HeightFraction is this entity's captured height, expressed as a
	// fraction of its output's LOGICAL height -- matching what niri's own
	// `set-window-height --id <id> <N>%` computes its percentage against
	// (confirmed experimentally). Unlike width, height is genuinely
	// per-entity even within a shared column: confirmed that niri
	// redistributes a column's height as a zero-sum split between its
	// windows, and that redistribution carries over sensibly across a
	// cross-monitor move (the same proportional split was preserved
	// against the new output's total, not the old one).
	//
	// A nil pointer means "leave this window's height alone, don't
	// guess" -- same rule as WidthFraction.
	HeightFraction *float64 `yaml:"height_fraction,omitempty"`

	// LastSeen is advisory/debugging information only. It must never be
	// treated as identity across a restart -- niri assigns new window IDs
	// every session.
	LastSeen LastSeen `yaml:"last_seen"`
}

// LaunchSpec is the command used to relaunch this entity's application.
type LaunchSpec struct {
	Command []string `yaml:"command,omitempty"`
}

// CWDConfidence describes how much we trust ProviderMetadata.CWD.
type CWDConfidence string

const (
	// CWDHigh: this window's process ID belonged to exactly one window at
	// capture time, so reading /proc/<pid>/cwd unambiguously describes it.
	CWDHigh CWDConfidence = "high"

	// CWDUnknown: either we couldn't read /proc/<pid>/cwd at all, or --
	// importantly -- this PID was shared by more than one window at
	// capture time (as confirmed happening with GNOME/GTK single-instance
	// apps like Nautilus, and possibly Ghostty), making it impossible to
	// know which window the directory actually belongs to. Per the design
	// doc's core rule, an unknown CWD must never be guessed at restore
	// time -- it means "launch the app, but do not attempt to set a
	// working directory."
	CWDUnknown CWDConfidence = "unknown"
)

// ProviderMetadata holds application-specific captured data. Phase 4 only
// populates the CWD fields (generic terminal handling); richer per-app
// providers are Phase 8's job (design doc, Part 11).
type ProviderMetadata struct {
	CWD           string        `yaml:"cwd,omitempty"`
	CWDConfidence CWDConfidence `yaml:"cwd_confidence,omitempty"`
}

// LastSeen records what we observed at capture time, for debugging only.
type LastSeen struct {
	NiriWindowID uint64    `yaml:"niri_window_id"`
	CapturedAt   time.Time `yaml:"captured_at"`
}
