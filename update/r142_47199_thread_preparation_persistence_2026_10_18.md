# Rust #47199: thread preparation and execution persistence

Upstream commit: `b781733f958ed55282c5ed343e3a6fef7139498c`

## Result

Equivalent architecture.

- Rust distinguishes a preparation checkpoint from speculative persistence
  activation before the first turn.
- Go's `thread/inject_items` writes preparation items synchronously to the
  thread record and rollout, and repairs rollout-only threads when necessary.
- There is therefore no speculative checkpoint that could delay the first model
  request, while prepared context is already durable before the first input and
  survives reload/restart.

## Verification

```text
go test ./appserver -run 'RouterSearchLoadedTurnsAndInjectItems|RouterInjectItemsMarksClientDeveloperMessages|RouterInjectItemsRepairRolloutOnlyThread' -count=1
```

Passed on 2026-10-18.
