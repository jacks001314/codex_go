# Rust #51595: add thread ID exclusions to thread/list

Upstream commit: `1fbe15c962cc3d8eabec36d67987c83cfc4eeec9`

## Result

Equivalent implementation was already present.

- `ThreadListParams.excludedThreadIds` is optional and accepts at most 100
  unique valid thread IDs.
- Exclusions are applied before the result limit, preserving full pages after
  filtering.
- Both the rollout path and `useStateDBOnly` path honor exclusions.
- Null and empty lists mean no exclusions.
- Duplicate or malformed IDs are rejected with invalid-params errors.

## Verification

```text
go test ./appserver -run ThreadListExcludedThreadIDs -count=1
```

Passed on 2026-10-18.
