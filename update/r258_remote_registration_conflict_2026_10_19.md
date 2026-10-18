# Confirmed remote registration conflicts

Rust upstream: `e325e3acd9ab64cd287a2c4d6cd7a7cebb639618` -
`Retry confirmed remote registration conflicts (#41219)`.

## Alignment

Go's remote executor registration path matches Rust's narrow retry policy:

- retry only HTTP 503 whose structured body has `code=registration_conflict`;
- use registry-recovery jittered exponential backoff so competing executors do
  not retry in lockstep;
- fail immediately for other 503 bodies, ambiguous timeouts, authorization
  failures and unrelated HTTP/protocol errors;
- keep retries inside the enclosing remote-transport cancellation scope.

Tests cover confirmed conflicts, non-conflicting 503 responses, nested detail,
auth failures, and fail-fast behavior.

## Verification

```text
go test ./execserver -run 'RegistrationConflict|Remote.*Retry|ConfirmedConflict' -count=1
go test ./parity -count=1
```
