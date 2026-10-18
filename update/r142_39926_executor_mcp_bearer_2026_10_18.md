# Rust #39926: executor HTTP MCP bearer tokens

Upstream commit: `93c54bca38996b56d344a2ca65f01627b1953b27`

## Implemented

- Executor-owned streamable HTTP MCP declarations retain
  `bearer_token_env_var`; the host no longer rejects them during discovery.
- The per-server executor HTTP doer removes any static or placeholder
  `Authorization` header and delegates `Authorization: Bearer ` plus
  `valueEnvVar` through the exec-server HTTP protocol.
- Exec-server resolves `valueEnvVar` in its own process for every request,
  including redirected requests. Missing, empty, and protected credential
  variables are rejected before an authorized request is sent.
- Added the `valueEnvVar` field to the exec-server `HTTPHeader` wire shape.
- Delegated requests use redirect policy `stop`, leaving redirect handling to
  the MCP HTTP client so its existing policy remains authoritative.
- Anonymous executor-only MCP servers continue to work; only bearer environment
  resolution is newly permitted.

## Verification

```text
go test ./mcp -count=1
go test ./execserver -run HTTPRequest -count=1
go test ./appserver -run Executor -count=1
go vet ./execserver ./mcp ./appserver
```

The focused suites passed on 2026-10-18. A full `go test ./execserver` run also
reached one unrelated pre-existing Windows PowerShell fallback assertion and
otherwise completed without failures in the touched HTTP paths.
