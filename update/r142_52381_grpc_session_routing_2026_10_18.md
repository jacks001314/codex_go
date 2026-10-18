# Rust #52381: per-session routing for gRPC code mode

Upstream commit: `5b3c1522f85316063e43b97fa7e13751d55bccdb`

## Result

Architectural equivalent with the applicable admission changes implemented.

- Go multiplexes every code-mode JSON session operation over one
  `CodeModeHost/Transport` gRPC stream. It does not issue independent
  `OpenSession`, `Execute`, `Wait`, and `CloseSession` RPCs, so sessions sharing
  one transport necessarily share one host affinity. There is no per-RPC
  `x-code-mode-route` carrier to capture or attach.
- Accordingly, `x-code-mode-route-lost` has no Go route layer to invalidate.
  Existing `NOT_FOUND` handling still reopens only the affected session without
  replaying the failed execution.
- The admission retry policy from this commit was applied: five attempts for
  transient `Unavailable`/`ResourceExhausted`, an immediate first retry, then
  jittered 50/100/200ms exponential backoff.
- Execute and other established session operations remain outside the retry
  loop, so no execution is replayed.

## Verification

```text
go test ./codemode -run GrpcSessionAdmissionRetryPolicy -count=1
go test ./codemode -run Grpc -count=1
```

Passed on 2026-10-18.
