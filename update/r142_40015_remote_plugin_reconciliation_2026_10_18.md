# Rust #40015: harden remote installed plugin cache reconciliation

Upstream commit: `e6a3877e95788b52c3aa5e9a143dba87f04720dc`

## Implemented

- Each installed-plugin reconciliation captures the current auth identity and
  cache generation. If the account owner changes while the HTTP snapshot or
  bundle materialization is in flight, the pass does not publish stale plugin
  metadata.
- Auth-owner changes advance the remote plugin generation and clear remote
  installed metadata for every remote marketplace while preserving the normal
  plugin/skill/MCP cache refresh.
- A per-Codex-home gate serializes installed-plugin bundle reconciliation with
  direct plugin installs and uninstalls, including the install-request tool
  path.
- Every row in the authoritative installed-plugin response is validated before
  any bundle download or stale-cache removal. Missing identities or invalid
  local cache names fail the entire pass.
- A failed bundle materialization records the failure but retains the valid
  installed metadata and remote identity, so it is not mistaken for an
  uninstall.
- Cancellation or another failed pass releases the mutation gate; a later
  retry is not permanently suppressed.

## Verification

```text
go test ./appserver -run RemoteInstalledPlugin -count=1
go test ./appserver -run Plugin -count=1
go test ./plugin -count=1
go vet ./appserver ./plugin
```

Passed on 2026-10-18.
