# Rust #52723: opt-in gRPC over stdio for the code-mode host

Upstream commit: `2dc0791c7d2cb4decda6b8ebd8b54d38d7411f64`

## Result

Equivalent implementation was already present.

- `code_mode_host_grpc` selects `codemode.GrpcProcessProvider`; without the
  opt-in feature, the existing process provider remains active.
- The provider spawns the host with stdin/stdout pipes and opens the
  `CodeModeHost/Transport` bidirectional gRPC stream over a passthrough dialer.
- The host side serves the same transport over stdin/stdout and exits when the
  connection closes.
- Nested tools, session isolation, and recovery after host death work through
  the gRPC transport.

## Verification

```text
go test ./codemode -run Grpc -count=1
go test ./appserver -run CodeModeHostGrpcFeature -count=1
```

Passed on 2026-10-18.
