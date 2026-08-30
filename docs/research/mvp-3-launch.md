# MVP 3 — Controlled Application Launch

**Status:** PASS

## Objective

Determine whether Continuum-WM can safely launch an application associated
with a saved window that live-state reconciliation identifies as missing.

MVP 3 intentionally tests launch capability only.

It does not attempt to identify, match, move, focus, or otherwise manipulate
the window created as a result of the launch.

## Safety Boundary

Continuum-WM may launch a missing saved window only when launch information
was captured from the live process associated with that window at snapshot
time.

For MVP 3:

- launch information is captured from `/proc/<pid>/cmdline`
- the captured command is stored as an argv array
- no command is inferred from `app_id` or window title
- commands are executed directly with `std::process::Command`
- no shell is involved
- only one explicitly selected saved window may be launched
- a saved window already classified as present must not be launched
- user confirmation is required before process creation
- launching does not imply that a resulting window has been matched

The command interface used for the test is:

```text
continuum-wm launch <saved-runtime-id>
