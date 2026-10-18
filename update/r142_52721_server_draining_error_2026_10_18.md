# Rust #52721: explain session creation failures during shutdown

Upstream commit: `07757f448f2d3cf475d0f328732c1c277bce8aa1`

## Implemented

- Graceful-shutdown admission now returns a dedicated `ErrServerDraining`
  instead of a generic thread-manager conflict.
- The error preserves Rust's exact message:
  `Server is draining; retry after reconnecting`.
- JSON-RPC maps it to invalid-request code `-32600` and includes
  `{"reason":"serverShuttingDown"}`.
- New turn reservation and tracked turn registration both return the structured
  shutdown error; rejected tracked registration still cancels its callback.
- The TUI lane already recognizes the exact draining message and presents the
  background-server shutdown guidance with automatic reconnect wording for
  non-embedded servers.

## Verification

```text
go test ./appserver -run ServerDrainingAdmissionError -count=1
go test ./appserver -run JSONRPCErrorCode -count=1
go test ./appserver -run ThreadManager -count=1
go vet ./appserver
```

Passed on 2026-10-18.
