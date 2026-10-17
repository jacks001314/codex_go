# Round 116 · #52652 bounded local unified-exec output drain

Rust source: `89c8759d8c`.

## Landed

- Local unified-exec launches now wait at most five seconds for stdout/stderr
  readers after the foreground process exits.
- On expiry, the inherited reader handles are closed and the local process is
  finalized with the foreground exit code/output already captured. A descendant
  retaining inherited pipes can no longer delay `exec_command` or turn
  completion indefinitely.
- Both the ordinary local shell and local Windows-sandbox launch paths use the
  same drain helper.
- Remote unified-exec keeps its existing behavior and waits for output closure,
  preserving buffered or delayed remote output.

## Verification

```text
go test ./tool -run LocalUnifiedExecOutputDrain -count=1  # ok
go test ./tool -run UnifiedExec -count=1
# only the known login-shell PATH baseline failure

go vet ./tool
go build ./...
```

The new test blocks a reader without closing it and verifies the bounded drain
returns after the grace period, closes the reader, and joins the goroutine. A
fast-closing reader returns immediately without waiting for the deadline.
