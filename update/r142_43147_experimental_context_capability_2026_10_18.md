# Rust #43147: gate experimental context by model capability

Upstream commit: `6af345407d9c2a568da9d01b6c4b81a9e61495c0`

## Result

Implemented.

- `ModelInfo.SupportsExperimentalContext` is parsed from the model catalog and
  defaults to false when omitted.
- `experimentalContextManagementEligible` and the token-budget history-notes
  surface require the capability before activating token-budget context,
  history notes, or `new_context`.

## Verification

```text
go test ./appserver -run 'PromptCacheKey|ExperimentalContext|TokenBudget|HistoryNotes' -count=1
```

Passed on 2026-10-18.
