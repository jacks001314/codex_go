# Rust #52337: durable thread read state with revision-checked updates

Upstream commit: `b6e8a2e207e5591780061c62c50df417670f8d75`

## Result

Equivalent implementation was already present.

- SQLite stores each durable thread's first-unread turn and monotonic revision.
- Read/unread updates run transactionally against an expected revision.
- Idempotent operations preserve the revision; stale writes return a conflict
  containing the current state.
- Missing/deleted threads return absent or unavailable as appropriate.
- Read state survives metadata rebuild and runtime restart, and follows thread
  deletion.

## Verification

```text
go test ./state -run ThreadReadState -count=1
```

Passed on 2026-10-18.
