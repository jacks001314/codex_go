# Round 116 · #44482 Guardian retry and failure reporting

Rust source: `5d3fe48b08`.

## Status

The behavior was present in Go; this round added direct regression coverage and
closed the ledger row.

- `guardianReviewRetryableError` retries rate-limit, capacity, and transient
  stream/transport/HTTP failures (status 0/408/429/5xx) while failing closed on
  non-transient HTTP errors such as 400.
- The review retry loop raises the service backoff to the server's requested
  `Retry-After` (`codexapi.RetryDelay`) and waits under the shared review
  deadline, so exhausted-stream advice is honored per attempt. Retry advice is
  not persisted across turns in Go, so a reused session cannot apply a stale
  delay.
- Failed reviews stay denied, `state.Event.FailClosed` leaves
  `RiskLevel`/`UserAuthorization` unset, and the rejection text includes
  "This is a review failure, not a determination that the action is unsafe."

`ExhaustedResponseRetry` is Rust's per-turn advice object; Go passes the delay
directly on the error chain, which is the equivalent single-turn scope.

## Verification

```text
go test ./appserver -run 'TestGuardianReviewFailureLeavesAssessmentUnsetLikeRust|TestGuardianReviewHonorsServerRetryDelayLikeRust' -count=1  # ok
go test ./appserver -run 'Guardian' -count=1  # ok
go build ./...  # ok
```

Coverage: non-transient failure is not retried and records no assessment,
rate-limit recovery approves after exactly two attempts, and the 60 ms server
delay is honored.
