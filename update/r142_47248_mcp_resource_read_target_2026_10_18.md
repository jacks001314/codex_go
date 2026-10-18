# Rust #47248: explicit app/account targets for MCP resource reads

Upstream commit: `3b50349e1a80930e6e9dcf6d9377d65ef55ef052`

## Result

Equivalent implementation was already present.

- `MCPResourceReadTarget` carries `connectorId` and a required-on-wire,
  nullable `linkId`; an explicit null requests no-auth access.
- Direct targets are accepted only for `codex_apps`, with a non-empty connector
  and either a real non-synthetic link or null.
- `originCallId` scope takes precedence and suppresses direct target metadata.
- Request metadata includes connector/link identity, and resource cache keys
  incorporate the target so the same URI under different accounts cannot share
  cached contents.

## Verification

```text
go test ./mcp -run 'MCPResourceReadTarget|ResourceReadTarget|Resource.*Target' -count=1
```

Passed on 2026-10-18.
