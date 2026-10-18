# Executor MCP discovery telemetry（#41205）

`discoverExecutorHTTPMCPServers` 现在发出：

- `codex.mcp.executor_discovery`，tag `outcome=success|error`；
- `codex.mcp.executor_discovery.server`，tag `server_name` 与
  `outcome=found|unavailable`。

server 计数发生在 policy/startup 投影之前。新增
`TestExecutorMCPDiscoveryEmitsOutcomeMetricsLikeRust`。

## 验证

- `go test ./appserver -run 'ExecutorMCPDiscoveryEmitsOutcomeMetrics' -count=1`
