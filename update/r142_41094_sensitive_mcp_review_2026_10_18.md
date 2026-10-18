# Rust #41094: synchronous review for sensitive MCP actions

Upstream commit: `89650c66f2f3ff0d028d3f5d6d0b187b2ed49be5`

## Result

Not applicable by architecture.

- `codex_sensitive_action: true` exists only to escalate a Guardian V2
  extension fast-approval decision to the synchronous reviewer.
- Go does not have an extension fast-approval path. Every Guardian MCP
  elicitation is handled by `appserver.mcp_elicitation`, which calls
  `GuardianReviewer.Review` synchronously.
- Absent, false, and true metadata therefore all use the existing synchronous
  route.

## Verification

```text
go test ./appserver -run 'GuardianMCP|MCPElicitation' -count=1
```

Passed on 2026-10-18.
