# Rust #39941 audit: discover HTTP MCP servers from selected executors

Upstream commit: `00a7b888b23715989db19b74f6cb623ca46be620`

The Go port already contains the requested behavior in
`appserver/mcp_executor_discovery.go` and the thread runtime wiring:

- Each selected remote executor reads its local `mcp_servers` configuration and
  requirements.
- Stdio servers and HTTP configurations using executor environment headers or a
  header helper are excluded.
- Eligible HTTP servers are bound to the executor environment and supplied with
  the executor route-bound HTTP client.
- Host requirements disable nonmatching servers, and executor-local servers are
  best effort rather than required at startup.
- Existing ownership rules prevent host servers or another environment from
  being overwritten.

```text
go test ./appserver -run ExecutorMCP -count=1
go test ./appserver -run Executor -count=1
```

Passed on 2026-10-18.
