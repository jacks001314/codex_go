# Round 762: Vim dot-repeat, provider auth retry, context hints, turn errors

## Complete

- `#40521`: Vim normal-mode dot-repeat for find/operator/insert, replace, and
  delete-word edits.
- `#40523`: provider auth refresh retries, Bedrock auth-command/credential-export
  refresh, and external auth refresh paths.
- `#40533` / `#40539`: context-window guidance fragment and token-budget history
  notes hint ingestion/analytics.
- `#40554`: transcript_v2 feature registration and daemon forwarding.
- `#40616`: turn-scoped service tier override with model-support constraints.
- `#40613`: terminal turn errors preserve composer/queued input.

## Blocked

- `#40625`: closed-agent navigation/replay-only channel lifecycle is absent in
  Go, so stale closed-agent turns cannot be excluded from `/cd` checks.

## Verification

- `go test ./tui/tea -run 'Vim|TurnFailed|PendingInput' -count=1`
- `go test ./appserver -run 'ContextWindowGuidance|HistoryNotes|ServiceTierForTurn' -count=1`
- `go test ./parity -count=1`
