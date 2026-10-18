# Rust #43595: centralize bounded Guardian review evidence

Upstream commit: `0b9b5ecff3b5951bc6c86c40aa91da3f7d6e86b1`

## Result

Equivalent centralized carrier.

- `state.ContextProfile` and `TranscriptEvidenceEntry` centralize bounded review
  evidence selection, per-section retention, omission notices, and original
  entry numbering.
- The synchronous reviewer consumes the same rendered transcript path.

## Verification

```text
go test ./state -run 'GuardianContextProfile' -count=1
```

Passed on 2026-10-18.
