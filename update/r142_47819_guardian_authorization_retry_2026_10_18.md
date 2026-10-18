# #47819 Retry Guardian reviews when authorization changes

## Upstream change

A completed Guardian allow is stale when the captured review authorization or
transcript changes while the reviewer runs. The review refreshes its evidence
under the same attempt budget and deadline without paying the service backoff.
A deny remains valid on the evidence it already saw, and exhausting the retry
budget fails closed.

## Go status

Complete by equivalent implementation.

`appserver/guardian_reviewer.go` detects a completed allow whose transcript
changed, rolls the reviewer back to its last committed checkpoint, and retries
locally under the shared attempt budget. Stale authorization uses `Gosched`
instead of the service backoff. Denies bypass the stale retry, and an exhausted
stale-allow budget returns the failed-closed denial.

## Verification

- `go test ./appserver -run 'TestGuardianReviewRefreshesChangedContextLikeRust|TestGuardianReviewStaleRetrySkipsServiceBackoffLikeRust|TestGuardianReviewDenySkipsStaleRetryLikeRust' -count=1`
