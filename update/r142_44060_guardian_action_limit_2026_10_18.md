# Rust #44060: raise Guardian action review limit to 200,000 bytes

Upstream commit: `3d3df0a0cad5d3d8d3340b633787e9dd304ea463`

## Result

Equivalent implementation was already present.

- `state.GuardianMaxActionBytes` is `50_000 * 4` (200,000 bytes), while
  individual action strings retain the 16,000-token truncation budget.
- `FormatGuardianActionPretty` rejects an oversized aggregate document with
  Rust's exact error instead of reviewing a partial action.
- Unified exec terminal input keeps its independent 8,000-byte approval cap,
  refuses formatter truncation or oversized serialized input, and records
  `codex.unified_exec.stdin_review.size_check` with result and input kind.

## Verification

```text
go test ./state -run 'GuardianActionReviewLimit' -count=1
go test ./appserver -run 'GuardianStdinReviewSizeLimit|Guardian.*Action|StdIn' -count=1
```

Passed on 2026-10-18.
