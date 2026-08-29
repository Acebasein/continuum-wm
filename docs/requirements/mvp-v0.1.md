# Continuum-WM — MVP V0.1

## 1. Purpose

MVP V0.1 exists to validate the fundamental technical premise of
Continuum-WM:

> Can Continuum-WM reliably restore one favorite workspace after
> login or reboot using public compositor interfaces, without
> incorrectly manipulating unrelated windows?

This MVP is an engineering feasibility gate, not a production release.

If the MVP succeeds, development proceeds.

If the MVP demonstrates that reliable restoration cannot be achieved
without unsafe guessing, privileged mechanisms, compositor plugins, or
fragile implementation-specific hacks, the project will be
re-evaluated before further development.


## 2. Target Platform

MVP V0.1 targets:

- Linux
- Wayland
- Niri
- One output is sufficient for MVP validation

Hyprland and other compositors are explicitly deferred until the
core restoration model has been validated.


## 3. Core MVP Principle

The MVP follows the same safety rule as the full product:

> Live desktop state always wins over saved state.

And:

> Prefer incomplete restoration over incorrect restoration.

Continuum must never manipulate a window merely because it looks like
the most likely match.

When identity is ambiguous, the window must be left alone.


## 4. MVP Scope

Continuum-WM MVP V0.1 will support one designated favorite workspace.

The MVP must be able to:

1. Observe the favorite workspace.
2. Enumerate its windows.
3. Capture relevant window metadata.
4. Persist a normalized snapshot.
5. Load the snapshot after Continuum starts again.
6. Observe the current live desktop before restoration.
7. Detect applications or windows that appear to be missing.
8. Launch missing applications where sufficient launch information is
   available.
9. Observe newly created windows.
10. Match live windows to saved window entities conservatively.
11. Move confidently matched windows to the intended workspace.
12. Reconstruct basic Niri layout where reliable.
13. Leave ambiguous windows untouched.


## 5. Snapshot Data

The exact storage format is an implementation decision, but the MVP
snapshot should capture available evidence such as:

### Workspace

- compositor runtime workspace ID
- visible workspace position/index
- workspace name, if present
- output
- focused/active state

Runtime workspace IDs and visible workspace indices must be represented
as separate properties.

Neither should initially be assumed to be persistent across compositor
restarts.


### Window

Where available:

- compositor runtime window ID
- application ID
- PID
- title
- workspace association
- output association
- focused state
- floating state
- scrolling column position
- tile position within a column
- tile size
- window size
- floating position
- relevant process metadata
- launch information, when safely obtainable

Runtime window IDs are observation/control identifiers only and must
not be treated as persistent cross-restart identities.


## 6. Window Identity

The MVP must use multiple pieces of evidence when matching windows.

Potential signals include:

- application ID
- executable
- launch command
- title
- process metadata
- working directory
- workspace context
- layout context
- creation timing

No single signal is assumed to provide universal persistent identity.

PID is runtime-only.

Application ID alone is insufficient when multiple instances of the
same application exist.

Window title is useful evidence but may change dynamically.


## 7. Terminal Windows

Research has demonstrated that multiple Ghostty windows may share one
Ghostty process while maintaining distinct child shell processes and
PTYs.

This metadata may provide useful identity evidence.

However, MVP V0.1 does not require perfect reconstruction of multiple
otherwise-indistinguishable terminal windows.

If Continuum cannot confidently distinguish multiple terminal
instances, those instances must be classified as ambiguous rather than
guessed.


## 8. Restoration Flow

The intended MVP restoration sequence is:

    Load saved favorite workspace
            ↓
    Observe current live desktop
            ↓
    Compare saved and live windows
            ↓
    Identify already-existing windows
            ↓
    Determine confidently missing windows
            ↓
    Launch missing applications
            ↓
    Observe resulting window events
            ↓
    Match new windows conservatively
            ↓
    Move matched windows to favorite workspace
            ↓
    Reconstruct basic layout
            ↓
    Verify resulting live state


## 9. Niri Requirements

MVP V0.1 must use Niri's public IPC interfaces.

No Niri plugin or compositor modification is permitted.

The implementation should prefer the Niri event stream over continuous
polling.

Runtime Niri window IDs may be used for exact manipulation after a
window has been matched.

Focus-contextual Niri actions may be used only when Continuum controls
the complete operation closely enough to avoid intervening focus
changes.


## 10. Explicit Non-Goals

MVP V0.1 does not attempt to provide:

- Hyprland support
- Sway support
- River support
- GNOME support
- KDE support
- multi-monitor fidelity guarantees
- lazy restoration of non-favorite workspaces
- graphical configuration UI
- cloud synchronization
- application-internal session restoration
- browser tab restoration
- editor buffer restoration
- shell command-history restoration
- perfect same-application instance matching
- persistent compositor window IDs
- emerging Wayland session-management protocol support
- pixel-perfect restoration of every layout property


## 11. MVP Success Criteria

The MVP passes if, after a real logout/login or reboot:

1. Continuum starts successfully.
2. The saved favorite workspace is identified.
3. The expected applications can be restored.
4. Existing live windows are recognized rather than unnecessarily
   duplicated.
5. Confidently matched windows are placed on the intended workspace.
6. Basic Niri layout and ordering can be reconstructed sufficiently to
   resemble the saved workspace.
7. Ambiguous windows are safely skipped.
8. Unrelated live windows are not disturbed.
9. Continuum does not require elevated privileges.
10. Continuum does not require compositor plugins or private compositor
    interfaces.


## 12. Critical Safety Criterion

The following behavior constitutes an MVP failure:

> Continuum confidently selects the wrong window and moves or
> restructures it based on an incorrect identity guess.

Missing a window is acceptable during MVP evaluation.

Incorrectly manipulating an unrelated window is not.


## 13. Go / No-Go Gate

After the MVP implementation is complete, it will be tested against a
real desktop session using logout/login or reboot.

### GO

Proceed with Continuum-WM development if the MVP demonstrates that:

- restoration is useful,
- matching is sufficiently conservative,
- incorrect window manipulation can be avoided,
- public compositor interfaces provide adequate control, and
- the architecture can reasonably be extended.

Successful MVP validation unlocks investigation and development of:

- lazy workspace restoration
- stronger persistent identity
- multiple same-application instances
- terminal-aware identity providers
- multi-monitor restoration
- Hyprland adapter
- additional compositor adapters
- optional UI


### NO-GO

Pause or close the project if reliable restoration fundamentally
requires:

- guessing ambiguous window identity,
- elevated privileges,
- weakening normal Linux security controls,
- compositor plugins,
- private or unstable compositor internals, or
- application-specific hacks as a general requirement.

A failed MVP should be documented so the research remains useful.


## 14. MVP Development Stages

Development proceeds in the following order:

### MVP 0 — Snapshot

Capture the favorite workspace and its windows.

### MVP 1 — Persistence

Serialize and reload the normalized snapshot.

### MVP 2 — Live-State Reconciliation

Compare the saved snapshot with the current desktop.

### MVP 3 — Launch

Launch confidently missing applications.

### MVP 4 — Matching

Associate newly appearing windows with saved entities conservatively.

### MVP 5 — Placement

Move matched windows to the intended workspace.

### MVP 6 — Layout

Reconstruct the basic Niri workspace layout.

### MVP 7 — Real Session Test

Perform logout/login or reboot and evaluate the complete restoration
flow against the success and safety criteria in this document.


## 15. Decision Rule

Continuum-WM will not expand its scope until MVP V0.1 passes the real
session restoration test.

Research discovered during MVP development should be recorded, but new
features should not be added unless they are required to determine MVP
feasibility.
