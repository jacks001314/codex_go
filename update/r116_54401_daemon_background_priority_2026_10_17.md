# Round 116 · #54401 managed daemon background priority

Rust source: `97ebf2d94d`.

## Landed

- macOS managed daemon launches probe `taskpolicy -b` for up to five seconds.
  When the probe succeeds, the managed app-server and updater are launched
  through `taskpolicy -b`, using its POSIX spawn exec behavior so the recorded
  PID remains the managed process PID.
- Probe denial, errors, or timeouts fall back to a direct launch so a restricted
  environment cannot make the daemon unavailable.
- The managed executable is checked for regular-file and execute permissions
  before the macOS launch.
- macOS readiness waits now use a 30-second budget instead of 10 seconds to
  account for background CPU and I/O scheduling. Other platforms keep 10
  seconds.
- Windows detached managed processes retain the existing detachment flags and
  now also set `CREATE_NO_WINDOW`.

## Verification

```text
go test ./appserverdaemon -count=1  # ok
go vet ./appserverdaemon            # ok
go build ./...                      # ok
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go test -c ./appserverdaemon  # ok
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./appserverdaemon   # ok
```

The macOS-only tests cover taskpolicy wrapper selection, argument forwarding,
and direct fallback after a timed-out probe. The common tests pin the
platform-specific readiness timeout.
