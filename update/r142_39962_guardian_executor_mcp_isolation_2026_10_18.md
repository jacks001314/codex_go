# Rust #39962: isolate Guardian reviews from executor MCP servers

Upstream commit: `e482cc66aeeedcb9f333a1f5a0a554eb5aea4b36`

## Implemented

- Guardian reviewer turns skip executor MCP candidate detection.
- Executor MCP discovery is not attempted for Guardian turns.
- Executor-discovered servers are not projected into the Guardian runtime.
- The Guardian runtime therefore cannot inherit an unavailable required
  executor server or block an approval review on executor MCP startup.

## Verification

`TestGuardianTurnSkipsExecutorMCPDiscoveryLikeRust` registers a selected
executor whose configuration contains an unreachable required MCP server. It
verifies the executor config reader is never called, no server is projected,
and required-server validation on the resulting Guardian runtime succeeds.

```text
go test ./appserver -run GuardianTurnSkipsExecutorMCPDiscovery -count=1
go vet ./appserver
```

Passed on 2026-10-18.
