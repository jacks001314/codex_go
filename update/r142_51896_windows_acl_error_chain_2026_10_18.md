# Rust #51896: preserve native errors in Windows sandbox ACL diagnostics

Upstream commit: `dd178bb7cabb828b253992e082301fa7bf18c735`

## Result

Equivalent implementation was already present.

- The setup helper records the ACL failure summary before writing its individual
  log line, so a logging failure cannot hide the native cause.
- Check, grant, and deny operations retain Rust's distinct summary/log wording.
- `formatSetupErrorChain` unwraps the complete error chain and preserves each
  distinct native Windows cause.
- Sandbox-bin lock failures use the same chain formatter.
- ACL handle-opening diagnostics name the actual access masks
  (`READ_CONTROL | WRITE_DAC` and `MAXIMUM_ALLOWED`).

## Verification

```text
go test ./sandbox/windowssandbox/bin/setup_main/win -run ACL -count=1
```

Passed on 2026-10-18.
