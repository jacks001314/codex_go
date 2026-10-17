# Round 116 · #46010 app result metadata with analytics controls

Rust source: `3a589370a4`.

## Status

The observable half landed in an earlier round; this round verified it against
the current worktree and closed the ledger row.

- `turn.mcpResultMetadataCaptureAllowed` implements Rust's
  `result_metadata_capture_allowed`: capture requires an enabled analytics
  state and the host-owned `codex_apps` server. A nil analytics state is a
  disabled client, and the dispatch recorder keeps its separate check
  (`turn/tool_dispatcher.go`, `turn/executed_tool_calls.go`).
- `analyticsEnabledOptionForThread` supplies the thread's effective analytics
  state, so `analytics.enabled = false` and host-disabled analytics both gate
  capture.
- Raw result metadata stays in the internal executed-tool-call record; the raw
  response item notifications omit it (existing dispatcher coverage).

## Websocket reuse comparison

Rust's `ResponseItem::has_same_tool_result_metadata` exists to decide whether an
incremental WebSocket request can reuse a previously sent output. Go's
Responses WebSocket path always writes the full `response.create` payload
(`model/responses_agent.go`, `websocketResponseCreatePayload`) and only reuses
the connection, not a previously sent item list. Late result metadata is
therefore always included in the next request, which is the behavior that
comparison protects. No Go carrier or dead helper was added.

## Verification

```text
go test ./turn -run 'TestMCPResultMetadataCaptureAllowedLikeRust|TestToolDispatcherRecordsResultMetadataLikeRust' -count=1  # ok
```
