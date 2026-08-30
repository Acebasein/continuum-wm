# MVP 1 — Persistence Validation

## Status

**PASS**

MVP 1 successfully demonstrated that Continuum-WM can load its persisted
snapshot, deserialize it into the Continuum-owned data model, validate the
snapshot schema, and safely reject an unsupported schema version.

---

## 1. Objective

The objective of MVP 1 was to validate the persistence boundary established
during MVP 0.

MVP 0 proved:

    Niri
      |
      v
    Continuum normalized model
      |
      v
    snapshot.json

MVP 1 proves the reverse path:

    snapshot.json
          |
          v
        load
          |
          v
      deserialize
          |
          v
    schema validation
          |
          v
    Continuum normalized model

The central question was:

> Can Continuum-WM safely reload and validate its own persisted state
> independently of the live compositor?

---

## 2. Implementation

The Continuum snapshot model now supports both serialization and
deserialization.

The persistence layer provides two primary operations:

### Save

A normalized Continuum snapshot is serialized as JSON and written to:

    $XDG_STATE_HOME/continuum-wm/snapshot.json

or, when `XDG_STATE_HOME` is not defined:

    ~/.local/state/continuum-wm/snapshot.json

### Load

The persisted JSON is:

1. read from disk
2. deserialized into the Continuum snapshot model
3. checked against the supported snapshot schema version
4. returned only when validation succeeds

This means persisted state is not automatically trusted simply because it can
be parsed.

---

## 3. Schema Versioning

The current snapshot schema version is:

    1

Continuum explicitly validates the persisted `schema_version` before accepting
the snapshot.

At this stage, Continuum supports exactly schema version 1.

Snapshots using unsupported versions are rejected rather than interpreted
using assumptions about compatibility.

This establishes the foundation for controlled snapshot-format evolution in
future versions.

---

## 4. Command Separation

During MVP 1 validation, capture and load behavior were separated into explicit
commands:

    cargo run -- capture

and:

    cargo run -- load

The capture command:

- queries Niri
- creates a normalized snapshot
- writes the snapshot to persistent state

The load command:

- does not query Niri
- does not rewrite the snapshot
- reads the existing persisted state
- deserializes it
- validates its schema version

This separation was necessary to test persisted state independently of live
capture.

It also creates a useful operational boundary for later Continuum commands.

---

## 5. Successful Load Validation

A valid schema version 1 snapshot was captured and persisted.

Running:

    cargo run -- load

successfully loaded the snapshot and reported the expected workspace and
window count.

The observed result was equivalent to:

    Continuum-WM MVP 1 — Load
    Loaded snapshot schema 1: workspace 1 with 2 window(s)

This demonstrated a successful disk-to-model round trip.

---

## 6. Unsupported Schema Validation

To test the failure path, the persisted snapshot was deliberately modified from:

    "schema_version": 1

to:

    "schema_version": 2

The snapshot was then loaded without performing another capture.

Continuum rejected the file with:

    Unsupported snapshot schema version: 2 (supported: 1)

This is the expected behavior.

The unsupported snapshot was not silently accepted or interpreted as schema
version 1.

---

## 7. Recovery Validation

After the unsupported-schema test, a new valid snapshot was created using:

    cargo run -- capture

The snapshot was then loaded again using:

    cargo run -- load

Loading succeeded.

This confirmed that the failure test did not affect Continuum's ability to
return to a valid persisted state.

---

## 8. Automated Validation

MVP 1 introduced automated tests for snapshot schema validation.

The test suite verifies:

- supported schema version is accepted
- unsupported schema version is rejected

The validation run completed with:

    2 passed
    0 failed

The implementation also passed:

    cargo fmt --check
    cargo clippy -- -D warnings
    cargo test

No compiler or Clippy warnings were reported.

---

## 9. Important Findings

### 9.1 Persisted state must be validated

Successful JSON deserialization alone is not sufficient evidence that a
snapshot is compatible with the running Continuum version.

Schema validation is therefore part of the persistence boundary.

---

### 9.2 Capture and load are separate operations

A combined capture-and-load command initially prevented a real unsupported
schema test because capture rewrote the manually modified snapshot before it
could be loaded.

Separating capture from load removed that ambiguity.

This also improves the architecture by allowing persisted state to be examined
without modifying it.

---

### 9.3 Unsupported state fails safely

Continuum does not attempt to guess how an unsupported snapshot should be
interpreted.

This follows the broader Continuum principle:

> Prefer incomplete restoration over incorrect restoration. Do not guess.

Although MVP 1 does not yet restore anything, the same safety principle applies
to persisted state.

---

### 9.4 Persistence is independent of compositor access

The load path operates entirely on the persisted Continuum model.

Niri is not queried during `load`.

This confirms that compositor-specific observation and Continuum persistence
remain separate architectural concerns.

---

## 10. MVP 1 Result

**MVP 1 — PASS**

Continuum-WM has demonstrated the ability to:

- serialize its normalized state
- persist that state locally
- reload persisted state
- deserialize it into the Continuum model
- validate snapshot schema compatibility
- reject unsupported schema versions
- recover cleanly by capturing a new valid snapshot
- test schema validation automatically

The persistence boundary required by the MVP is therefore validated.

---

## 11. Next Stage

**MVP 2 — Live-State Reconciliation**

MVP 2 will introduce the first comparison between persisted Continuum state
and the current compositor state.

Conceptually:

    Saved Snapshot          Live Niri State
          |                       |
          +-----------+-----------+
                      |
                      v
                 Reconciliation
                      |
                      v
              Restoration Plan

The objective will be to determine what already exists, what is missing, and
what Continuum should leave untouched before any application launching or
window manipulation occurs.
