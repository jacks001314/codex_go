# Round 116 · #52235 gRPC code-mode missing-session recovery

Rust source: `137f25f643`.

## Landed

- gRPC code-mode Execute, Wait, and Terminate now retire the session binding
  when the RPC returns `NOT_FOUND`, allowing the next execution to open a fresh
  session.
- The failed call returns its original error and is never replayed.
- `INVALID_ARGUMENT`, ordinary transport failures, and successful calls preserve
  the existing session binding.
- `CompleteToolCall` is intentionally excluded because its `NOT_FOUND` may
  identify a missing invocation in a live session.

## Verification

```text
go test ./codemode -run 'RemoteSessionNotFound|RemoteProvider|GrpcSession' -count=1  # ok
go test ./codemode -count=1                                                          # ok
go vet ./codemode                                                                    # ok
go build ./...                                                                       # ok
```

Go uses the generic code-mode transport rather than separate Execute/Wait/
Terminate RPCs, so the recovery rule is applied at `remoteSession` on every
session-scoped operation's gRPC status.
