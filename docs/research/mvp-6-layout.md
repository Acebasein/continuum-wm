# MVP 6 — Basic Layout Reconstruction

## Status

**PASS**

Continuum-WM successfully restored the basic Niri layout of a newly
launched and uniquely matched window using only public Niri IPC.

Validated capabilities:

- restore the matched window to the saved workspace
- restore its saved window width
- restore its saved window height
- restore its saved scrolling-layout column
- verify the resulting position
- restore the user's previously focused window
- avoid using the saved runtime window ID as a new-session identity

Multi-window columns and row reconstruction are intentionally outside the
scope of MVP 6.

---

## Objective

MVP 6 asks:

> After Continuum has safely matched a newly created window, can it
> reconstruct enough of the saved Niri layout to provide useful desktop
> continuity without incorrectly manipulating unrelated windows?

The MVP requires basic layout reconstruction rather than complete
pixel-perfect restoration of every compositor property.

---

## Starting Saved State

The saved Firefox record used throughout the test was:

```text
saved runtime ID: 52
app_id:           firefox
workspace index:  1
scrolling pos:    [2,1]
window size:      [1528,826]
floating:         false
