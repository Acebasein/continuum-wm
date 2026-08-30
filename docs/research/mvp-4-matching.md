# MVP 4 — Window Matching

**Status:** PASS

## Objective

Determine whether Continuum-WM can safely associate a newly created Niri
window with one explicitly launched saved window without relying on unstable
runtime IDs, process IDs, or window titles as persistent identity.

MVP 4 does not manipulate the desktop. It only observes and classifies
candidate windows.

## Safety Rule

Continuum-WM must prefer an incomplete restoration over an incorrect one.

For a controlled launch transaction:

- zero eligible candidates → `NO MATCH`
- exactly one eligible candidate → `MATCHED`
- more than one eligible candidate → `AMBIGUOUS`

When ambiguous, Continuum-WM must not guess.

## Matching Strategy

MVP 4 uses transaction-scoped matching.

Before launching a saved missing window:

1. Open the public Niri event stream.
2. Receive the initial live window state.
3. Record all existing Niri runtime window IDs as the baseline.
4. Launch exactly one saved missing window.
5. Observe new `WindowOpenedOrChanged` events.
6. Ignore windows already present in the baseline.
7. Keep newly appearing windows whose `app_id` matches the saved window.
8. Classify the resulting candidate set.

This avoids treating unrelated existing windows from the same application as
candidates for the current launch transaction.

## Why PID Is Not Sufficient

MVP 3 already demonstrated that the PID returned by process launch cannot be
assumed to own the resulting Wayland window.

A Firefox launch returned a newly spawned process PID, while the resulting
window belonged to the already-running Firefox process.

During MVP 4, multiple Firefox windows also shared:

- the same `app_id`
- the same PID

Therefore `app_id + PID` is not sufficient to distinguish Firefox windows.

PID remains useful diagnostic evidence, but is not used as the decisive
matching identity in MVP 4.

## Why Title Is Not Sufficient

The saved Firefox window title was:

`Project Foundry | Notion — Mozilla Firefox`

A newly created Firefox window initially appeared as:

`Mozilla Firefox`

Window titles are application-controlled and may change before, during, or
after restoration.

Title is therefore diagnostic/supporting metadata only for MVP 4.

## Event-Driven Observation

Niri's public JSON event stream provides:

- an initial `WindowsChanged` state
- incremental `WindowOpenedOrChanged` events

This allows Continuum-WM to establish a live baseline and then observe newly
created windows without polling the complete compositor state repeatedly.

## Observation Timeout

The first MVP 4 implementation used blocking `read_line()` calls.

That meant the intended five-second observation deadline could not be
guaranteed when no new event arrived.

The event reader was changed to use Linux `poll(2)` on the Niri event stream
file descriptor before attempting a blocking read.

The `poll(2)` call is isolated behind a small Rust wrapper. The rest of the
matching implementation remains safe Rust.

A five-second no-match diagnostic completed successfully instead of hanging.

Example:

```text
Expected app_id: "continuum-mvp4-does-not-exist"

Baseline established with 12 live window(s).
Observing new Niri windows for 5 seconds...

NO MATCH
No new window with app_id "continuum-mvp4-does-not-exist" appeared during the observation window.
No placement or desktop manipulation was attempted.
