# Rust #44862: preserve parent cache affinity for ephemeral forks

Upstream commit: `bc5957eac9e89e66f990ed490d11e625a4a3b02c`

## Result

Implemented.

- `RuntimeRouter.responsesPromptCacheKey` uses the parent session as the cache
  key for an ephemeral fork that carries parent lineage.
- Non-ephemeral forks, root threads, and subagent threads retain their own
  cache identity.

## Verification

```text
go test ./appserver -run 'PromptCacheKey|ExperimentalContext|TokenBudget|HistoryNotes' -count=1
```

Passed on 2026-10-18.
