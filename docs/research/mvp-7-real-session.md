# MVP 7 — Real Session Test

## Status

**PASS**

MVP 7 validates the Continuum-WM V0.1 restoration pipeline across a genuine compositor session boundary.

The purpose of this stage was not to add new restoration capabilities. It was to determine whether the mechanisms validated during MVP 0–6 continue to behave safely after a real reboot and fresh Niri session.

The primary safety requirement remained:

> Prefer incomplete restoration over incorrect restoration. Do not guess.

---

## Objective

Validate that Continuum-WM can:

1. persist a saved workspace snapshot across reboot;
2. recognize that runtime compositor identifiers from the previous session are stale;
3. reconcile saved state against a fresh live desktop;
4. relaunch a missing application using captured launch information;
5. identify a newly created window conservatively;
6. restore workspace placement and basic layout when identity is sufficiently known; and
7. leave unrelated or ambiguously identified windows untouched.

The test used the existing MVP 0–6 implementation without introducing special reboot handling or application-specific restoration logic.

---

## Pre-Reboot Snapshot

The controlled saved workspace contained two windows on workspace 1.

### Ghostty

* saved runtime ID: `27`
* app ID: `com.mitchellh.ghostty`
* launch command: `ghostty`
* saved scrolling position: `[1, 1]`
* saved window size: `1527 × 825`

### Firefox

* saved runtime ID: `52`
* app ID: `firefox`
* saved title: `Project Foundry | Notion — Mozilla Firefox`
* launch command:

```text
/run/current-system/sw/bin/firefox --name firefox
```

* saved scrolling position: `[2, 1]`
* saved window size: `1528 × 826`

The snapshot was intentionally left unchanged immediately before reboot so that the same controlled two-window specimen used during earlier MVP testing would cross the real session boundary.

The repository and MVP 6 state were committed and pushed before reboot.

---

## Test Procedure

The system was rebooted normally into a new Niri session.

No saved Continuum application was manually opened immediately after login.

Kitty was opened as an independent test console so that neither saved Ghostty nor saved Firefox state would be contaminated before reconciliation.

---

## Test 1 — Post-Reboot Reconciliation

The following command was executed from Kitty:

```bash
cargo run -- reconcile
```

Continuum reported:

```text
Continuum-WM MVP 2 — Reconciliation

Saved workspace 1 contains 2 window(s):

[MISSING] runtime=27 app=com.mitchellh.ghostty title=cargo run -- capture
[MISSING] runtime=52 app=firefox title=Project Foundry | Notion — Mozilla Firefox

Plan: 0 present, 2 missing
No desktop changes were made.
```

### Result

**PASS**

The persisted snapshot survived reboot.

Both historical window runtime IDs were treated as missing in the new Niri session.

The newly opened Kitty test console was not confused with either saved entity.

No desktop state was modified during reconciliation.

---

## Test 2 — Ghostty Restoration

The saved Ghostty entity was restored using:

```bash
cargo run -- launch-match 27
```

Continuum relaunched Ghostty and successfully identified the resulting window.

The new Ghostty window was restored to workspace 1 at:

```text
[1, 1]
```

A later live-state query showed:

```text
runtime ID: 3
app ID: com.mitchellh.ghostty
PID: 3680
workspace ID: 1
scrolling position: [1, 1]
window size: 1363 × 733
```

The runtime ID and PID were new-session values and were not the historical saved values.

### Result

**PASS**

This validates the core V0.1 restoration path across a real reboot:

```text
persisted saved entity
        ↓
fresh compositor session
        ↓
saved runtime identity missing
        ↓
application launch
        ↓
new live window observation
        ↓
conservative unique match
        ↓
exact new runtime identity
        ↓
workspace/layout restoration
```

Continuum did not attempt to manipulate the historical runtime ID.

---

## Test 3 — Firefox Restoration

The saved Firefox entity was then tested using:

```bash
cargo run -- launch-match 52
```

Continuum captured the saved launch command and established a baseline containing two existing live windows.

After confirmation, Firefox was launched:

```text
Process started with PID 4045.
```

Continuum observed for the configured five-second matching window but reported:

```text
NO MATCH
No new window with app_id "firefox" appeared during the observation window.
LAYOUT RESTORATION BLOCKED.
No desktop manipulation was attempted.
```

Because no unique saved-window match was established, Continuum did not issue the second layout-restoration confirmation.

### Result

**SAFE INCOMPLETE RESTORATION**

The application launch succeeded, but saved-window identity was not established.

Continuum correctly refused to manipulate any Firefox window.

This behavior satisfies the MVP safety requirement.

---

## Firefox Application-Owned Restore Behavior

The Firefox cold-start sequence exposed an important distinction between application launch and individual window restoration.

Firefox initially created a transient window:

```text
runtime ID: 4
PID: 4045
app ID: firefox
title: Firefox - Choose a profile
workspace ID: 1
scrolling position: [3, 1]
window size: 679 × 734
```

The user selected the Firefox profile.

Firefox then opened its normal browser state and requested whether the previous Firefox session should be restored.

After the user accepted Firefox's own session restoration, the transient profile-selection window was no longer present and Firefox produced three browser windows:

```text
ID 5
PID 4045
title: Continuum-WM for Wayland — Mozilla Firefox
workspace: 1
position: [3, 1]

ID 6
PID 4045
title: New Tab — Mozilla Firefox
workspace: 1
position: [4, 1]

ID 7
PID 4045
title: New Tab — Mozilla Firefox
workspace: 1
position: [5, 1]
```

All three windows shared:

```text
app_id = firefox
PID = 4045
```

One Firefox window was manually resized during investigation, so its final size is not used as restoration evidence.

---

## Finding — Application Launch Is Not Window Identity

The Firefox test demonstrates that these are separate questions:

### Application launch

Did Continuum successfully start or activate the application?

In this test:

```text
Firefox launch: YES
PID 4045 created
```

### Window identity

Can Continuum determine which particular live Firefox window corresponds to saved entity `52`?

In this test:

```text
Saved-window identity: NO
```

A successful application launch therefore does not imply successful individual-window restoration.

The architecture must preserve this distinction.

---

## Finding — One Application Launch May Produce Multiple Windows

The V0.1 prototype uses a simplified restoration transaction:

```text
saved window
    ↓
launch captured command
    ↓
observe new windows
    ↓
classify candidates
```

Firefox demonstrated that a real application may instead behave as:

```text
launch application
    ↓
transient startup window
    ↓
user interaction
    ↓
application-owned session restoration
    ↓
multiple persistent windows
```

Multiple resulting windows may share the same:

* application ID;
* process ID; and
* launch origin.

Therefore neither application ID nor PID alone is sufficient to associate a particular restored browser window with a particular saved Continuum window.

Continuum must not resolve this situation by selecting the first matching application window.

---

## Finding — Interactive Startup Must Not Become a Global Restore Barrier

Firefox also demonstrated that application-owned restoration may require user interaction.

Examples include:

* selecting an application profile;
* accepting an application's own session restoration;
* responding to startup dialogs.

The user may respond immediately, respond much later, or ignore the application entirely.

Therefore Continuum must not require:

```text
launch application A
        ↓
wait until application A stabilizes
        ↓
launch application B
```

as a global restoration strategy.

An unresolved or interactive application must not prevent unrelated applications from progressing through their own restoration transactions.

This finding supports an event-driven, independently progressing restoration model.

---

## Finding — Application Restoration and Window Restoration Are Separate Layers

MVP 7 establishes two distinct restoration responsibilities.

### Application restoration

Ensure that an application required by the saved workspace is running or has been activated.

### Window restoration

Associate individual live compositor windows with saved Continuum entities and restore compositor-owned state only when identity is sufficiently known.

Possible outcomes therefore include:

```text
application launched
window matched
layout restored
```

but also:

```text
application launched
window identity unresolved
layout untouched
```

The second outcome is not necessarily an application-launch failure.

It is a safe incomplete restoration.

---

## Safety Validation

The most important Firefox result is what Continuum did **not** do.

It did not:

* treat the transient Firefox profile chooser as saved window `52`;
* select one of the later Firefox windows arbitrarily;
* use the historical runtime ID `52` as an action target;
* resize an uncertain Firefox window;
* move an uncertain Firefox window;
* reorder an uncertain Firefox window; or
* manipulate the unrelated Kitty test console.

This directly validates the core safety principle:

> Prefer incomplete restoration over incorrect restoration. Do not guess.

Had Continuum arbitrarily selected one of the Firefox windows and restored saved layout onto it, MVP 7 would have failed.

It did not.

---

## Final Live Desktop State

The final observed workspace 1 state was:

```text
[1,1] Ghostty   runtime ID 3
[2,1] Kitty     runtime ID 2
[3,1] Firefox   runtime ID 5
[4,1] Firefox   runtime ID 6
[5,1] Firefox   runtime ID 7
```

Kitty remained focused and untouched.

Ghostty represented the successful end-to-end restoration case.

Firefox represented the deliberately incomplete case where application launch succeeded but individual-window identity could not be established safely.

---

## MVP 7 Conclusion

**PASS**

The real-session test demonstrates that the Continuum-WM V0.1 architecture can survive a genuine reboot/session boundary and perform useful restoration without relying on historical compositor runtime identifiers.

The test validates:

* persistent saved state;
* fresh-session reconciliation;
* missing-window detection;
* controlled application relaunch;
* observation of newly created compositor windows;
* conservative matching;
* exact new-session runtime targeting;
* workspace restoration;
* basic layout restoration; and
* safe refusal when identity is insufficient.

The Firefox test exposed limitations in the simplified V0.1 restoration model, but it did not invalidate the feasibility result.

Instead, it established architectural requirements for later design work:

1. application identity, application launch/activation, and individual window identity must remain separate concepts;
2. one application launch may create multiple compositor windows;
3. application-owned session restoration may require user interaction;
4. interactive applications must not globally block unrelated restoration work;
5. ambiguous individual-window identity must remain unresolved rather than guessed.

These findings belong to the post-MVP architecture and V1 scope-definition phase.

They should not be addressed by weakening the conservative matching requirement.

---

## V0.1 MVP Scorecard

| Stage | Description               | Result   |
| ----- | ------------------------- | -------- |
| MVP 0 | Snapshot                  | PASS     |
| MVP 1 | Persistence               | PASS     |
| MVP 2 | Live-State Reconciliation | PASS     |
| MVP 3 | Launch                    | PASS     |
| MVP 4 | Matching                  | PASS     |
| MVP 5 | Placement                 | PASS     |
| MVP 6 | Layout                    | PASS     |
| MVP 7 | Real Session Test         | **PASS** |

All V0.1 feasibility stages have now passed.

The project can proceed to the formal **GO / NO-GO review** against the original V0.1 feasibility question.

