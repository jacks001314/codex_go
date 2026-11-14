# #61199 Rollout trace same-binary schema contract

## Behavior

- `rollout/trace_reduce.go` now requires manifest `schema_version == 2` and each raw trace event `schema_version == 2`.
- Empty manifests, missing or non-integer versions, and mismatched versions are rejected with an actionable error telling the operator to run the exact executable that recorded the bundle.
- Existing trace fixtures and the debug CLI fixture were updated to the versioned format.

## Boundary

This implements the Go reducer/replay validation boundary. It does not add a new bundle writer version; callers that still emit unversioned bundles are intentionally rejected.

## Regression

- `rollout/trace_same_binary_replay_like_rust_test.go` covers manifest mismatch, event mismatch, and missing event schema.
- Existing reduce/replay validation tests still pass with versioned fixtures.

## Evidence

- `go test ./rollout -count=1` passed in 2.240s after removing a duplicate test helper.
- `go test ./app -run TestDebugTraceReduce -count=1` passed after updating the CLI fixture.
- Logs: `update/rollout_trace_same_binary_2026_11_14.log`, `update/go_test_after_rollout_fix_2026_11_14.log`.
