# Continuum-WM: Session Continuity for Niri — Architecture & Design

*A design document written as a professor/architect walkthrough for a systems-programming beginner.*

---

## Part 1 — Restate the Problem

Strip away the jargon and Continuum-WM is trying to answer one question, over and over, safely:

> **"The windows I had before are gone. Which of the windows I have now correspond to them, and where should each one go?"**

That's it. Everything else — layout, sizing, working directories, lazy loading — is detail hanging off that one question.

The hardest engineering problems, in order of difficulty:

1. **Identity without persistence.** Nothing the compositor gives you (PID, window ID) survives a reboot. You have to invent identity that *you* own and that survives compositor/OS churn.
2. **Matching under ambiguity.** When you ask an app to relaunch and two windows of the same app appear, which one is "the new one"? This is fundamentally a disambiguation problem, not a lookup problem.
3. **Safety under partial information.** You will often not have enough evidence to be sure. The system has to have a well-defined "I don't know" state that does nothing destructive, rather than a forced binary decision.
4. **Concurrent reality.** The live desktop keeps changing while you're trying to reconcile it against saved state (user opens something mid-restore). Your model can't assume the world holds still.
5. **Compositor capability limits.** Niri does not expose (and may never expose) full Cartesian placement or exact pixel geometry in its scrolling model. You must design your data model around what's *actually restorable*, not around what would be convenient.

None of these are Niri bugs — they're inherent to "restore session on any tiling/scrolling Wayland compositor." Good news: this means the hard parts of your design are compositor-independent, which validates your instinct to build a portable core.

---

## Part 2 — Feasibility

| Requirement | Feasibility on Niri |
|---|---|
| Enumerate workspaces/windows | **Straightforward.** `niri msg -j workspaces` / `windows` give full structured JSON. |
| Event stream (window opened/closed, workspace activated) | **Straightforward.** `niri msg -j event-stream` is a supported, documented, stable JSON-lines stream. |
| Detect workspace activation for lazy restore | **Straightforward.** `WorkspaceActivated` events exist. |
| Move window to workspace | **Straightforward.** `niri msg action move-window-to-workspace`. |
| Determine app ID / title | **Straightforward.** Present in window JSON. |
| Distinguish multiple instances of same app | **Difficult but achievable.** Requires your own matching layer — Niri gives you no help here, nor should it; this is fundamentally your problem. |
| Restore working directory generically | **Difficult but achievable for most terminals** via `/proc/<pid>/cwd` of the foreground shell. **Compositor-dependent/unsafe for Ghostty-like multi-PTY-single-process terminals** — requires an app-specific provider, or must be explicitly skipped rather than guessed. |
| Exact pixel size restoration | **Potentially impossible to guarantee.** Niri's scrolling-column model doesn't have a persistent absolute pixel size concept the way a floating WM does — column width is often a *proportion* or a size Niri itself renegotiates on layout changes. Treat "exact size" as best-effort, not guaranteed. |
| Column/order/layout reconstruction | **Difficult but achievable, approximation only.** You can reconstruct *membership and relative order*; exact interleaving reconstruction with concurrent live windows is not fully deterministic. |
| Floating/tiled/fullscreen state | **Straightforward** — exposed and settable via actions. |
| Output/monitor placement | **Straightforward when the same outputs exist. Compositor/hardware-dependent** when monitor configuration has changed (must degrade gracefully). |
| Zero-guess safety guarantee | **Achievable by design**, not by compositor feature — this is entirely a property of *your* matching algorithm, discussed in Part 7. |

The one-sentence summary: **Niri gives you excellent read/observe primitives and adequate control primitives; it gives you no identity or matching primitives at all — that's entirely your layer to build**, which is exactly why your architecture needs to treat matching as a first-class subsystem, not a helper function.

---

## Part 3 — Recommended Technology

You asked me not to default to "easy because beginner" or "hard because systems project." Let's actually score it.

### Candidates against your real constraints

| Concern | Rust | Go | Python |
|---|---|---|---|
| Niri IPC (JSON over stdout/socket) | Excellent (serde) | Excellent (encoding/json) | Excellent (json stdlib) |
| Event-driven / async long-running daemon | Excellent (tokio), but async Rust has real learning curve | Excellent — goroutines + channels are *built for exactly this pattern* | Workable (asyncio) but weaker for a long-lived, multi-stream daemon |
| `/proc` inspection | Fine, some boilerplate | Fine, very ergonomic (`os`, `procfs` pkgs) | Easiest, but weakly typed data extraction |
| Process spawning + supervision | Good | Excellent, this is Go's home turf | Good |
| Concurrency model matching your sequential-transaction + future limited-parallel-restore design | Good, more ceremony | **Very good fit** — one goroutine per in-flight restore transaction, channels for event routing, is almost exactly your Part 13 diagram | Workable, GIL limits true parallelism (not fatal here, restore is I/O bound) |
| Memory safety / correctness guarantees | **Best** | Good (GC, no manual memory mgmt, but less strict typing than Rust) | Weakest (runtime type errors are a real risk in a system meant to be *conservative and correct*) |
| Single static binary, easy packaging on NixOS/Arch | **Best** (musl static binaries, trivial Nix derivation) | Excellent (native static binaries) | Weakest — Python packaging on Nix is real friction, plus a running interpreter as a systemd service is heavier |
| Daemon/service ergonomics (systemd, signals, socket activation) | Good | **Excellent**, huge ecosystem precedent (many Linux daemons are Go) | Workable, less idiomatic |
| Debugging/testing story | Excellent, strict compiler catches classes of bugs before runtime | Excellent, simple mental model, good tooling | Excellent tooling but weaker safety net |
| Future Hyprland backend (trait/interface abstraction) | Excellent — traits are made for this | Excellent — interfaces are made for this | Fine — ABCs work but less enforced |
| Learning curve for a systems-programming beginner | Steep (ownership/borrowing + async both at once) | **Gentle** — no ownership model to learn, no async ceremony, reads almost like structured pseudocode | Gentle but you already said you don't want "easiest by default" |
| Long-term OSS maintainability / contributor onboarding | Good, but Rust async has a real barrier to casual contributors | **Best** — Go is famous for being readable by contributors who've never touched the codebase before | Good |

### My one recommendation: **Go**

Not the "beginner-easy" pick and not the "systems-purist" pick — it's the pick that best matches *this specific system's shape*:

- Continuum-WM is fundamentally an **event-driven daemon coordinating sequential state machines with I/O-bound waits** (spawn process → wait for event → validate → act). That is precisely the workload goroutines + channels were designed for, without the conceptual overhead async/await + ownership brings in Rust.
- You need a **static, trivially-packaged binary** for NixOS/Arch — Go delivers this with none of Rust's build-time friction and none of Python's interpreter/venv problem.
- Memory-safety is *nice to have* here but not the dominant risk. Your dominant risk (per your own prioritization: correctness → non-destructive behavior → reliable matching) is **logical/state-machine correctness**, not memory corruption. Go's simplicity reduces incidental complexity so you can spend your attention on the matching/state-machine logic where the real risk lives.
- Interfaces give you the exact compositor-abstraction (`CompositorBackend`) and provider-abstraction (`ApplicationProvider`) seams you want, with far less ceremony than Rust traits + generics + async trait workarounds (async traits in Rust are still genuinely awkward as of today).
- Onboarding contributors to an OSS project: Go's low-ceremony style materially lowers the bar for someone to read `restore.go` and understand what's happening, which matters for a project you want the community to help maintain.

**Why not Rust**, despite it being the "obvious" systems-Linux choice: the marginal safety benefit is real but not decisive for a userspace daemon that isn't touching unsafe memory, parsing untrusted binary formats, or needing max performance — while the async ergonomics cost (tokio + trait objects + Send/Sync bounds on a project with a plugin/provider architecture) is a genuine tax on both your learning curve and contributor onboarding.

**Why not Python**: your project explicitly prioritizes correctness and non-destructive behavior above almost everything else, in a domain (matching windows, deciding whether to move/kill/launch things) where a `None` vs missing-key runtime error is exactly the kind of bug that could cause the "incorrect restoration" you most want to avoid. A statically typed language earns its keep here.

### Learning path if Go is new to you
You don't need to learn all of Go before starting:
1. Week 1: variables, structs, slices/maps, functions, error handling (`if err != nil`) — this is 80% of the language.
2. Week 2: goroutines + channels, using **only** the small pattern you already prototyped (spawn → wait for one event → done) before touching concurrent restores.
3. Introduce `context.Context` for timeouts/cancellation once Phase 5 (state machine) needs it.
4. Interfaces come naturally once you write your second backend stub (even a fake "mock compositor" for tests counts).

You will be productive within days, not weeks — that's part of the point.

---

## Part 4 — Architecture

Let's critique your proposed diagram before adopting it.

**What's right:** Capture / Reconcile / Restore as separate concerns, and Compositor API as a swappable boundary — keep both.

**What's missing or under-specified:**
- There's no explicit **Matching Engine** as its own component — you've buried it inside "Reconcile," but per your Part 7 emphasis, matching is complex and important enough to be a first-class component with its own tests, independent of reconciliation policy.
- There's no explicit **State Store** — restore state (Part 12) and session data (Part 16) are different lifetimes and need to be modeled as a component, not implied.
- There's no **Event Bus/Dispatcher** — with an event-driven daemon watching Niri's event-stream while also running restore transactions and serving CLI commands, you need one place that fans events out to interested subscribers (lazy-restore trigger, live-desktop tracker, an in-progress restore transaction waiting for "did my window appear").
- "Restore" as one box hides the sequential state machine (Part 8) — that deserves to be visible at the architecture level since it's central to your whole safety model.

### Revised component diagram

```
                         ┌─────────────────────┐
                         │        CLI            │
                         │  (capture / status /   │
                         │   restore / daemon)    │
                         └───────────┬───────────┘
                                     │
                         ┌───────────▼───────────┐
                         │        Daemon           │
                         │  (long-lived process)   │
                         └───────────┬───────────┘
                                     │
        ┌────────────────────────────┼────────────────────────────┐
        │                            │                            │
┌───────▼────────┐        ┌──────────▼──────────┐       ┌─────────▼────────┐
│  Event Bus       │◄──────┤  Compositor Backend   │       │   State Store      │
│ (fan-out live     │       │  (interface)          │       │ (session model +   │
│  Niri events)     │       │   ├── Niri Backend    │       │  restore state,    │
└───────┬────────┘        │   └── Hyprland Backend │       │  on-disk + cache)  │
        │                  └──────────┬──────────┘       └─────────┬────────┘
        │                             │                             │
        │                  ┌──────────▼──────────┐                 │
        │                  │   Live Desktop Model  │                 │
        │                  │ (current windows/     │◄────────────────┘
        │                  │  workspaces, updated   │
        │                  │  from events)          │
        │                  └──────────┬──────────┘
        │                             │
┌───────▼─────────────────────────────▼────────┐
│              Reconciliation Engine              │
│  (saved state + live state → per-entity plan:   │
│   already-present / missing / ambiguous / etc.) │
└───────┬─────────────────────────────┬─────────┘
        │                             │
┌───────▼────────┐          ┌─────────▼─────────┐
│ Matching Engine  │          │  Restore Orchestr. │
│ (candidate window │◄────────┤  (per-entity state │
│  vs saved entity,  │        │  machine, Part 8)   │
│  confidence score) │        └─────────┬─────────┘
└──────────────────┘                    │
                              ┌──────────▼──────────┐
                              │ Application Providers │
                              │  ├── Generic           │
                              │  ├── Terminal           │
                              │  ├── Ghostty            │
                              │  ├── Browser            │
                              │  └── Editor             │
                              └───────────────────────┘
```

The Matching Engine and Restore Orchestrator are separated deliberately: **matching answers "is this candidate window entity X?"**, a pure question with a confidence score and no side effects. **The Orchestrator decides what to *do* with that answer** (place it, wait longer, mark ambiguous, skip). Keeping "judge" and "actor" separate is what lets you unit-test matching logic without ever touching a real compositor.

---

## Part 5 — Session Data Model

Design goals restated as concrete choices: **YAML** on disk (human-readable/diffable/editable, unlike JSON's no-comments limitation), one file per session, with an explicit schema `version` field from day one.

```yaml
schema_version: 1
session_id: "b3f1c2b0-primary"
created_at: "2026-09-03T10:00:00Z"
updated_at: "2026-09-03T14:12:00Z"
compositor: "niri"

workspaces:
  - persistent_id: "ws-continuum-dev"     # your own stable ID, NOT niri's runtime index
    display_name: "1: dev"
    is_favorite: true                      # startup workspace
    output_hint: "DP-1"                    # best-effort, not guaranteed on restore
    restore_state: "restored"              # not_started | in_progress | restored | partial

    entities:
      - persistent_id: "ent-ghostty-continuum-wm"
        app_id: "com.mitchellh.ghostty"
        kind: "terminal"                   # drives which provider handles it
        launch:
          command: ["ghostty"]
          env: {}
        matching_hints:
          title_pattern: null              # Ghostty titles are often unreliable; don't over-rely
          expected_provider: "ghostty"
        provider_metadata:
          cwd: "~/Projects/continuum-wm"
          cwd_confidence: "high"           # high | low | unknown — see Part 6
        layout:
          column_index: 0
          order_in_column: 0
          width_fraction: 0.5              # proportion of output width, NOT pixels
          floating: false
          fullscreen: false
        last_seen:
          niri_window_id: 42               # RUNTIME ONLY — advisory, never trusted after restart
          matched_at: "2026-09-03T14:10:00Z"

      - persistent_id: "ent-firefox-main"
        app_id: "firefox"
        kind: "browser"
        launch:
          command: ["firefox"]
        matching_hints:
          expected_provider: "browser"
        provider_metadata: {}              # browser owns its own tab state — see Part 14
        layout:
          column_index: 1
          order_in_column: 0
          width_fraction: 0.5
          floating: false
          fullscreen: false
```

Key modeling decisions:
- `persistent_id` is a **UUID or stable slug you generate at capture time**, never derived from anything Niri gives you. This is the identity that survives reboot (your Part 5 requirement, made concrete).
- `last_seen.niri_window_id` exists *only* as a debugging/observability aid and a same-session optimization — it is explicitly documented as untrusted across restarts, so nobody is ever tempted to treat it as identity.
- `width_fraction`, not pixels — matches Niri's proportional column model (Part 7/10 below) and degrades gracefully across resolution/scaling changes.
- `cwd_confidence` makes uncertainty a first-class, visible field rather than a silent guess.
- `restore_state` lives on the workspace, `provider_metadata` lives on the entity — different lifetimes, different owners.

---

## Part 6 — Capture Architecture

Capture has two triggers, and they should be treated differently:

1. **Explicit capture** (`continuum-wm capture`) — snapshot everything right now. Straightforward: enumerate workspaces/windows via Niri IPC, run each matched entity through its provider's capture hook (e.g., TerminalProvider reads `/proc/<pid>/cwd`), write the session file.

2. **Continuous/background capture** — updating the saved session as the live desktop changes, so you're not relying on the user remembering to run `capture` before every crash. This should be **debounced and event-driven**, not polling: subscribe to the Niri event stream, and on window-opened/closed/moved events, mark the affected workspace's saved entry as "dirty" and re-capture that workspace only, after a short debounce window (e.g. 2–3 seconds of quiet) to avoid re-capturing on every intermediate event during, say, a drag operation.

**Safety rule for capture itself:** capture must be **read-only** with respect to the compositor — it never issues move/resize/launch actions. This keeps capture safe to run frequently and even automatically, which matters because a session file that's stale by hours is much less useful than one that's a few seconds behind.

**CWD capture confidence, concretely:**
- Generic terminal (one process, one shell, unambiguous `/proc/<pid>/cwd`) → `cwd_confidence: high`.
- Ghostty-style (shared process, multiple PTYs) → the TerminalProvider/GhosttyProvider must have a way to disambiguate (e.g. Ghostty may expose per-surface metadata via its own IPC/`ghostty +list-windows`-style mechanism if one exists — this needs to be experimentally verified, don't assume) or else it must write `cwd_confidence: unknown` and `cwd: null` rather than a guess. On restore, `unknown` means: launch the terminal, but do not attempt `cd`.

---

## Part 7 — Matching Algorithm

This is the crux of the whole system, so let's be rigorous.

### The core rule
No single signal — PID, Niri window ID, title, or app ID — is ever sufficient **alone**. Matching is always a **weighted combination of signals**, producing a **confidence score**, evaluated against a **threshold with a deliberate gap between "confident" and "reject."**

### Signals, and why none is sufficient alone
| Signal | Why it's necessary but not sufficient |
|---|---|
| `app_id` | Necessary filter (a saved Firefox entity should never match a new Ghostty window) but multiple instances share it — it narrows the candidate pool, it doesn't pick within it. |
| Launch-transaction context | *You* launched this specific process for *this specific saved entity* — so a newly-appeared window that arrives shortly after your launch call, matching the expected `app_id`, is strong evidence — but not proof (the app might have opened an unrelated dialog, or the user might have independently launched the same app at the same moment). |
| Title | Useful corroborating signal, unreliable as primary — titles change (page navigation, file switching) and are sometimes generic ("Terminal"). |
| Provider-specific metadata | E.g., TerminalProvider resolving CWD post-launch and comparing it to the saved CWD — this is often your **strongest single signal** when available, because it's specific and hard to coincidentally match. |
| Workspace/monitor context | Weak positive signal (you asked it to launch targeting a workspace) — not proof, since Niri or the app may place it elsewhere. |
| Timing | A window that appears 30 seconds after you gave up waiting is *not* the one you launched — treat matches outside your timeout as new/unrelated, not late confirmations. |

### Scoring model
Compute a confidence score (I'd literally start with something as simple as a weighted sum, don't over-engineer this early):

```
score = w1*(app_id_matches)
      + w2*(within_launch_transaction_window)
      + w3*(provider_metadata_matches)     // e.g. CWD match
      + w4*(title_similarity)
      + w5*(workspace_hint_matches)
```

Then two thresholds, not one:
- `score >= CONFIDENT` → auto-match.
- `score < REJECT` → definitely not a match, keep waiting / try next candidate.
- **In between → `AMBIGUOUS`.** This middle band is not a bug to eliminate, it's the honest representation of "I don't have enough evidence" and per your own top-line principle, ambiguous must never silently resolve to a guess — it surfaces to the reconciliation layer as a distinct outcome (Part 12), logged and (eventually) presentable to the user.

### Why sequential launch matters for matching (validating your Part 13 instinct)
If you launch four terminals simultaneously, you get four `WindowOpened` events in unpredictable order with no way to attribute any one of them to a specific launch call — the "within launch transaction window" signal becomes useless because *every* new window is within *every* launch's window. Launching **one saved entity at a time**, and only proceeding to the next once the current one is matched-or-timed-out, is what makes that signal meaningful at all. This isn't just "safer," it's **structurally required** for your best matching signal to have any discriminating power. I'd formalize this as an architectural principle, not just an optimization (see Part 13's answer below).

---

## Part 8 — Restore State Machine

Per-entity state machine:

```
        SAVED
          │
          ▼
      REQUESTED  ──────────────► SKIPPED (workspace not activated yet; lazy)
          │
          ▼  (already-present check, Part 12, happens here first)
   ┌──ALREADY_PRESENT (reconciliation matched a live window with high confidence
   │      before any launch — no launch needed)
   │
   ▼
     LAUNCHING ───(spawn fails / app missing)───► FAILED
          │
          ▼
     OBSERVING ───(timeout, no candidate window appears)───► FAILED (timeout)
          │
          ▼
      MATCHING
      │        │
      ▼        ▼
   MATCHED   AMBIGUOUS ──► held for manual resolution / logged, entity stays AMBIGUOUS
      │
      ▼
    PLACING ───(niri rejects layout op)───► PARTIALLY_RESTORED
      │            (window exists and is roughly right, but exact
      │             placement/size failed — still usable, logged)
      ▼
   VERIFYING ───(post-check fails, e.g. window vanished)───► FAILED
      │
      ▼
   RESTORED
```

Notes on design choices:
- `ALREADY_PRESENT` is a distinct terminal-ish state from `RESTORED` even though both mean "the entity exists and is fine" — because it changes what actions were taken (no launch happened) and is important for idempotency logging (Part 12/13).
- `AMBIGUOUS` and `PARTIALLY_RESTORED` are **not failures** — they're first-class outcomes that a status command can report distinctly. Treating them as generic "error" would hide exactly the information (Part 24 principle) you most care about.
- Each transition is logged with timestamp + reason, since this state machine *is* your audit trail for Part 15 (failure recovery/debugging).

---

## Part 9 — Workspace Lazy Loading

Model this as **per-workspace restore-state**, driven off the Event Bus, independent of any global "restore everything" notion.

```
Daemon starts
     │
     ▼
Load session file → for each workspace, restore_state starts as
"not_started" (except any marked "restored" from a prior run this login)
     │
     ▼
Identify favorite workspace → immediately trigger its restore transaction
(runs the full per-entity state machine from Part 8, sequentially per entity)
     │
     ▼
Subscribe to WorkspaceActivated events for ALL other saved workspaces
     │
     ▼
On WorkspaceActivated(ws):
    if ws.restore_state == "not_started":
        trigger restore transaction for ws
    else:
        no-op   (already restored or in progress — see Part 12 idempotency)
```

Important subtlety: "activation" should probably be **debounced by a very short window** (e.g. don't trigger on a pass-through activation from someone scrolling through workspaces quickly with a keybind) — but don't over-engineer this without first observing real usage; a simple "activation lasted >300ms" check is a reasonable first cut, and this is exactly the kind of assumption worth validating experimentally (Part 16) rather than guessing up front.

### Addendum (post-Phase-3 finding): the "race" concern is largely a non-issue, and here's why

During Phase 1/3 research we confirmed a specific, citable Niri behavior: **each output maintains exactly one empty trailing workspace at a time, and the next empty workspace only appears once a real window is placed in the current trailing one.** (Niri's own wiki: "There's always one empty workspace at the end... When you open a window on this empty workspace, a new empty workspace will immediately appear further below it.")

This has a direct, favorable consequence for lazy restore: **a user physically cannot navigate to a not-yet-restored workspace ahead of where restoration has reached**, because that workspace doesn't exist yet from Niri's perspective — there's nothing to navigate to. If the user activates workspace N and immediately tries to jump to N+1 before our restore transaction has placed anything, `focus-workspace-down` (or equivalent) is a no-op, since workspace N is still empty and workspace N+1 hasn't been created.

Practically, this means:
- We do **not** need to build explicit ascending-order enforcement or locking to prevent a user from "getting ahead" of lazy restoration — Niri's own workspace-creation semantics already provide this guarantee, for free, per the project's general principle of preferring supported compositor behavior over reimplementing enforcement ourselves.
- We **do** still need to restore workspaces in ascending saved-idx order when doing so programmatically (e.g. if we ever batch-restore several workspaces without the user navigating between them) — the guarantee above is about user navigation, not about our own action ordering. If Continuum-WM itself tried to place a window at saved idx=3 while only one live workspace exists, that would still fail or misplace, since idx=3 doesn't exist yet on the live side either.
- This guarantee is **per-output** (each monitor has its own independent workspace stack) — worth re-confirming this holds as expected once multi-monitor restore is in scope, since "user can't get ahead" needs to be true independently on every connected output, not globally.

### New risk surfaced by this finding: stranded workspaces on restore failure

If restoring workspace N **fails entirely** (e.g. the one saved app for that workspace can't be launched, and nothing else is placed there), workspace N remains permanently empty from Niri's point of view. Per the mechanism above, this means **the user can never navigate past it to reach saved workspace N+1**, since Niri will never generate a "next" workspace without something landing in the current trailing one first.

This isn't a blocker for Phase 3–5, but it's a real Part 15 (failure handling) scenario to solve before Phase 6 ships: a saved workspace whose restore fails shouldn't silently strand every workspace after it. Candidate approaches to evaluate when we get there (not decided yet):
- On total restore failure for a workspace, still place *something* there (even an empty terminal, or a workspace named to signal "restore failed here") so the trailing-workspace mechanism keeps advancing.
- Surface this prominently in `continuum-cli status` so the user knows to intervene manually.

### New mechanism worth adopting: named workspaces as a restore-time pin

Also confirmed from Niri's docs: **named workspaces (`niri msg action set-workspace-name <name>`) are exempt from the empty-workspace pruning/renumbering behavior entirely**, and can be addressed by name instead of by (unstable) index.

Proposed refinement to the restore algorithm for Phase 5/6 (not yet implemented — Phase 3 restore-of-a-single-known-workspace didn't need this): the moment Continuum-WM places the *first* window into a workspace during a restore transaction, immediately name that live workspace using our own `persistent_id` (e.g. `set-workspace-name continuum-ws-a3372ecb`). This gives the rest of that restore transaction a **stable, durable address** for the workspace, immune to index renumbering caused by anything else happening concurrently (the user opening/closing things elsewhere, other workspaces being pruned, etc.) — meaningfully more robust than continuing to address it by `idx_hint` for every subsequent entity placement within the same transaction. Whether to leave the name in place afterward or unset it once restore completes is a minor UX decision to make when we build this (it would show up in things like Waybar's workspace indicator either way).

---

## Part 10 — Niri Layout and Window Size

Niri's scrolling model breaks the (x, y, w, h) mental model in a specific way: **columns exist in a horizontal scroll sequence per workspace, and a column's width is frequently expressed/managed as a proportion or a small set of preset ratios, not an arbitrary pixel value the compositor promises to preserve.**

Recommended representation (as encoded in Part 5's schema):
- **Column index + order-within-column** = your primary "layout identity" — this is what you can reconstruct with high confidence, because it's structural, not geometric.
- **Width as `width_fraction` of output width** — this degrades gracefully: if the monitor resolution/scaling changes, a proportion is still meaningful where a saved pixel width (e.g. `1847px` on a monitor that no longer exists at that resolution) is not.
- **Do not attempt to store or restore an exact pixel height for tiled windows** — Niri, like most scrolling WMs, largely manages vertical extent as "fill the column," so this dimension usually isn't meaningfully yours to restore at all. Verify this experimentally against `niri msg -j windows` output structure before writing restore code that assumes otherwise.
- **Floating windows are the exception**: for floating-state windows, actual (x, y, w, h) is meaningful and should be captured/restored as such, since floating windows in Niri behave more like a traditional WM.

Restore algorithm (approximate, deterministic, explicitly not "exact"):
1. Restore workspace membership first (move window to correct workspace).
2. Restore column membership/order using Niri's `consume-window-into-column` / `move-column-*` actions (verify exact action names against current `niri msg action --help` at implementation time — don't hardcode from memory).
3. Apply `width_fraction` via Niri's set-column-width action if available; if not, accept default width as an acceptable approximation and log it as `PARTIALLY_RESTORED` for that field specifically (fine-grained partial success, not just per-window).
4. Never treat step 3's failure as a reason to fail the whole entity — width is cosmetic relative to "the right window, in the right workspace, with the right content," which is the actual product goal per your Part 1 framing.

---

## Part 11 — Terminal/Application Providers

Yes, this abstraction is necessary — but scope it narrowly at first. A provider's contract should be exactly three optional hooks:

```
Provider interface:
    Matches(window) bool                 // "is this app_id/kind mine?"
    Capture(window) (metadata, confidence)
    ResolveCandidate(saved_entity, candidate_window) (score contribution)
```

- **GenericProvider**: no-op capture, contributes nothing extra to matching beyond app_id/title — this is the default and must always exist as a fallback, so every app_id has *some* provider even if nobody's written a specific one.
- **TerminalProvider**: `/proc/<pid>/cwd` capture, straightforward CWD-match scoring.
- **GhosttyProvider**: attempts a more precise CWD resolution strategy specific to Ghostty's process model; if it can't resolve unambiguously, explicitly returns `unknown` rather than falling back to TerminalProvider's naive logic (which would be actively wrong here, not just less precise).
- **BrowserProvider / EditorProvider**: initially near-generic — their main value is *deliberately declining* to attempt tab/project-state capture (Part 14 boundary), so the generic core doesn't accidentally try to do an app's job.

The core (Matching Engine, Orchestrator) never contains app-specific logic — it only calls into whichever provider `Matches()` the window/entity. This is the seam that keeps your core compositor-independent and app-independent, which serves both your Hyprland goal and your "don't reimplement app state" boundary (Part 14) simultaneously.

---

## Part 12 — Reconciliation and Safety

Model outcomes as a proper enum evaluated **before** any launch happens, per entity, at the moment its workspace restore transaction begins:

```
ReconciliationOutcome:
  ALREADY_PRESENT   — a live window matches this saved entity with CONFIDENT score
                       → skip launch, jump straight to VERIFYING/RESTORED, possibly
                         still apply layout/placement corrections
  MISSING           — no live window plausibly matches → proceed to LAUNCHING
  AMBIGUOUS         — multiple live windows are each plausible, none decisively →
                       do NOT launch (avoid creating a 3rd competitor), do NOT touch
                       any candidate, surface as AMBIGUOUS, require next reconciliation
                       pass or manual input
  CONFLICTING       — a live window occupies the "slot" (e.g. same workspace+column
                       position) but clearly does NOT match (different app) →
                       do not move/close it; place the restored entity elsewhere in
                       the same workspace and log the conflict
```

**Idempotency** falls directly out of `ALREADY_PRESENT`: re-running restore on a workspace re-evaluates reconciliation fresh each time. If everything's already matched with confidence, every entity resolves to `ALREADY_PRESENT` and nothing launches — no extra state needed beyond "re-run reconciliation before doing anything," which is a nice property: **idempotency is a consequence of reconciliation-first design, not a separate mechanism you have to build and keep in sync.**

The one thing you *do* need persisted beyond the session file: a lightweight **in-memory (or lightly persisted) "restore transaction lock" per workspace**, so that if a user mashes the activation trigger twice quickly, you don't start two concurrent restore transactions for the same workspace racing each other. A simple per-workspace mutex/flag in the daemon is sufficient — this doesn't need to survive daemon restart, since a fresh daemon start naturally re-does reconciliation from scratch anyway.

---

## Part 13 — Failure Recovery

- **Timeouts**: per-entity, configurable, sane default (~10s for `OBSERVING`). On timeout → `FAILED(timeout)`, move on to next entity. Never block the whole workspace restore on one slow entity.
- **Retry policy**: exactly one retry for `LAUNCHING`/`OBSERVING` failures by default (apps sometimes are just slow to open their first window), no automatic retry for `AMBIGUOUS` (retrying won't resolve ambiguity, more candidates might make it worse) — surface it instead.
- **Confidence/ambiguity handling**: already covered in Parts 7/12 — the key recovery-relevant point is that every non-RESTORED terminal state is **queryable later** (`continuum-wm status`), not just logged and forgotten.
- **Rollback**: generally **avoid rollback** — per your own priority list (non-destructive > everything), the safer default on failure is "leave whatever exists, however imperfect, alone" rather than trying to undo partial actions, which risks doing more damage than the original failure. Exception: if you yourself launched a process and it's clearly not going to produce a matchable window (e.g. it crashed), it's safe and correct to kill *that specific PID you just spawned* — that's cleanup of your own action, not rollback of ambiguous state.
- **Crash recovery**: since restore state lives primarily in reconciliation-computed-fresh-each-time (Part 12), a daemon crash mid-restore is safe to recover from by simply restarting the daemon and letting it re-run reconciliation for any workspace that wasn't fully `RESTORED` — no special crash-recovery code path needed, which is a strong argument for keeping reconciliation cheap and stateless-where-possible.
- **Logging**: structured (JSON lines) log per restore transaction — entity id, state transitions, scores, timestamps. This becomes your primary debugging tool and should be considered a first-class deliverable, not an afterthought.
- **User-visible status**: a `continuum-wm status` command showing, per workspace, per entity: current state, confidence score if matched, and reason if failed/ambiguous.

---

## Part 14 — Application-Owned State Boundary

```
Continuum-WM responsibility:
  - which app, which workspace, which column/position, floating/tiled, rough size
  - working directory for terminals (via provider)
  - launching the process

Application responsibility:
  - browser: which tabs, history, session restore (browsers already do this — invoking
    a browser's own "restore last session" is often better than Continuum-WM tracking tabs)
  - editor: which files/project open (VS Code's own `--folder-uri` / recent-workspaces,
    Neovim's own session files)
  - any in-app document/undo state
```

The practical rule: **Continuum-WM's job ends at "the right process is running, in the right place, pointed at the right starting context (like a CWD)."** Where an app has its own native restore mechanism, invoking it (e.g. launching with the right `--folder` flag, or simply trusting the browser's built-in session restore) is strictly preferred over Continuum-WM trying to track and replay that state itself — less code, less to get wrong, and it respects the application's own more-detailed model of its own state.

---

## Part 15 — Testing Strategy

**Testing pyramid:**

- **Unit tests** (fast, no compositor): Matching Engine scoring logic against synthetic candidate/entity pairs; Reconciliation outcome logic against synthetic live+saved state; session schema serialization round-trips; provider CWD-parsing logic against fixture `/proc` data.
- **Integration tests** (real Go code, mocked `CompositorBackend` interface): full state machine transitions driven by a fake backend that emits scripted events — verifies orchestrator logic without needing Niri running at all.
- **Niri-integration tests** (require a real or headless Niri instance, e.g. under a nested Wayland session): verify actual IPC calls parse correctly against real `niri msg` output, verify actions actually produce the expected event.
- **End-to-end tests** (scripted, real Niri, real (lightweight, e.g. `foot`/test apps) applications): the actual scenarios in your Part 21 list — four terminals with different CWDs, lazy workspace restore, idempotent re-restore, manual-window-before-restore reconciliation.
- **Manual acceptance tests**: logout/login, reboot, daemon crash mid-restore, monitor reconfiguration — scenarios too environment-dependent to reliably automate early on.

**The most important acceptance tests, concretely, to write first:**
1. Four terminals, four different CWDs, saved and restored sequentially → all four correctly distinguished (this directly validates your core value proposition).
2. Restore workspace twice in a row → no duplicate windows (idempotency).
3. Manually open an app in a not-yet-restored workspace, then trigger its restore → the manual window is recognized as `ALREADY_PRESENT`, no duplicate launched.
4. Activating workspace B before A has finished restoring doesn't corrupt A's in-progress transaction (concurrency safety).

---

## Part 16 — Risks / Unknowns to Validate Experimentally *Before* Committing Further

Be honest about what's assumed vs. proven so far:

1. **Whether Niri exposes any per-column width query/set primitive precisely enough to round-trip a `width_fraction`** — you've noted size restoration is "partially constrained" but haven't fully characterized *what specifically* is and isn't controllable. Worth a focused half-day experiment against current `niri msg action --help` and the IPC schema before designing Part 10's algorithm in detail.
2. **Whether Ghostty (or any provider you target) exposes any IPC/introspection surface for per-window CWD at all** — if it genuinely doesn't, GhosttyProvider's job is entirely "return unknown, never guess," which is fine, but confirm this rather than assuming it before writing provider code.
3. **Actual behavior of `WorkspaceActivated` events under rapid switching** — validate the debounce assumption from Part 9 against real usage before hardcoding a threshold.
4. **Whether Niri's window IDs are stable enough *within a single session* to skip re-matching on every event** (an optimization, not a correctness requirement — but worth knowing before you build unnecessary polling logic).
5. **Output/monitor identity stability** — confirm whether Niri identifies outputs by a stable name (e.g. `DP-1`) or something that can change, since your `output_hint` restore-safety story depends on this.

**Update, post-Phase-3:** item 3 above is resolved, and better than expected — see Part 9's addendum. Confirmed via Niri's own documentation (not just observation) that empty trailing workspaces are created one-at-a-time and only advance once populated, which structurally prevents the user from navigating ahead of lazy restoration. No debounce-threshold guesswork needed after all; the mechanism is a hard guarantee, not a timing heuristic. This also surfaced a new, previously-unlisted risk: restore failure on a workspace can strand navigation past it (see Part 15/9 addendum) — added to the roadmap as a Phase 6 concern.

Also confirmed via real testing (Phase 3): NixOS-packaged applications' `/proc/<pid>/cmdline` captures a Nix store path containing a content-addressed hash (e.g. `/nix/store/f0328rw.../bin/.ghostty-wrapped`), which will very likely become invalid after any Nix flake update or system rebuild that changes the package's derivation hash. This is a real, NixOS-specific fragility in generic launch-command capture that a plain-`$PATH`-name fallback (e.g. preferring `ghostty` over the literal captured store path when they resolve to the same app) should address in a later phase — noted here rather than fixed immediately, consistent with not over-building ahead of need.

None of these block starting Phase 1 — they block committing to *exact* mechanisms in Parts 7/9/10, which is exactly why the roadmap below front-loads observation before any restore logic.

---

## Part 17 — Development Roadmap & First Implementation Step

### Roadmap (reordered from your draft based on dependency risk)

```
Phase 1 — Reliable Niri Observation
  Goal: read-only, rock-solid workspace/window enumeration + event stream parsing.
  Gate: can print a live, correctly-updating view of all workspaces/windows for 30+ min
        without crashing or missing an event.

Phase 2 — Persistent Session Model
  Goal: schema (Part 5) + serialize/deserialize with versioning, no restore logic yet.
  Gate: capture a real session, write YAML, read it back, byte-for-byte round-trip of
        all fields.

Phase 3 — Single-Window Restore (no matching ambiguity yet — one saved entity, one app)
  Goal: prove the launch → observe → match → place state machine (Part 8) end to end
        for the simplest possible case.
  Gate: kill one tracked window, run restore, correct window reappears in correct
        workspace.

Phase 4 — Multiple-Instance Matching
  Goal: prove the scoring/confidence model (Part 7) against your hardest real case —
        four terminals, four CWDs.
  Gate: your Part 21 test #1, automated, passing repeatably (not just once).

Phase 5 — Reconciliation & Idempotency
  Goal: ALREADY_PRESENT / AMBIGUOUS / CONFLICTING outcomes (Part 12), re-run safety.
  Gate: Part 21 tests #2 and #3, automated.

Phase 6 — Workspace-Level Restore + Lazy Loading
  Goal: favorite-workspace-on-login, on-demand others (Part 9).
  Gate: full multi-workspace session, only favorite restores at "login" (simulated),
        others restore only on simulated activation.

Phase 7 — Layout/Size Restoration
  Goal: column/order/width_fraction restoration (Part 10), now that Phase 6 gives you
        a real multi-window-per-workspace scenario to test it against.
  Gate: visually/structurally correct column order for a saved multi-column workspace.

Phase 8 — Application Providers
  Goal: TerminalProvider generalized, GhosttyProvider's honest unknown-handling,
        Generic fallback formalized.
  Gate: provider-specific unit tests (Part 15) passing; unknown-CWD case doesn't
        attempt a `cd`.

Phase 9 — Hardening
  Goal: failure/timeout/retry (Part 13), structured logging, `status` CLI,
        daemon/systemd packaging.
  Gate: full Part 21 failure-scenario matrix passing, plus one real logout/login
        and one real reboot cycle, manually verified.
```

This reordering puts **Reconciliation (old Phase-numbering "later") right after basic matching and before layout** — because idempotency/safety is higher priority than layout fidelity per your own stated priority list, and because getting reconciliation wrong is a correctness bug while getting layout wrong is a cosmetic shortfall.

### First implementation step — deliberately small

**What we're proving:** that we can reliably observe Niri's live state and event stream from Go, continuously, without drift or crashes — literally nothing else. No restore logic, no matching, no launching.

**What we build:**
- A single Go binary, `continuum-observe`.
- Uses `niri msg -j workspaces` and `niri msg -j windows` once at startup to print a baseline.
- Subscribes to `niri msg -j event-stream`, parses each JSON line, and prints a human-readable line for every event (window opened/closed/moved, workspace activated).
- No file writes. No compositor mutation. Purely observational.

**How we test it:** run it in a terminal, then in a separate session do ordinary things for 15–20 minutes — open/close windows, switch workspaces, resize things, connect/disconnect a monitor if you can. Watch the output.

**PASS:** every action you take produces a corresponding, correctly-parsed event line, with no missed events, no parse errors, no crash, and the periodic "full state" you can re-query matches what you did.

**FAIL:** any crash, any JSON parse failure, any event that doesn't show up, or state drift (a query of current windows disagreeing with what the event stream implied).

**What we should learn before proceeding to Phase 2:** the *actual* shape of Niri's JSON (field names, what's nullable, what output-identity looks like, what happens to events across a monitor hotplug) — because Part 5's schema and Part 7's matching signals should be designed against real observed data, not against documentation-derived assumptions. This is intentionally the cheapest possible experiment that de-risks everything downstream.

---

## Closing note

Your instincts throughout — persistent vs. runtime identity, sequential launch-and-match, ambiguity as a first-class outcome rather than a forced guess, lazy per-workspace restore, "partial-but-correct beats complete-but-wrong" — are all structurally sound and match how I'd design this myself. The main things this document adds are: making Matching and Reconciliation separate first-class components rather than folding them together, choosing Go with concrete reasoning tied to your actual workload shape rather than general reputation, and re-sequencing the roadmap so idempotency/safety lands before layout fidelity, consistent with your own priority ordering.
