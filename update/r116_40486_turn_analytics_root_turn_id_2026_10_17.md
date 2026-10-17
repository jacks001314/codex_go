# Round 116 · #40486 root turn IDs in turn and tool analytics

Rust source: `49880081fe`.

## Status

The tool-item half already existed (`telemetry.CodexToolItemEventBase.RootTurnID`
and its serialization test). This round completed the turn-event half and
verified the shared root resolution.

- `telemetry.CodexTurnEventParams` now carries `root_turn_id` (serialized
  explicitly, `null` when unknown), and `CodexTurnEventInput` accepts it.
- `RuntimeRouter.emitCodexTurnAnalyticsEvent` resolves the root with
  `rootTurnIDForTurn(params, record.ID)` when each event is built: a top-level
  turn is its own root, and a subagent/continuation turn carries the captured
  `RootTurnID` supplied by its creator. Go has no separate "live root
  invalidation" signal on steering, so it always reports the effective root
  instead of Rust's null-after-ambiguity branch.
- Tool-item and command-execution events keep using the existing
  `RootTurnID` field, so control-tool and code-mode completions retain the same
  origin.

## Verification

```text
go test ./telemetry -count=1  # ok
go test ./appserver -run 'TestRuntimeRouterTurnStartEmitsCodexTurnAnalyticsLikeRust' -count=1  # ok
go test ./appserver -run 'TurnAnalytics|TurnStartEmits|TurnStartFailedEmits|TurnInterruptedEmits|ToolEvent|RootTurn' -count=1  # ok
go vet ./telemetry ./appserver  # ok
go build ./...  # ok
```

The serialization fixture asserts `root_turn_id`, and the turn-start analytics
test now asserts the top-level turn reports itself as the root.
