# #61215 Dictation provenance in turn analytics

## Behavior

- Added internal `UsedDictation *bool` to turn start/steer params (`turn/api.go`). It is not part of the app-server wire schema.
- Added `appserver/turn_dictation.go` to extract `codex_app_used_dictation` from both Responses metadata forms. Patch values take precedence over replacement values; the marker is removed from both maps even when malformed. Only exact `true` and `false` strings are recognized.
- Provenance is captured only for accepted user input (start with prompt/input and no tool output; steer with prompt/input).
- Active turns aggregate accepted inputs: any `true` wins, an unknown value keeps the result unknown unless a `true` was seen, and `false` is reported only when every accepted input is known non-dictation.
- Completion, error, and review paths pass the aggregate into `CodexTurnEventParams.UsedDictation` (`used_dictation`, omitted when unknown).

## Regression

- `appserver/turn_dictation_like_rust_test.go` covers metadata precedence, removal, malformed values, non-user-input exclusion, steer aggregation, and analytics state transitions.
- `telemetry/turn_event_dictation_like_rust_test.go` covers JSON presence/absence for true, false, and unknown.
- Existing analytics fixtures were updated to the new internal parameter.

## Evidence

- `go test ./appserver ./telemetry -run 'TestTurnDictation|TestCodexTurnEventDictation' -count=1` passed.
- Full `./appserver` package passed in 261.362s during this round.
- Log: `update/dictation_analytics_2026_11_14.log`.
