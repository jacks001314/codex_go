# Executor MCP discovery telemetry

Rust upstream: `e931d07b88 Track executor MCP discovery telemetry (#41205)`.

The r109 symbol pass marked `#41205` as `not_applicable` because the change
adds no public Rust symbols; the runtime behavior was nevertheless missing.

## Implementation

- `discoverExecutorHTTPMCPServers` emits `codex.mcp.executor_discovery` with
  `outcome=success|error` for each completed remote discovery attempt,
  including cache refreshes and connect/capability/read/parse failures.
- Each discovered server emits `codex.mcp.executor_discovery.server` with
  `server_name` and `outcome=found|unavailable` before host ownership and
  policy filtering, matching Rust's measurement point.
- Metrics carry only bounded outcome and server-name labels; raw errors and
  credentials are not attached.

## Verification

```text
go test ./appserver -run 'TestExecutorMCPDiscoveryEmitsOutcomeMetricsLikeRust|ExecutorMCP' -count=1
go test ./appserver -count=1
go vet ./appserver
go build ./...
```
