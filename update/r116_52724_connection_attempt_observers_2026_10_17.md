# Round 116 · #52724 initial exec-server connection observers

Rust source: `7efd184c24`.

## Landed

- Added `ConnectionAttemptOutcome` (`Success`, `Failure`, `Cancelled`) and the
  `ConnectionObserver` callback signature carrying the elapsed duration.
- Added `EnvironmentManager.ObserveConnectionAttempts`, the Go analogue of
  `Environment::observe_connection_attempts`. Registration must happen before
  the first connect; the first observer wins and unknown environments are
  ignored.
- Both first-connection paths (`InfoContext` and `StatusContext`) arm the
  observer once the transport is about to be used, after any provisioning gate
  and without touching info-override, pending-provisioning, or unconfigured
  environments.
- The reported duration starts at the transport connection and ends at client
  installation: a completed websocket upgrade plus `initialize`/`initialized`
  handshake, or a completed `DialClientWithOptions` /
  `DialNoiseRendezvousClient` (which include the handshake and any internal
  retries). A later `environment/info` or `environment/status` RPC failure does
  not turn an installed client into a failed attempt.
- Failed startup attempts keep the observer armed, so a retry reports
  `Failure` followed by `Success`. Success or `Cancelled` disarms it, so
  reconnections and past attempts are never reported.
- Replacing (`environment/add`, provider snapshot, Noise registration) or
  removing an environment cancels every in-flight initial attempt. The attempt
  then unwinds and reports `Cancelled` exactly once; the replacement record
  starts without an observer.

## Verification

```text
go test ./appserver -run 'TestEnvironmentObserveConnectionAttempts' -count=5  # ok
go test -race ./appserver -run 'TestEnvironmentObserveConnectionAttempts' -count=2  # ok
go test ./appserver -run 'TestManager|TestEnvironment|TestFetchRemote|TestRemote|TestDeferred|TestProvision|TestOpenAI' -count=1  # ok
go vet ./appserver  # ok
go build ./...      # ok
```

New tests cover failure-then-retry-success with a measured elapsed duration,
first-observer-wins, cancellation on replacement and removal with exactly one
notification, and ignoring attempts that started before registration.
