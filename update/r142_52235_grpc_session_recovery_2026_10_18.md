# Rust #52235: recover gRPC code-mode sessions after missing-session errors

Upstream commit: `137f25f6439ce8be758f15dbd1e1b297c6cd0c3b`

## Result

Equivalent implementation was already present.

- Execute, Wait, and Terminate retire only the affected session binding when
  the host reports gRPC `NotFound`.
- The next operation reopens the session; the failed execution itself is never
  replayed.
- CompleteToolCall `NotFound` is intentionally excluded because it may refer to
  a missing invocation in an otherwise live session.
- Other sessions, unrelated status codes, and ordinary transport errors retain
  their existing state.

## Verification

```text
go test ./codemode -run RemoteSessionNotFoundRetiresBindingLikeRust -count=1
```

Passed on 2026-10-18.
