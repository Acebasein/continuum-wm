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

## Implementation Findings — Phases 3 through 5 (Real-World Testing)

This section records what actually happened once the design above met a real Niri session, real applications, and real hardware. Kept separate from the original design sections above so the reasoning that led to each decision stays traceable, rather than silently rewriting history.

### Capture-side findings

**NixOS launch-command fragility (Phase 3).** `/proc/<pid>/cmdline` on NixOS frequently resolves to a content-addressed Nix store path (e.g. `/nix/store/f0328rw.../bin/.ghostty-wrapped`), which breaks the moment a system rebuild changes that package's derivation hash. **Fix adopted:** derive a plain, `$PATH`-resolvable command name from `app_id` (last reverse-DNS segment, lowercased -- e.g. `com.mitchellh.ghostty` → `ghostty`), verified via `exec.LookPath` at capture time, storing only the plain name (never the resolved absolute path) so `$PATH` re-resolves fresh at restore time. Falls back to the raw `/proc/<pid>/cmdline` capture when the heuristic doesn't resolve. This approach was validated against `nirinit`, a comparable existing niri session tool, which independently arrived at the same solution (see the nirinit code-review conversation).

**Shared-PID windows are not a Ghostty-specific quirk.** Confirmed directly: GNOME/GTK single-instance apps (Nautilus) report the SAME pid for multiple distinct windows. Any generic "read CWD from the window's pid" approach is unsafe for any app with this architecture, not just terminals. Capture now counts windows per pid and marks CWD `unknown` whenever a pid is shared, rather than guessing.

**Terminal emulator CWD requires reading the CHILD shell process, not the terminal's own pid.** Confirmed experimentally: Kitty and Ghostty's own process cwd reflects wherever they were launched from (stale), not where the user has since navigated to inside the shell. Fix: resolve CWD via the terminal's child process instead. Refined further after finding Kitty spawns a `kitten __atexit__` helper process alongside the real shell -- a naive "exactly one child" check saw two children and incorrectly reported ambiguity. Fixed by filtering children to a known-shell-name allowlist (`bash`, `zsh`, `fish`, etc.) before counting. Deliberately does NOT fall back to the terminal's own stale cwd when zero recognized shells are found -- that fallback was tried and reverted, since it silently reintroduces the exact bug this fix was for.

**CORRECTION (superseding an earlier conclusion in this same section): Ghostty's shared-pid problem is NOT a fundamental per-window CWD limitation.** Originally documented above (and carried through Phase 4/5) as "Ghostty shares one process across multiple windows, so CWD must always be `unknown` for it." This turned out to be true only for windows opened via Ghostty's OWN internal new-window shortcut, which asks the already-running instance to spawn another window, sharing that instance's pid. Confirmed directly: two Ghostty windows launched as SEPARATE processes (`ghostty &` run twice, or `continuum-cli`'s own `LaunchDetached` -- i.e. exactly how restore actually creates every window) report entirely distinct pids, with no ambiguity at all. Verified end-to-end: capture now reports `confidence=high` with correct, distinct CWDs for two separately-launched Ghostty windows, and restore correctly places both in their right directories -- no new code was needed, `resolveCWD`'s existing shared-pid check already handles this correctly once the actual condition is tested rather than assumed. Net effect: Ghostty is no longer a documented limitation for entities that were themselves captured from separately-launched windows (the common case for anything continuum-wm itself restored). The ORIGINAL shared-pid scenario (multiple windows opened via Ghostty's internal shortcut, sharing one pid) remains real and still correctly falls back to `unknown` -- that part of the finding stands, it was just wrongly generalized to "Ghostty, always."

**SECOND CORRECTION (the above was ALSO premature): a real post-reboot `restore-all` test produced three Ghostty windows sharing ONE pid again, despite each being launched via a separate `continuum-cli` call exactly as before.** Root cause, confirmed via Ghostty's own source (a GitHub commit adding the `+new-window` CLI action, explicitly documented as "use a D-Bus method call to tell a running Ghostty instance to open a new window"): Ghostty's GTK/`GApplication` layer can register itself for D-Bus session activation, meaning a plain `ghostty` invocation is not guaranteed to start a genuinely separate process -- it can silently be routed to an already-running instance depending on session/login timing, independent of the `gtk-single-instance` config key (which appears to govern a narrower behavior, not this). The earlier "confirmed distinct pids" result was real but NOT general -- it depended on no backing Ghostty instance already being alive at the time, which can't be assumed after a normal login. Two of the three post-reboot windows were silently placed at the wrong CWD as a result; our own verification step only caught one of the three, because by the time the second and third were checked, the shared pid already had multiple shell children, which correctly resolves to "can't verify" rather than "reject" -- an honest gap in the verification's coverage, not a bug (no-evidence was never treated as reject-evidence, by design, but this is worth remembering as a real limit of that safety net).

**ACTUAL FIX, confirmed working end-to-end:** rather than fighting Ghostty's process/instance topology, use Ghostty's own built-in mechanism for this: `ghostty +new-window --working-directory=<path>`. This is an explicit instruction to whichever instance ends up creating the new window, entirely independent of process-level `cmd.Dir` inheritance -- it doesn't matter anymore whether Ghostty collapses into one shared process or not. Implemented as a small, targeted override (`buildLaunchCommand` in `internal/restore/restore.go`) for Ghostty entities with a high-confidence saved CWD -- an early, minimal precursor to the full Application Provider architecture (Part 11), not yet a pluggable interface. Verified: 3 separately-restored Ghostty windows, 3 distinct saved CWDs, all landed correctly. Note this fix is restore-side only -- capture-side CWD detection (shared-pid → `unknown`) is unchanged and still correctly conservative when Ghostty windows were opened via its own internal shortcut rather than by continuum-wm itself.



**XWayland/X11 apps report the compatibility bridge's pid, not the app's own.** Confirmed with ONLYOFFICE: its window's reported pid belongs to `xwayland-satellite`, not ONLYOFFICE itself. Captured `launch.command` was `xwayland-satellite`'s own argv, including session-specific file descriptor numbers (`-listenfd 91 -listenfd 54`) that are meaningless in a new process -- confirmed this fails restore with a launch timeout every time. This is a distinct failure category from the shared-pid problem above: a proxy process fronting for a window that belongs to a different, unrelated process. Not yet fixed -- likely needs an X11/XWayland-aware Application Provider (Phase 8) that resolves the real client PID rather than the bridge's.

**GUI file managers don't expose "location" via CWD at all.** Confirmed with Nautilus: unlike a terminal, a GTK file manager does not `chdir` its own process to the folder it displays -- the browsed folder is pure in-app state, invisible to `/proc`. This is a different problem than CWD-confidence scoring; a real fix needs a provider that launches with an explicit path argument (e.g. `nautilus /home/som/Downloads`), which is Phase 8 scope, not a capture-side fix.

### Restore-side findings

**Launched processes must be given an explicit working directory -- never rely on inheritance.** Found via real testing: without setting `cmd.Dir` explicitly, every launched process inherited `continuum-cli`'s OWN current directory (standard Unix fork/exec behavior) -- causing every restored terminal to silently open wherever the operator happened to invoke the tool from, regardless of each entity's saved CWD, and causing multiple simultaneously-restored entities to all land in the identical (wrong) place. Fixed by passing the saved CWD through to `cmd.Dir` explicitly. For entities with no known CWD (Ghostty, non-terminals), default explicitly to `$HOME` rather than leaving `cmd.Dir` unset -- otherwise the exact same invoker-dependent bug resurfaces for exactly the apps that can't be fixed any other way. This also matters for the eventual daemon (Part 18): a background service has no meaningful "invoking directory" at all, so relying on inheritance was never going to be viable long-term regardless.

**Multi-instance disambiguation via CWD works, including under a real race condition.** Implemented a "settle window" (750ms, unvalidated beyond this initial guess) after the first candidate window appears, during which additional candidates are collected rather than committing immediately. When multiple candidates appear, CWD comparison (for known terminals with high-confidence saved CWD) is used to actively pick the correct one, not just verify a single candidate post-hoc. Verified against a REAL race: manually launched a competing Kitty window via a niri keybind while a restore was in-flight -- the correct window was identified via cwd match and placed; the competing manual window was left completely untouched. When CWD can't resolve the ambiguity (multiple matches, zero matches, or no reliable CWD available at all -- e.g. two racing Ghostty windows), the result is `AMBIGUOUS` and nothing is touched, never a guess.

**New risk category: some apps produce more than one window ID from a single launch, indistinguishable from a real multi-instance race.** Observed with Typora: a single `typora` launch produced two distinct new window IDs, triggering `AMBIGUOUS` with no actual competing instance involved. Root cause (plausible, app-specific): a first-run/free-tier license dialog presented as a separate top-level window alongside the main one. This generalizes beyond Typora -- ANY app that can show a license prompt, first-run wizard, update nag, or "restore previous session?" dialog on launch will trip the same path. This is not a bug: `AMBIGUOUS` is the correct, safe outcome when we can't tell which window is "the real one," even though the actual cause here (a nag dialog) turned out to be benign. No fix planned -- documented as an accepted, expected category of ambiguity.

**Reconciliation (`ALREADY_PRESENT`) is currently only possible for known terminals with a high-confidence CWD.** Every other app (and, per the correction above, Ghostty windows sharing a pid via its own internal new-window shortcut) has no reliable per-instance identity signal, so `Reconcile` always falls through to a fresh launch for them -- meaning restore is genuinely NOT idempotent yet for those. Verified directly: repeated restore attempts against a live window with no resolvable CWD correctly reported "no confirmed pre-existing match" every time and launched a new window each time. This is an accepted, named gap for Application Providers (Phase 8) to eventually close, not a defect in Phase 5's logic. Note this gap no longer applies to Ghostty windows that were themselves separately launched (see correction above) -- only to the narrower shared-instance case.

**"Conflicting" doesn't need blocking/eviction logic in Niri's model.** The original design (Part 11) imagined a live window occupying the same "slot" as a saved entity, requiring a decision to place elsewhere. Niri's scrolling layout has no such contention -- a workspace simply accumulates windows side by side, so placing a new one never requires evicting anything. Implemented as informational only: `Reconcile` checks for and reports unrelated live windows already on the target workspace, without altering placement behavior. Verified against a real desktop with 4 unrelated apps already occupying a target workspace -- correctly listed all of them, placed the restored entity alongside them, touched nothing.

**Possible workspace-idx drift during a long-running batch restore -- suspected, not yet confirmed.** One `restore-all` run produced a single `PARTIALLY_RESTORED` result ("window exists but is not on the expected workspace") for an entity restored several minutes into a long batch (which included a full 10-second timeout on an unrelated failed entity just before it). Leading theory: Niri's empty-workspace pruning/renumbering (see Part 9 addendum) shifted which live workspace a saved `idx_hint` pointed to, between when the batch started and when this particular entity was reached -- exactly the failure mode the named-workspace-pinning idea (Part 9 addendum, inspired by nirinit) was proposed to prevent. Not yet root-caused with certainty; worth deliberately reproducing (e.g. a batch restore spanning several minutes, checking workspace idx/id stability throughout) before deciding whether to implement named-workspace pinning now or continue monitoring.

**`restore-all` is sequential-only, ascending by saved workspace idx, and non-aborting on failure.** Confirms Part 13's sequential principle in practice, not just in design: entities are restored one at a time, in ascending idx order (a batch-specific requirement Part 9's addendum did NOT eliminate -- that addendum only established that a *human navigating workspaces* can't get ahead of lazy restoration; a *program* issuing placement commands directly can still target an idx that doesn't exist yet if it goes out of order). A single entity failing (timeout, ambiguity, etc.) does not stop the rest of the batch -- confirmed directly: an ONLYOFFICE timeout and a Nautilus timeout both occurred mid-run without aborting the remaining entities.

**Ghostty per-window CWD capture is confirmed structurally impossible for shared-process windows -- not a gap in effort, a real constraint.** Checked directly: `ghostty +list-actions` returns Ghostty's complete, exhaustive set of CLI actions (`+version`, `+list-fonts`, `+list-keybinds`, `+list-themes`, `+list-colors`, `+list-actions`, `+ssh-cache`, `+edit-config`, `+show-config`, `+validate-config`, `+show-face`, `+crash-report`, `+boo`, `+new-window`) -- there is no window-enumeration or introspection action of any kind. This means: for the ordinary, everyday way people open a second terminal window (Ghostty's own new-window shortcut, which shares one process across windows), there is currently no available mechanism -- not `/proc` inspection, not `-e`, not `+new-window --working-directory`, not a different restore-side matching architecture -- that can determine which existing window corresponds to which directory. `+new-window --working-directory=<path>` (see the earlier fix in this section) correctly solves LAUNCHING into a known directory; it does nothing for DISCOVERING that directory in the first place for windows that already exist. These are two different problems, easily conflated -- confirmed the hard way, through a real disagreement about whether this counted as "already solved." It doesn't, for this specific case. A genuine fix would require Ghostty itself to expose some form of window/session introspection, which does not currently exist.

**CORRECTION: "structurally impossible" above was incomplete -- it's structurally impossible GIVEN GHOSTTY'S DEFAULT CONFIGURATION, and there is a real, user-facing fix.** Confirmed directly: setting `gtk-single-instance = false` in Ghostty's own config file (`~/.config/ghostty/config`) -- or per-invocation via `ghostty --gtk-single-instance=false` -- stops the GTK/D-Bus single-instance activation entirely -- every subsequent Ghostty window becomes its own genuinely separate process, with its own PID, exactly like Kitty already was. With that setting in place, capture's existing shared-pid detection correctly finds NO shared pid at all (since there isn't one), and resolves each window's CWD correctly via the same child-shell logic already in place. By default, Ghostty leverages single-instance architecture specifically to conserve memory and speed up window initialization -- this is a deliberate performance tradeoff on Ghostty's part, not an oversight, which is worth keeping in mind when recommending the override to a user (it has a real, if usually minor, cost).

**Follow-through, implemented:** `buildLaunchCommand`'s fallback path (used whenever a Ghostty entity has no known CWD) now explicitly appends `--gtk-single-instance=false` to the launch command, overriding whatever Ghostty's own `.desktop` file set (confirmed: Ghostty's default `.desktop` `Exec=` line explicitly sets `--gtk-single-instance=true`, meaning our OWN restore path was previously re-triggering the exact collapsing behavior that causes this limitation in the first place, every time it restored a CWD-unknown Ghostty entity). This doesn't recover the CWD for the CURRENT restore -- that information is genuinely gone -- but it means the window this creates will be a genuinely separate process, so a FUTURE capture of it can resolve its CWD correctly, where it couldn't before. Effectively turns a permanent "unknown" into a self-correcting one across successive sessions, with no user configuration required. Documenting the manual global-config fix (for users who want full fidelity from the very first capture) remains worthwhile as a README/setup recommendation, but is no longer the only lever available.

**IMPORTANT REFINEMENT, confirmed via real testing: the self-correction above only reliably holds for a SINGLE Ghostty entity per restore run -- restoring two or more in the same run can undo it.** Confirmed directly: two Ghostty entities, both with high-confidence saved CWDs, both restored via `+new-window --working-directory=<path>` (the OTHER code path, used when CWD IS known -- distinct from the fallback path described above). PLACEMENT was completely correct for both (verified against actual `/proc/<pid>/cwd` of each window's real shell, not just the title text, which can lag) -- `+new-window` reliably honors `--working-directory` regardless of what follows. However: the SECOND `+new-window` call joined the FIRST instance via D-Bus (the same join-an-existing-instance behavior as Ghostty's own manual new-window shortcut), leaving both windows sharing one pid again. This means the NEXT capture will mark both `unknown` again, even though today's restore placed each one perfectly. Net effect: whenever 2+ Ghostty entities are restored together, placement remains fully correct every time, but future re-capturability oscillates rather than staying fixed. The `--gtk-single-instance=false` override added to the fallback path does NOT apply here, since that code path is never reached when a CWD is already known -- whether `+new-window` itself respects the same global config setting (per the manual fix, `gtk-single-instance = false` in Ghostty's own config) was not verified in this session; if it does, that remains the one lever that fixes both placement AND future trackability simultaneously, for any number of Ghostty entities. Worth confirming directly before relying on it.

---

### CORRECTION: monitor hotplug mid-run is ALREADY supported -- not a gap after all

Previously assumed (and told to the user) that a monitor connected *after* `lazy-restore` starts would be unreachable without restarting the process, since the workspace lookup is only built once at startup. Confirmed via real testing that this assumption was WRONG: the saved-workspace lookup (`byKey`) is built from the FILE, not from what's currently connected, so entries for a disconnected monitor's workspaces were never removed, only unreachable for the moment. Separately, the existing `WorkspacesChanged` event handler (added for the renumbering-safety fix, Part 9) already refreshes the live id->key map whenever niri's workspace list changes -- which includes a newly-connected monitor creating its own workspace stack. The combination, entirely incidental (neither piece was built with hotplug in mind), turned out to fully support it: reconnect a monitor mid-run, navigate to one of its saved workspaces, and it restores correctly, no restart needed. Verified directly by the user reconnecting HDMI-A-3 mid-session and having its saved workspaces correctly, automatically dispatched. The startup warning for a disconnected output was corrected to reflect this ("no action needed, will restore automatically once reconnected and visited") rather than the original, inaccurate "restart required" wording.

### KNOWN LIMITATION (documented, closed for now): Ghostty multi-instance CWD tracking

**Summary for anyone hitting this fresh, without re-reading the full investigation above:**

- **Placement always works correctly.** Whenever Continuum-WM restores a Ghostty window with a known saved CWD, it lands in the right directory every time, regardless of anything else happening on the system. This is not in question.
- **Future CWD *capture* can be unreliable across restores of 2+ Ghostty windows in the same run**, because Ghostty's default configuration routes new windows through a shared, single-instance process via D-Bus -- which makes it impossible to tell, after the fact, which window owns which directory.
- **The fix is a one-line change to the user's own Ghostty config, not something Continuum-WM can or should override globally:**

  ```
  # ~/.config/ghostty/config
  gtk-single-instance = false
  ```

  This gives every Ghostty window its own process, permanently, matching Kitty's already-reliable behavior. The tradeoff (per Ghostty's own defaults) is marginally higher memory use and slightly slower new-window startup -- deliberate on Ghostty's part, not a bug, and negligible for most users.
- Continuum-WM does what it reasonably can on its own (forcing separation on restore when no CWD was known, to self-heal over time) but cannot fully guarantee reliable multi-window CWD tracking for Ghostty without this user-side setting. Recommending it in the project's README/setup docs is the right long-term answer, not further code changes.

### KNOWN LIMITATION (closed permanently, upstream, not fixable on our side): Nautilus has no location-restore capability to build against

Distinct from the Ghostty situation above, in an important way worth being precise about: Ghostty's "limitation" turned out to be a real capability that was simply undocumented/overlooked (`gtk-single-instance`) -- a five-minute check found a genuine fix. Nautilus's case is different.

**Confirmed via direct research, not assumption:** GNOME's own minimalist design philosophy deliberately does not include session/location persistence for Nautilus -- unlike Dolphin (KDE), which has "a robust state-saving framework" that saves exact directories, tabs, and panes across restarts. This has been an open, actively-tracked feature request in GNOME's own issue tracker for years (`GNOME/nautilus#1196`) and has not been built. There is no config flag to flip, no hidden setting, no dconf key -- checked directly (`dconf dump /org/gnome/nautilus/`, `~/.config/nautilus`, `~/.local/share/nautilus`), nothing relevant found. This isn't "not yet investigated" -- it's a genuine, permanent upstream gap.

**What this means for Continuum-WM concretely:** launching Nautilus into a specific folder (`nautilus /path/to/folder`) is a normal, trivially-supported CLI argument -- that part was never the hard problem. The actual blocker was always CAPTURE: knowing which folder a specific, currently-open Nautilus window is displaying, so we'd know what path to pass on restore. Nautilus doesn't `chdir` its own process (ruling out the CWD approach that works for terminals) and, now confirmed, doesn't persist that information anywhere else on disk either. There is no remaining data source to read it from, upstream or otherwise.

**Decision: no further Nautilus-specific work planned.** For anyone who wants their file manager to genuinely remember where they were, Dolphin is a real, working alternative today -- confirmed via real testing (see the Phase 9 systemd daemon test above) that it correctly restored to its exact prior location, with zero help from Continuum-WM, purely from its own built-in session handling. Recommending Dolphin over trying to force this capability into Nautilus is the pragmatic answer, consistent with this project's general stance of not reimplementing what an application already owns (Part 14) -- and in Nautilus's case, of not attempting to build a capability the application's own maintainers have deliberately chosen not to provide.


**A large batch (`lazy-restore` across two monitors, rapid workspace-switching) produced one `FAILED` timeout (`google-chrome`) on an app whose window did visibly open.** Same pattern as the earlier ONLYOFFICE/Firefox findings -- consistent with the load/timing theory, not a new defect. Also notable: `google-chrome`'s app_id correctly resolved via the `.desktop` lookup to `google-chrome-stable` (the real binary name) -- confirms the `.desktop`-lookup mechanism, originally built to fix ONLYOFFICE, generalizes cleanly to other apps never specifically tested against it.

**VS Code restores correctly out of the box, no special handling needed.** Confirmed: capturing and restoring a VS Code window landed it back in the exact folder it was working in, via nothing more than the generic launch mechanism (a plain `code` command, folder argument implicit in VS Code's own recently-opened-workspace behavior). This is Part 14's app-ownership boundary working exactly as intended -- Continuum-WM doesn't need to know or restore anything about VS Code's internal state; VS Code's own native session restore does that job once it's launched into roughly the right context.

**A large `restore-all` batch (12 entities) produced two `FAILED` timeouts (Firefox, ONLYOFFICE) on apps that work correctly when restored individually.** Leading theory, not yet fully confirmed: resource contention from launching many real GUI applications back-to-back with a fixed 10-second per-entity timeout, rather than a matching or launch-logic defect -- supported by later evidence that ONLYOFFICE's window did visibly open even when the run reported `FAILED`, meaning the app launched fine but our own observation window closed before recognizing it. This is a real, open tuning question (per-entity timeout, or genuine pacing/lazy restoration per Phase 6) rather than a correctness bug in the matching/verification logic itself, which continued to behave correctly (zero incorrect placements) throughout every test tonight, including this one.

**Follow-up `restore-all` run (9 entities, including a first-ever VS Code entity): 8/9 restored correctly, 1 ONLYOFFICE timeout again.** VS Code (`app_id="code"`) restored cleanly on the first attempt, consistent with the earlier finding that it needs no special handling. The single failure was again ONLYOFFICE, again a 10-second timeout, again in a batch context -- consistent with, though not yet definitively confirming, the load/timing theory above rather than a new or different defect. Matching and placement correctness held for all 8 successful entities, including alongside a growing pile of unrelated windows on shared workspaces (informational conflict reporting continued to work correctly throughout).



Your instincts throughout — persistent vs. runtime identity, sequential launch-and-match, ambiguity as a first-class outcome rather than a forced guess, lazy per-workspace restore, "partial-but-correct beats complete-but-wrong" — are all structurally sound and match how I'd design this myself. The main things this document adds are: making Matching and Reconciliation separate first-class components rather than folding them together, choosing Go with concrete reasoning tied to your actual workload shape rather than general reputation, and re-sequencing the roadmap so idempotency/safety lands before layout fidelity, consistent with your own priority ordering.
