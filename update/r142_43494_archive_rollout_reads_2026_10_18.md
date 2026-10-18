# Rust #43494: limit archive rollout reads to requested threads

Upstream commit: `d0a8dcd15721adb02a3227d08d3cde888e5546e5`

## Result

Implemented.

- Archived rollout scans restrict candidates to the requested thread IDs using
  canonical filename ownership and session metadata, so unrelated archived
  rollouts are not parsed.
- `ArchiveThreadRollouts` moves every active rollout owned by the requested
  threads and leaves other archived rollouts untouched.

## Verification

```text
go test ./rollout -run 'ArchivedThread|ArchiveThreadRollouts' -count=1
```

Passed on 2026-10-18.
