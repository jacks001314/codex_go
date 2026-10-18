# Rust #40665: turn trigger metadata

Upstream commit: `538900ee769f8e353312c5052bf771e1e5f40446`

## Result

Equivalent implementation was already present.

- `TurnStartParams.TurnTrigger` is accepted by `turn/start` and is ignored when
  steering an active turn.
- The value reaches Responses request metadata as `turn_trigger` and is carried
  into turn attribution/rollout persistence.
- Queue dispatch, queue-start, goal continuation, delegated agents, Guardian,
  memory consolidation, realtime, and recovery paths populate their expected
  trigger classifications.
- Turn completion and resumed attribution preserve the same value.

## Verification

```text
go test ./appserver -run 'TurnTrigger|Attribution' -count=1
```

Passed on 2026-10-18.
