# Rust #39937: bounded unified-exec output delta frames

Upstream commit: `748d8ac834157bb1f29aa0966efe86b8ad824d41`

## Implemented

- Each `UnifiedExecEventOutputDelta` frame remains capped at 8 KiB.
- Delta construction stops at the existing 10,000-frame per-call quota.
- A split multibyte UTF-8 scalar is retained until the next producer chunk
  completes it.
- Malformed bytes and incomplete trailing bytes are passed through without
  being lost. The final incomplete suffix is flushed before the end event.
- The transcript receives every producer byte independently of delta event
  emission, so frame/quota limits never truncate the end event's full output.

## Verification

```text
go test ./tool -run UnifiedExecUTF8Boundary -count=1
go test ./tool -run UnifiedExecOutputDeltas -count=1
go test ./tool -run SplitUnifiedExecValidUTF8Prefix -count=1
go test ./tool -run UnifiedExecLifecycleEventsMatchRust -count=1
go test ./tool -run UnifiedExecManagerReusesTTYSession -count=1
go vet ./tool
```

All focused checks passed on 2026-10-18. The broader `./tool` suite reached two
unrelated login-shell PATH assertions on this host; the touched unified-exec
delta and lifecycle cases passed.
