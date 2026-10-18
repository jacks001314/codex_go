# Round 762b: capability batching, agent roles, control tools, rollout lock, realtime timeline

## Complete

- `#40443`: batched capability-root discovery with ordered chunks and empty
  request handling.
- `#40487`: dedicated agent role loading/validation/nickname normalization and
  malformed-role warnings.
- `#40496`: history/notes and async-message control tools with root-only async
  exposure.
- `#40499`: rollout migration maintenance lock, journal/canonicalization, and
  startup scheduling.
- `#40508`: thread/timeline/list plus typed realtime item start/completed/
  transcript-delta notifications.

## Not applicable

- `#40438`: Rust Bazel/rust-lld linker setting; Go has no Bazel toolchain.

## Verification

- `go test ./execserver -run 'CapabilityRoots|CapabilityDiscovery' -count=1`
- `go test ./appserver -run 'HistoryNotes|Async|ControlTool|Timeline|RealtimeItem' -count=1`
- `go test ./rollout -run 'Migrate|MaintenanceLock|Canonicalize' -count=1`
- `go test ./parity -count=1`
