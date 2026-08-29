Continuum-WM — Requirements V0.1
1. Purpose

Continuum-WM is a Linux-native, distribution-agnostic session continuity service for Wayland environments.

Its purpose is to preserve the user's working desktop across logout, reboot, or session restart and progressively reconstruct that environment when it is needed.

The core experience is:

Favorite first. Everything else on demand.

Continuum-WM shall avoid restoring the entire previous desktop immediately at login. Instead, it restores one user-selected favorite workspace and keeps the remaining saved workspaces dormant until the user visits them.

2. Product Principles
P1 — Live desktop is the source of truth

Continuum-WM observes and assists the running desktop.

It shall not maintain a competing representation of what the desktop should contain.

If saved state conflicts with the current live session:

Live desktop state wins.

P2 — Prefer incomplete restoration over incorrect restoration

Continuum-WM shall not guess when it cannot confidently identify a saved application/window instance.

A partially restored workspace is preferable to incorrectly assigning applications or windows.

P3 — Restore progressively

Restoration should happen according to user intent.

Entering a dormant workspace expresses the intent to resume that workspace.

P4 — Compositor independence

The core session model shall not depend upon Hyprland, Niri, or another specific compositor.

Compositor-specific capabilities shall be isolated behind defined integration boundaries.

P5 — No compositor plugins

Continuum-WM shall operate using public compositor interfaces and standard Linux/Wayland facilities.

A compiled Hyprland, Niri, or other compositor plugin shall not be required.

P6 — Graceful degradation

Not every compositor or application will expose identical restoration capabilities.

Continuum-WM shall use available capabilities without requiring every environment to support the complete feature set.

P7 — Maintainability

The project shall prefer documented, stable public interfaces over internal compositor APIs, implementation-specific hooks, or fragile workarounds.

3. Terminology

Session
The user's running graphical working environment.

Snapshot
Persisted information describing restorable parts of a session.

Workspace
A compositor-managed logical area containing windows.

Favorite Workspace
The single workspace selected by the user for automatic restoration at login.

Dormant Workspace
A workspace for which saved session state exists but whose applications/windows have not yet been restored.

Live Workspace
A workspace whose current running state supersedes its saved dormant state.

Restoring Workspace
A workspace currently undergoing restoration.

Window Instance
A specific application window. Multiple windows belonging to the same application are distinct instances.

Application State
Application-owned information such as browser tabs, editor buffers, open documents, terminal history, etc.

Compositor State
Window/workspace information such as workspace membership, monitor placement, layout position, size, floating state and fullscreen state.

4. Functional Requirements
4.1 Session Observation

Continuum-WM shall observe the current compositor session sufficiently to maintain an up-to-date representation of:

workspaces
windows
application identity where available
workspace membership
monitor/output membership
window lifecycle
relevant layout/window state exposed by the compositor

Event-driven observation should be preferred where supported.

The system shall avoid unnecessary continuous polling.

4.2 Session Persistence

Continuum-WM shall maintain sufficient persistent state to reconstruct supported portions of the previous desktop session.

The persisted snapshot shall be updated as the live desktop changes.

Closing, opening, moving, or otherwise modifying windows during normal use shall eventually be reflected in the persisted state without requiring the user to manually save the session.

Normal usage therefore becomes the mechanism by which Continuum-WM learns the desired desktop state.

4.3 Favorite Workspace

The user shall be able to designate exactly one workspace as the favorite workspace.

At graphical login/session startup:

Favorite Workspace
        ↓
restore automatically

Other saved workspaces shall not automatically restore by default.

The favorite workspace selection shall be simple and shall not require creation of a project/session profile.

4.4 Lazy Workspace Restoration

Saved non-favorite workspaces shall initially remain dormant.

Example:

LOGIN

WS1   ● Live / Favorite
WS2   ○ Dormant
WS3   ○ Dormant
WS4   ○ Dormant
WS5   ○ Dormant

When the user first activates a dormant workspace:

WS3 selected
     ↓
detect saved state
     ↓
RESTORING
     ↓
launch required applications
     ↓
identify resulting windows
     ↓
restore supported placement/state
     ↓
LIVE

Restoration shall occur independently per workspace whenever practical.

Visiting one workspace shall not require restoring unrelated dormant workspaces.

4.5 Application and Window Restoration

For each restorable window instance, Continuum-WM should attempt to recover supported information including:

application
workspace
monitor/output
tiled or floating state
window size
floating geometry
fullscreen/maximized state
compositor-specific layout position where reliably supported

Exact capabilities may vary between compositors.

Continuum-WM shall expose this limitation rather than pretending unsupported state has been restored.

4.6 Multiple Instances of the Same Application

Continuum-WM shall not assume that application identity uniquely identifies a window.

For example:

Ghostty A → Project A
Ghostty B → Project B
Ghostty C → General

Firefox A → Development
Firefox B → Research

VS Code A → Repository A
VS Code B → Repository B

These shall be treated as distinct session entities.

Where standardized persistent session identity is available, Continuum-WM should prefer it.

Where it is unavailable, multiple available signals may be used to distinguish instances.

Potential signals include:

application ID/class
executable
process metadata
command line
working directory
window title
process ancestry
workspace context
compositor metadata

No single heuristic signal shall automatically be assumed globally unique.

4.7 Restoration Confidence

When restoration requires heuristic matching, Continuum-WM shall distinguish between:

Confident match
Ambiguous match
No match

A confident match may be restored automatically.

An ambiguous match shall not be placed based purely on a guess.

A failed or ambiguous match shall not prevent other confidently identified windows from being restored.

This gives us the principle:

Do not guess.

4.8 Application-Owned State

Continuum-WM shall not attempt to reproduce application-specific session-management functionality.

Examples include:

Firefox / Chromium
→ tabs, browsing session

Neovim
→ buffers, editor session

VS Code
→ project/editor state

Office applications
→ document state

Applications remain responsible for their internal state.

Continuum-WM coordinates desktop/session placement and lifecycle around those applications.

Where standardized Wayland session-management facilities allow applications and compositors to cooperate on restoration, Continuum-WM should be designed to take advantage of them.

4.9 User Intervention During Restoration

The user shall retain control of the desktop during restoration whenever technically safe.

If the user changes the live desktop while Continuum-WM is restoring or preparing to restore saved state, Continuum-WM shall not continually fight those changes in an attempt to recreate an obsolete snapshot.

For example:

Saved state:

WS4
└── Ghostty


Before Continuum restores WS4:

User opens Firefox on WS4
        ↓
Live state has changed
        ↓
Continuum adapts

Again:

Live state wins.

4.10 Partial Restoration

A workspace may enter a partially restored state.

Example:

WS3 saved
├── Ghostty       ✓
├── Firefox       ✓
├── VS Code       ?
└── application X ✗

Successfully restored windows shall remain usable.

One failed application shall not cause the entire workspace restoration to roll back or repeatedly restart.

The system should retain sufficient diagnostic information to explain what could not be restored.

5. Workspace State Model

At minimum, Continuum-WM shall conceptually support:

DORMANT
    saved state exists but is not running

RESTORING
    restoration is underway

LIVE
    live desktop state is authoritative

PARTIAL
    restoration completed with unresolved items

FAILED
    restoration could not meaningfully proceed

The precise implementation of these states is a design decision rather than a requirement.

6. Platform Requirements
6.1 Linux

Continuum-WM shall target Linux.

Linux facilities such as process metadata may be used to augment compositor information.

6.2 Wayland

Wayland compositors are the primary target.

The architecture should allow adoption of standardized Wayland session-management protocols as they mature.

6.3 Distribution independence

Continuum-WM shall not require a particular Linux distribution.

Initial development environments may include:

Arch / Omarchy
NixOS

but distribution-specific package management shall not form part of the core session model.

6.4 Compositor support

The initial compositor targets shall be:

Niri
Hyprland

Their differing workspace models shall be treated as an architectural test of compositor independence rather than forcing one compositor's semantics onto the other.

Additional compositors may be supported later through suitable integration mechanisms.

7. Reliability and Safety

Continuum-WM shall:

avoid incorrectly moving unrelated user windows
avoid repeatedly relaunching applications after restoration failure
tolerate applications that start slowly
tolerate applications appearing in unpredictable order
tolerate multiple instances of the same application
tolerate applications that do not support session restoration
preserve useful successfully restored state when another restoration fails
avoid requiring compositor restart/reload for normal operation where practical
recover gracefully from malformed or incomplete persisted session state

A failure in Continuum-WM should not make the compositor itself unusable.

8. Maintainability

Continuum-WM shall prefer:

Standard Wayland protocols
        ↓
Documented compositor IPC
        ↓
Stable Linux interfaces
        ↓
Carefully isolated heuristics

over:

Private compositor APIs
Internal implementation hooks
Compiled compositor plugins
Version-specific binary interfaces

Compositor-specific behavior shall be isolated sufficiently that a change in Hyprland should not require rewriting Niri integration or the Continuum-WM core, and vice versa.

9. Privacy and Local State

Session snapshots may contain potentially sensitive metadata such as:

application names
window titles
working directories
executable paths
project names
command-line information

Continuum-WM shall treat session state as local user data.

V1 shall not require a cloud service or external account.

Persisted state should contain only information reasonably necessary for session continuity.

10. User Control

V1 should require minimal configuration.

At minimum, the user shall be able to:

Select favorite workspace

Enable/disable automatic favorite restoration

Enable/disable lazy restoration

Inspect restoration status

Trigger restoration manually when necessary

Prevent/stop an unwanted restoration

Normal session changes shall not require manually editing a project/session definition.

11. V1 Scope

The initial useful release should focus on:

Session capture

Observe workspaces/windows and maintain restorable state.

Favorite workspace

Automatically resume one selected workspace after login.

Lazy restoration

Resume other saved workspaces when first visited.

Multiple-instance handling

Avoid treating every window from the same application as interchangeable.

Partial restoration

Recover what can safely be recovered.

Niri + Hyprland

Demonstrate compositor-independent architecture using two substantially different compositor models.

Local operation

No cloud dependency.

12. Explicit V1 Non-Goals

V1 shall not require:

macOS/GNOME-style desktop overview
gesture support
project/workspace profile management
cloud synchronization
cross-machine session migration
restoration of application-internal documents/tabs/buffers
compositor plugins
identical restoration fidelity across all compositors
graphical configuration UI
exact restoration for every application
support for every Wayland compositor

This is important because otherwise Continuum-WM could very quickly turn into a desktop environment. 😄

13. Future Considerations

The architecture should leave room for, without committing V1 to:

Standard Wayland session-management integration
                │
Named snapshots │
                │
Project/activity grouping
                │
Session history / rollback
                │
Additional compositor adapters
                │
Optional graphical frontend
                │
Cross-device continuity

Project/activity grouping in particular should be capable of becoming a view over Continuum's existing workspace/session state, rather than requiring users to maintain a second representation of their desktop.

14. V0.1 Success Definition

Before calling Continuum-WM's first implementation successful, we should be able to demonstrate this scenario:

BEFORE REBOOT

Favorite WS
├── App A
└── App B

WS2
├── App C
└── App D

WS3
├── App A instance #2
└── App E


             ↓ REBOOT ↓


LOGIN

Favorite WS
├── App A
└── App B

WS2     ○ Dormant
WS3     ○ Dormant


             ↓ user visits WS3 ↓


WS3
├── App A instance #2
└── App E

WS2 remains dormant

And most importantly:

Continuum-WM must correctly understand that App A on the favorite workspace and App A instance #2 on WS3 represent different session entities.
