# Codex Go appserver repair batch - 2026-08-12

## Background

The `update/plan_2026_08_12_next.md` deferred register recommended a dedicated
repair batch for the pre-existing appserver test cluster that every prior batch
had to skip:
`TestRouterResumeFromPath`, `TestRouterForkFromPath`,
`TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread`, and the hang in
`TestRouterThreadSectionsListUpdateFilterAndClearLikeRust`.

## Root causes and fixes (Go commit `f01d404`)

### 1. Rollout replay phantom items/turns (`rollout/session_items.go`)

- Go-written legacy rollouts emit the `event_msg` user/agent mirror before its
  canonical response item (`AppendSessionItemEvent` plus `LineFromItem`) with no
  `turn_started` event. The replay builder placed the mirror under a synthetic
  `rollout-N` turn while the canonical item carried the real turn; the mirror
  dedupe (`hasCanonicalEventMirror` / `generatedEventMirrorIndex`) required
  equal turn IDs, so the mirror and its synthetic turn survived as a phantom
  item plus phantom turn. This broke `TestRouterResumeFromPath`,
  `TestRouterForkFromPath`, and
  `TestRouterInjectItemsAndRollbackRepairRolloutOnlyThread`.
- Fix: relax the turn match for synthetic `rollout-N` mirrors (canonical
  response items win by role and text regardless of the canonical's real turn)
  and, when a canonical replaces a synthetic-turn mirror, adopt the canonical's
  real turn for the current turn snapshot. Real Rust rollouts (which emit
  `turn_started`) keep strict turn matching via the new `isSyntheticRolloutTurn`
  guard.

### 2. Session store RWMutex reentrancy deadlock (`session/store.go`)

- `MoveThreadToSection` and `updateMetadataLocked` call `threadSectionForID`
  while already holding the store write lock; `threadSectionForID` re-acquired
  an `RLock`. `sync.RWMutex` is not reentrant, so any non-pinned/unknown
  section move or section-carrying metadata update deadlocked. This hung
  `TestRouterThreadSectionsListUpdateFilterAndClearLikeRust`.
- Fix: split into a public `threadSectionForID` (RLock wrapper) and a
  `threadSectionForIDLocked` variant used by the two locked callers.

## Verification

- `go build ./...` clean; `go vet ./rollout ./session ./appserver` clean;
  `gofmt -l` empty; `git diff --check` clean.
- `go test ./rollout ./session ./prompt ./turn -count=1` passes.
- `go test ./appserver -count=1` passes with no skips for the first time across
  the recent batches (previously required the four-test skip list above).

## Deliverable

- Commit `f01d404` "Fix appserver thread fork/resume/rollback and thread-section
  deadlock cluster", pushed to `origin/main`.
- Future appserver runs no longer need the skip list; the deferred register's
  repair recommendation is closed.
- This plan document (`update/plan_2026_08_12_repair.md`).
