# Rust #53800: wire background compaction into the turn lifecycle

Upstream commit: `9e03960cf8807c37fc987bf17b4e745b8b3339d2`

## Result

Equivalent implementation was already present.

- Eligible completed turns launch background compaction from the captured
  checkpoint/prefix.
- Cancelled turns do not launch background work.
- Async state deduplicates attempts and retains the latest candidate.
- Captured attempts are validated before becoming ready.
- Concurrent waiters share one pending result.
- Auth-owner changes and shutdown cancel/drain workers correctly.

## Verification

```text
go test ./appserver -run BackgroundCompaction -count=1
```

Passed on 2026-10-18.
