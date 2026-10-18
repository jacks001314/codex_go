# Rust #53723: wire V2 capability discovery through exec-server RPC

Upstream commit: `703d253ed676c26e4b9708a374b7344b5c1bcc53`

## Result

Equivalent implementation was already present.

- Exec-server advertises `capabilityDiscoveryV2` and serves
  `capabilities/discoverV2`.
- The client retries transient discovery failures.
- The executor capability cache coalesces in-flight requests by environment
  instance, session, and caller-bound request key.
- Permanent results are cached, transient failures retry, recovered discovery
  signaling is retained, and cache entries are FIFO-bounded.
- App-server consumes V2 snapshots for executor roots, hooks, and model-step
  capability catalogs.

## Verification

```text
go test ./execserver -run CapabilityDiscoveryV2 -count=1
go test ./appserver -run CapabilityDiscoveryV2 -count=1
```

Passed on 2026-10-18.
