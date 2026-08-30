# MVP 2 — Live-State Reconciliation Validation

## Status

**PASS**

MVP 2 successfully demonstrated that Continuum-WM can compare a persisted
workspace snapshot against the current Niri window state and produce a
read-only restoration plan.

Continuum correctly distinguished saved windows that were still present from
saved windows that had disappeared, while ignoring unrelated live windows.

No desktop state was modified during reconciliation.

---

## 1. Objective

The objective of MVP 2 was to introduce the first decision-making stage in
Continuum-WM.

MVP 0 established observation:

    Niri
      |
      v
    Continuum Snapshot

MVP 1 established persistence:

    Continuum Snapshot
          |
          v
    Persistent State
          |
          v
    Continuum Snapshot

MVP 2 adds comparison:

    Saved Snapshot          Live Niri State
          |                       |
          +-----------+-----------+
                      |
                      v
                 Reconciler
                      |
                      v
              Restoration Plan

The central question was:

> Can Continuum determine which saved windows still exist and which are
> missing without modifying the live desktop?

---

## 2. Scope

MVP 2 intentionally implements only current-session reconciliation.

A saved window is classified as `PRESENT` only when its saved Niri runtime
window ID exists in the current live Niri state.

Otherwise it is classified as `MISSING`.

The MVP 2 identity rule is therefore:

    saved runtime ID exists live
        |
        +-- yes --> PRESENT
        |
        +-- no  --> MISSING

This is deliberately strict and conservative.

Niri runtime IDs are not assumed to survive compositor restart, logout, login,
or reboot.

MVP 2 therefore does not claim to solve persistent window identity.

Cross-session identity remains a later matching concern.

---

## 3. Reconciliation Model

MVP 2 introduced a reconciliation plan containing one entry for each window in
the saved workspace.

Each entry currently records:

- saved runtime window ID
- application ID
- title
- reconciliation status

The current statuses are:

    PRESENT
    MISSING

`PRESENT` means the exact saved runtime window ID still exists in the live
compositor state.

`MISSING` means that runtime ID is no longer present.

No action is associated with either state during MVP 2.

---

## 4. Read-Only Architecture

The MVP 2 reconciler is intentionally read-only.

It:

- loads the persisted Continuum snapshot
- queries Niri for current windows
- compares saved runtime IDs with live runtime IDs
- produces a restoration plan
- reports the result

It does not:

- launch applications
- close applications
- move windows
- resize windows
- focus windows
- switch workspaces
- modify Niri state

This establishes a safety boundary between deciding what may need restoration
and actually performing restoration.

---

## 5. Test Environment

The validation workspace contained two captured windows:

### Ghostty

    Runtime window ID: 27
    App ID:            com.mitchellh.ghostty

### Disposable Firefox Window

    Runtime window ID: 46
    App ID:            firefox

A separate Firefox window containing the active working session remained open
on another workspace.

This was intentional.

It allowed the test to verify that the reconciler did not treat an arbitrary
Firefox window as equivalent to the specific Firefox window stored in the
snapshot.

---

## 6. Test 1 — All Saved Windows Present

A fresh snapshot was captured while both test windows existed.

Running:

    cargo run -- reconcile

produced:

    [PRESENT] runtime=27 app=com.mitchellh.ghostty
    [PRESENT] runtime=46 app=firefox

    Plan: 2 present, 0 missing
    No desktop changes were made.

### Result

**PASS**

Both exact saved runtime IDs existed in Niri and were correctly classified as
present.

---

## 7. Test 2 — Saved Window Missing

The disposable Firefox window with runtime ID 46 was closed.

The snapshot was deliberately not recaptured.

Running:

    cargo run -- reconcile

then produced:

    [PRESENT] runtime=27 app=com.mitchellh.ghostty
    [MISSING] runtime=46 app=firefox

    Plan: 1 present, 1 missing
    No desktop changes were made.

### Result

**PASS**

Continuum correctly detected that the captured Firefox window no longer
existed.

The separate Firefox window on another workspace remained open.

Continuum did not classify runtime ID 46 as present merely because another
window with the same application ID existed.

---

## 8. Test 3 — Unrelated Live Window

An unrelated application window was opened after the snapshot had already been
captured.

The snapshot was again left unchanged.

Running reconciliation continued to report:

    [PRESENT] runtime=27 app=com.mitchellh.ghostty
    [MISSING] runtime=46 app=firefox

    Plan: 1 present, 1 missing
    No desktop changes were made.

The unrelated live window was not added to the restoration plan and was not
modified.

### Result

**PASS**

Live-only windows outside the saved snapshot do not alter the current
reconciliation result.

---

## 9. Important Findings

### 9.1 Exact runtime identity works within a compositor session

Niri runtime window IDs provide an exact and safe identity mechanism while the
same compositor session remains active.

This allows Continuum to distinguish between multiple windows belonging to the
same application without relying on application ID or title.

---

### 9.2 Application ID alone is insufficient

The missing Firefox test was performed while another Firefox window remained
alive.

Continuum correctly reported the captured Firefox runtime ID as missing.

This reinforces the earlier architectural requirement that multiple windows
from the same application must be treated as distinct session entities.

---

### 9.3 Unrelated live state must not become restoration work

A window that exists live but was not part of the saved snapshot is not
automatically something Continuum should manipulate.

The MVP 2 reconciler only evaluates saved entities.

This is consistent with the project principle:

> Live desktop state always wins over saved state.

---

### 9.4 Reconciliation and action should remain separate

MVP 2 produces a plan without executing it.

This separation provides an important safety boundary.

Future restoration stages can consume a reconciliation plan while retaining
the ability to refuse unsafe or ambiguous actions.

---

### 9.5 Current-session identity is not persistent identity

The successful MVP 2 tests must not be interpreted as evidence that runtime
window IDs can identify windows after logout, compositor restart, or reboot.

The current rule is intentionally limited to the active compositor session.

Persistent matching remains unresolved by MVP 2.

---

## 10. Automated Validation

The implementation passed:

    cargo fmt --check
    cargo clippy -- -D warnings
    cargo test

The test suite now includes reconciliation classification tests in addition to
the persistence/schema tests.

No compiler or Clippy warnings were reported.

---

## 11. MVP 2 Result

**MVP 2 — PASS**

Continuum-WM has demonstrated the ability to:

- load saved Continuum state
- query current Niri window state
- compare saved windows against live windows
- identify exact saved windows that remain present
- identify saved windows that have disappeared
- distinguish specific same-application windows using current-session runtime
  identity
- ignore unrelated live windows
- produce a restoration plan without modifying the desktop

The current-session reconciliation boundary required for MVP 2 is therefore
validated.

---

## 12. Known Limitation

MVP 2 reconciliation depends on Niri runtime window IDs.

These identifiers are suitable for exact targeting within the current
compositor session but are not considered persistent identifiers.

Therefore:

    Current-session reconciliation     VALIDATED
    Cross-session window matching      NOT VALIDATED

This limitation is intentional and must remain explicit as development
continues.

---

## 13. Next Stage

**MVP 3 — Launch**

MVP 3 introduces Continuum's first controlled restoration action.

The next question is:

> Given a saved window that reconciliation identifies as missing, can
> Continuum safely determine and execute enough launch information to recreate
> the application without disturbing unrelated live state?

The initial launch implementation must remain conservative.

If Continuum does not have sufficient information to launch a missing
application safely, it must not guess.
