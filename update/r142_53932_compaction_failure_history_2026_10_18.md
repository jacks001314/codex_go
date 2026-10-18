# Rust #53932: preserve conversation history when compaction fails

Upstream commit: `400f2356d60c4663d46817a40b4d09c5c86f35ad`

## Result

Equivalent implementation was already present.

- Pre-turn and during-turn compaction collect attempt output locally.
- History replacement and compaction-output recording occur only after the
  attempt completes successfully.
- Failed attempts record the compaction error without committing partial
  compaction items.
- The accepted user prompt remains exactly once in persisted conversation
  history.

## Verification

```text
go test ./appserver -run PreTurnCompactFailurePreservesPromptLikeRust -count=1 -timeout=60s
```

Passed on 2026-10-18.
