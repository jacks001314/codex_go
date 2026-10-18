# Rust #43943: gate new turns on host shutdown admission

Upstream commit: `7c098d87418a357bafb241a579a18938801989e0`

## Result

Equivalent host-shutdown gate.

- `ThreadManager.BeginShutdown` sets the closing bit and snapshots/cancels
  active turns.
- `ReserveTurn` and `RegisterTurn` return `ErrServerDraining` for later work.
- The runtime router maps that error to the server-draining RPC error.
- Go has no extension admission registry, so the host-shutdown gate is the
  equivalent carrier.

## Verification

```text
go test ./appserver -run 'ServerDraining|DaemonRecoverySnapshot|DaemonRecoveryRestore|DaemonSnapshot' -count=1
```

Passed on 2026-10-18.
