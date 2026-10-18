# MXC managed-networking local binding default

Rust upstream: `74af2496c9 Default local binding to true for MXC managed networking (#46523)`.

## Implementation

- `ProxySettings` now tracks whether `allow_local_binding` was explicitly set,
  instead of collapsing omission to false during parsing.
- Managed-network spec resolution applies a per-executor `LocalBindingPolicy`:
  - Windows MXC: omitted becomes true; explicit false is rejected while
    networking is enabled.
  - Non-MXC and non-Windows executors: omitted becomes false.
- Turn setup derives the platform from the selected environment's `PlatformOS`,
  so remote Windows MXC uses the same policy as a local Windows executor.
- Owner/requirement restrictions are applied before the executor default.

## Verification

```text
go test ./network -run 'LocalBinding|ExecutorOS|Socket' -count=1
go test ./appserver -run 'ManagedNetworkLocalBinding|ManagedNetwork' -count=1
go build ./...
go vet ./network ./appserver
```
