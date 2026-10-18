# #41215 Roll over Guardian context before follow-up reviews

## Upstream change

Rust compacts an exhausted Guardian reviewer session before the next review,
then resets transcript and Node REPL evidence cursors when the full transcript is
no longer present, and restores the follow-up reminder and Node REPL policy.

## Go status

Partial.

Implemented:

- `guardianSessionRunner` retains `previous`/`seeded` review history,
  prior-review/completed-review state, and Node REPL policy invalidation.
- `modelGuardianReviewer` tracks admitted-image and reviewed-evidence
  sequences.
- Parent compaction can reseed or drop the cached reviewer session
  (`resetGuardianAfterParentCompaction`).

Remaining:

- No pre-review `context_window_token_status_for_model` check and no reviewer
  `Op::Compact` submission.
- No detection that a rollover removed the full transcript marker, and no reset
  of transcript/evidence cursors in that case.
- No equivalent post-compaction re-injection of the follow-up reminder and Node
  REPL policy.

## Verification

- `go test ./appserver -run 'Guardian' -count=1`
