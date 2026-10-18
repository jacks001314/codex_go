# Bounded CLI auth credential-storage telemetry

Rust upstream: `2347a61d0d Add bounded credential-storage telemetry helpers (#47901)`.

## Implementation

- CLI auth `load`, `save`, and `delete` now emit bounded storage observations
  with `credential_kind=codex`, configured/selected/actual store, operation,
  secure and overall outcomes, duration, phase, secure error class, fallback
  reason, and `originator=cli`.
- Keyring unavailability now has a typed `auth.ErrKeyringUnavailable` sentinel
  while preserving the existing user-facing message.
- App-server wires CLI auth and MCP OAuth observers to the same
  `codex.auth_storage.operation` / `codex.auth_storage.duration` metrics.
- No credential payloads, file paths, keyring service/account names, or raw
  error text enter telemetry.

## Verification

```text
go test ./auth -run StorageTelemetry -count=1
go test ./auth -count=1
go test ./appserver -run OAuthStorageTelemetry -count=1
go vet ./auth ./appserver ./mcp
go build ./...
```
