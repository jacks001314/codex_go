# #42623 Bound Noise handshakes by the exec server initialization timeout

## Upstream change

Rust separates transport startup into a Noise handshake readiness wait and the
JSON-RPC `initialize` request, sharing one configured initialization budget.
A stalled handshake is cancelled at that deadline, the transport tasks are
terminated, and timeout diagnostics retain the configured duration even after
the handshake consumed part of the budget.

## Gap found in Go

`execserver/environments_toml.go` parsed `initialize_timeout_sec` into
`EnvironmentTransport.InitializeTimeout`, but no dial path consumed it.
`DialNoiseRendezvousClient` also performed `dialNoiseHarnessConnection` and
`initializeClientConnection` without any initialization deadline, so only an
outer caller context could stop a stalled handshake.

## Implementation

- Added `DialClientOptions.InitializeTimeout`, defaulting to
  `DefaultRemoteExecServerInitializeTimeout`.
- Added `InitializeTimedOutError` with the configured duration and classified it
  as retryable for both preparation and recovery.
- Direct WebSocket and stdio transports wrap the initialize RPC in the timeout.
- Noise rendezvous runs the authenticated handshake and initialize RPC under a
  single deadline and closes the websocket when initialize fails.
- App-server environment records carry `InitializeTimeoutMS`; unified-exec and
  remote filesystem dial options carry `ExecServerInitializeTimeout`.

## Verification

- `go test ./execserver -run 'InitializeTimeout|NoiseHandshakeUsesInitializeTimeout|NoiseInitializeRPC|DirectInitializeRPC' -count=1`
- `go test ./appserver ./tool -run 'InitializeTimeout|EnvironmentProvider|ExecServerClientOptions|UnifiedExecEnvironments' -count=1`
- `go test ./execserver -count=1` passes except the pre-existing Windows
  `TestWindowsSandboxPowerShellFallbackWithoutCompatibleShellLikeRust` baseline
  failure.
