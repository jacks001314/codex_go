# Opt-in OTLP logging for final agent responses

Rust upstream: `e6da6fd7c7 Add opt-in OTLP logging for final agent responses (#47649)`.

## Implementation

- `otel.log_agent_responses` is accepted by the strict config loader, defaults
  to false, and only enables logging when `otel.exporter` is OTLP HTTP or gRPC.
- The turn loop reports each completed response item. The app-server logger
  exports only assistant `final_answer` messages from main agents and
  `subAgentThreadSpawn` children; review, compact, memory, internal, and other
  subagent sources are excluded.
- `codex.agent_response` carries the agent type, turn/item ids, parent
  conversation/turn and root turn ids, and initiating agent path when present.
- Exported text strips memory citations, is bounded at 64 KiB on a UTF-8
  boundary, and includes `response_length` plus `response_truncated`. Duplicate
  item ids within a turn are suppressed, and stored history is untouched.

## Verification

```text
go test ./config ./telemetry ./eventmap ./turn ./appserver -run 'AgentResponse|Citation|CompletedResponseItem|ExecutedToolCall' -count=1
go vet ./appserver ./telemetry ./config ./turn ./eventmap
go build ./...
```
