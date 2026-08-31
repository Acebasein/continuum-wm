# MVP 5 — Exact Window Placement

## Status

**PASS**

## Objective

Determine whether Continuum-WM can safely place a uniquely matched window onto the saved Niri workspace using only public compositor interfaces.

The placement stage must preserve Continuum-WM's primary safety rule:

> Prefer incomplete restoration over incorrect restoration. Do not guess.

## Safety Boundary

Placement is permitted only after matching has produced exactly one candidate.

The restoration chain is:

```text
Saved missing window
        ↓
Controlled launch
        ↓
Observe new Niri windows
        ↓
MATCHED
        ↓
Use newly matched runtime window ID
        ↓
Move exact window
        ↓
Saved workspace index
