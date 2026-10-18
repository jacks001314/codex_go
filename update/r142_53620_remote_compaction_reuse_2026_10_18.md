# Rust #53620: reuse background results for remote V2 compaction

Upstream commit: `9167a0b2d4a74d44a4d388057d304682e47313c6`

## Result

Equivalent implementation was already present.

- Remote compaction checks the thread's async compaction state with
  `TakeReady`.
- If no result is ready, it waits for the shared pending result.
- A candidate is installed only when its captured history prefix, model
  compaction hash, and auth-owner revision still match.
- Otherwise the normal remote compaction path runs without replaying invalid
  background state.

## Verification

```text
go test ./appserver -run 'AsyncCompactionTakeReady|AsyncCompactionWaitForPending|BackgroundCompactionLaunchesFromTurnCheckpoint' -count=1
```

Passed on 2026-10-18.
