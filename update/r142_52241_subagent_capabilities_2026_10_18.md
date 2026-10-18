# Rust #52241: preserve subagent capabilities independently of forked history

Upstream commit: `845345b1dd4f8e2a5d5ce91363fff666e41b07fc`

## Result

Equivalent implementation was already present.

- Runtime subagent spawns inherit selected environment capability roots through
  child thread metadata rather than deriving them from forked conversation
  history.
- Parent disabled-plugin selections are inherited independently of
  `fork_turns`.
- Explicit child selections override inherited values; an explicit empty
  disabled-plugin list remains present and clears the parent choice.

## Verification

```text
go test ./appserver -run SpawnInherits -count=1
```

Passed on 2026-10-18.
