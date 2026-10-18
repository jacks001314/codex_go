# #45985 Named Guardian review result structs

## Upstream change

Rust replaces positional review result tuples with `ReviewTurnResult` and
`ReviewSessionResult`, carrying outcome, session disposition, completion
accounting, and analytics. The refactor removes boolean reuse flags and
positional tuple access without changing observable review behavior.

## Go status

Not applicable by architecture.

Go's Guardian reviewer does not use tuple-based results or a boolean
keep-session flag. Review attempts return the decision, rejection rationale,
and error directly, while reviewer history is managed by
`guardianSessionRunner` checkpoints, `rollbackReviewHistory`, and
`completeReview`. Session retention is expressed by retaining or resetting the
runner rather than by a result tuple. There is no behavioral gap to port.

## Verification

- `go test ./appserver -run 'Guardian' -count=1`
