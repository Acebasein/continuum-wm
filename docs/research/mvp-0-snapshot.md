# MVP 0 — Niri Snapshot Validation

## Status

**PASS**

MVP 0 successfully demonstrated that Continuum-WM can observe a live Niri
session through public compositor interfaces, identify the focused workspace,
normalize its state into a compositor-independent Continuum model, and persist
that state locally.

---

## 1. Objective

The objective of MVP 0 was to prove the first part of the Continuum-WM
architecture:

> Continuum-WM can observe a live compositor session and convert compositor
> state into its own normalized representation.

This stage intentionally does not perform restoration, matching, application
launching, or reconciliation.

For MVP 0, the currently focused Niri workspace temporarily represents the
future Continuum "favorite workspace".

---

## 2. Implementation

The implemented flow is:

    Niri
      |
      v
    Niri public JSON IPC
      |
      v
    Niri Adapter
      |
      v
    Continuum normalized model
      |
      v
    snapshot.json

Niri is queried using its public JSON interface.

Continuum queries:

- workspaces
- windows

The adapter identifies the focused workspace and selects only windows belonging
to that workspace.

The Niri-specific data is then converted into Continuum-owned snapshot
structures before persistence.

Raw Niri JSON is not stored directly as Continuum's persistent state model.

---

## 3. Snapshot Location

The snapshot is stored according to the XDG user-state convention.

When `XDG_STATE_HOME` is defined:

    $XDG_STATE_HOME/continuum-wm/snapshot.json

Otherwise Continuum falls back to:

    ~/.local/state/continuum-wm/snapshot.json

This keeps runtime state outside the source repository and user configuration
directories.

---

## 4. Normalized Snapshot Model

The MVP 0 snapshot contains:

### Snapshot

- schema version
- capture timestamp
- workspace

### Workspace

- compositor runtime ID
- visible workspace index
- optional workspace name
- output
- windows

### Window

- compositor runtime ID
- application ID
- PID
- title
- workspace runtime ID
- focused state
- floating state
- layout information

### Layout

Where available from Niri:

- scrolling-layout position
- tile size
- window size
- position in workspace view
- window offset inside tile

The normalized model intentionally separates compositor runtime identifiers
from user-visible workspace positions.

---

## 5. Live Validation

MVP 0 was tested against a real Niri session.

Continuum captured:

    Workspace runtime ID: 1
    Workspace index:      1
    Output:               eDP-2
    Windows:              2

The workspace contained:

### Ghostty

    Runtime window ID: 27
    App ID:            com.mitchellh.ghostty
    PID:               15759
    Focused:           true
    Floating:          false
    Scrolling position: [1, 1]

### Firefox

    Runtime window ID: 3
    App ID:            firefox
    PID:               2725
    Focused:           false
    Floating:          false
    Scrolling position: [2, 1]

The resulting snapshot correctly represented both windows and their relative
positions in Niri's scrolling layout.

---

## 6. Important Findings

### 6.1 Runtime workspace ID and workspace index are different concepts

Continuum stores these separately.

The compositor runtime ID must not be treated as the user-visible workspace
position.

This is particularly important because Niri workspace runtime IDs and workspace
indices are not guaranteed to have the same value.

---

### 6.2 JSON array order must not define workspace order

Previous live Niri investigation demonstrated that workspace objects returned
by Niri are not necessarily ordered according to their visible workspace
position.

Continuum therefore uses Niri's explicit workspace index rather than relying on
JSON array ordering.

---

### 6.3 Continuum owns the persistent model

The persisted snapshot is not a copy of Niri's JSON response.

Niri-specific state is translated into a normalized Continuum model.

This establishes the architectural boundary required for future compositor
adapters such as Hyprland.

---

### 6.4 Runtime IDs are observational identifiers

Niri workspace and window runtime IDs are useful for controlling objects during
the current compositor session.

They are not assumed to provide persistent identity across compositor restarts
or login sessions.

Persistent window identity remains a later MVP concern.

---

### 6.5 Layout information is observable

Niri exposes enough information to capture useful scrolling-layout state,
including column/tile position and geometry.

Earlier experiments demonstrated that this information can also be manipulated
through Niri's public IPC.

MVP 0 confirms that the required information can be normalized and persisted.

---

## 7. Validation

The implementation successfully passed:

    cargo build
    cargo run
    cargo fmt --check
    cargo clippy -- -D warnings

The generated snapshot was manually inspected and matched the actual focused
workspace and its windows.

No compositor plugin, private API, elevated privilege, or polling mechanism was
required.

---

## 8. MVP 0 Result

**MVP 0 — PASS**

Continuum-WM has demonstrated the ability to:

- observe Niri through public interfaces
- identify the focused workspace
- enumerate its windows
- capture useful layout information
- normalize compositor-specific state
- persist the normalized state locally
- maintain a clean separation between Niri and the Continuum data model

This validates the observation and snapshot foundation required for the next
stage.

---

## 9. Next Stage

**MVP 1 — Persistence**

MVP 1 will validate the reverse side of the persistence boundary:

    snapshot.json
          |
          v
        load
          |
          v
      deserialize
          |
          v
    validate schema
          |
          v
    Continuum Snapshot

The objective is to prove that persisted Continuum state can be safely loaded
and validated independently of the live compositor.
