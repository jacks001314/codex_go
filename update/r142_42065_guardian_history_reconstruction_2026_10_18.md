# Rust #42065: preserve Guardian history across thread reconstruction

Upstream commit: `28097e98ebcb5e7eaa2e14534e60337f209a8a80`

## Result

Partially implemented.

- Go reconstructs normal thread history from rollout items, including the
  items Guardian reviews consume from the current record.
- Go has no `GuardianHistoryCheckpoint` or `guardian_history` rollout item
  (`rg` finds no carrier). The reviewer conversation and its truncation
  checkpoint remain in memory.
- After a thread reconstruction, Guardian-specific history checkpoints cannot
  be restored or truncated with the thread.

## Verification

```text
go test ./rollout -run 'Reconstruct|Rollout' -count=1
```

Passed on 2026-10-18.
