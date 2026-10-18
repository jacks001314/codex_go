# MCP OAuth credential storage telemetry

Rust upstream: `05ea5f757e Add attributed MCP OAuth credential storage telemetry (#49392)`.

## Implementation

- `mcp.OAuthStore` emits bounded telemetry for `load`, `save`, and `delete`.
- Events carry `credential_kind=mcp`, configured/selected/actual store,
  secure and overall outcomes, file-fallback reason, secure error
  classification, storage phase, originator, and duration.
- Auto mode records keyring errors or missing entries before file fallback.
- App-server wires the observer to `codex.auth_storage.operation` and
  `codex.auth_storage.duration`; fallback warnings use the same bounded fields.
- Credential values, server names/URLs, paths, and raw error text never enter
  tags or warnings.

## Verification

```text
go test ./mcp -run 'OAuthStorageTelemetry|OAuthStore' -count=1
go test ./mcp -count=1
go test ./appserver -run 'OAuthStorageTelemetry|OAuth|MCP' -count=1
go vet ./mcp ./appserver
go build ./...
```
