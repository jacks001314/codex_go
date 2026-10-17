# Round 116 · #40688 bounded SQLite integrity checks for diagnostics

Rust source: `32ce703a80`.

## Status

The behavior was already present in `doctor/doctor.go`; this round added the
missing regression coverage and closed the ledger row.

- `stateCheck` gives every runtime database scan a one-second deadline
  (`feedbackIntegrityCheckDeadline`) only when collecting a diagnostic
  attachment (`opts.Feedback`). Direct `codex doctor` checks pass a zero
  deadline and stay unbounded.
- `sqliteIntegrityCheckForDoctor` runs `PRAGMA integrity_check` through
  `QueryContext`, so the driver interrupts scans and lock waits at the
  deadline, and returns any corruption rows read before the interruption.
- `sqliteIntegrityDetailForDoctor` keeps those rows and reports a failure when
  corruption was observed; an incomplete clean scan is a warning
  (`scan exceeded time limit`) instead of a silent pass or false failure.

## Verification

```text
go test ./doctor -run 'TestDiagnosticIntegrityCheckDeadlineLikeRust|TestIntegrityResultKeepsCorruptionRowsLikeRust' -count=1  # ok
go test ./doctor -count=1  # ok
go vet ./doctor  # ok
go build ./...   # ok
```

The new test covers an expired diagnostic deadline (warning, no corruption
claim), an unbounded clean scan (`integrity: ok`), and preservation of partial
corruption rows.
