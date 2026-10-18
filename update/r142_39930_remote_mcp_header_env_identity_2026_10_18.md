# Rust #39930: remote MCP header environment identity

Upstream commit: `9c3da20b3f6dadf60646d6fb16f5e357c234760d`

## Implemented

- MCP connection identity now appends resolved environment values, so live
  connection reuse follows the credentials and headers it actually uses.
- Remote streamable-HTTP `env_http_headers` values remain part of the identity
  even when the MCP server runs in an executor environment.
- A remote `bearer_token_env_var` participates only when the host may resolve
  it. Executor-only declarations never read a host bearer value.
- Local environments continue to track bearer values, and local stdio
  `env_vars` track values from the local source.

## Verification

```text
go test ./mcp -run CredentialPolicy -count=1
go test ./mcp -run MCPToolCatalogGraceKey -count=1
```

Both suites passed on 2026-10-18.
