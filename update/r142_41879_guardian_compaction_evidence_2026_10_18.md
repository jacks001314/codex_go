# Rust #41879: preserve Guardian evidence across compaction

Upstream commit: `9f97cb79eb15b38d24c552c56fe24e211ff9cf3a`

## Result

Partially implemented.

Already present:

- The synchronous reviewer applies the retained user-instruction context and
  per-entry Guardian transcript retention/truncation rules.
- Rollback replaces the thread history, so reverted turns are no longer visible
  to the reviewer through the current record.

Still missing for full parity:

- Go builds the reviewer transcript from the current thread record
  (`guardianReviewTranscriptSelection`), not from a retained pre-compaction
  review-history snapshot. User/tool evidence replaced by a compaction summary
  can disappear from later reviews.
- The async-scorer transcript collector and its shared formatting path do not
  exist in Go.

## Verification

```text
go test ./appserver -run 'Guardian.*Compaction|Guardian.*Retained|Guardian.*Evidence' -count=1
```

Passed on 2026-10-18.
