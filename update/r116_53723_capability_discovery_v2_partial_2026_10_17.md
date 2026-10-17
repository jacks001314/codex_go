# Round 116 · #53723 V2 capability discovery (partial)

Rust source: `703d253ed6`.

## Landed

- Advertised `capabilityDiscoveryV2` in `environment/info`.
- Added `capabilities/discoverV2` wire request/response types and client API.
- Added executor-owned discovery for:
  - `$CODEX_HOME/skills` standalone skills
  - `$HOME/.agents/skills`
  - installed plugin cache packages under `$CODEX_HOME/plugins/cache`
- Responses carry plugin identity/version/root/files and standalone or plugin
  skill metadata. Skill instruction bodies are intentionally omitted from the
  returned metadata.
- Request sandbox context is forwarded through the existing V1 discovery
  filesystem operations.

## Remaining for full parity

- Startup/prewarmed location cache and on-demand retry after prewarm failure.
- Replay once after executor recovery while preserving request parameters.
- Filesystem watch registration success tracking and request-time reload when
  registration fails.

The symbol ledger is therefore `partial`, not `complete`.

## Verification

```text
go test ./execserver -run 'DiscoverCapabilitiesV2|CapabilityDiscoveryV2|CapabilityRoots' -count=1  # ok
go test ./execserver -count=1
# only the two known Windows path baseline failures

go vet ./execserver  # ok
go build ./...       # ok
```
