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
- The client retries V2 discovery once with the original request parameters
  after a recoverable executor disconnect.
- A per-server `fsnotify` watcher caches successful inventories while all
  required roots are watched. File events and watcher errors mark the cache
  dirty; failed watch registration makes each request reload discovery rather
  than serving a stale snapshot.

## Remaining for full parity

- Startup prewarm and on-demand retry after prewarm failure.
- Recovery replay of a prewarmed location snapshot.

The symbol ledger is therefore `partial`, not `complete`.

## Verification

```text
go test ./execserver -run 'DiscoverCapabilitiesV2|CapabilityDiscoveryV2|CapabilityRoots' -count=1  # ok
go test ./execserver -count=1
# only the two known Windows path baseline failures

go vet ./execserver  # ok
go build ./...       # ok
```
