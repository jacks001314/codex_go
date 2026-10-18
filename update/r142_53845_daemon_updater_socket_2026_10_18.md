# Rust #53845: daemon updater socket binding and bootstrap readiness on Unix

Upstream commit: `2f05c0a71f59fb2f05800846351f3e9926c3e52b`

## Result

Equivalent implementation was already present.

- Long updater socket paths bind through a private short socket target.
- An existing regular file is preserved rather than overwritten by socket
  binding.
- Managed updater readiness checks both the process and socket.
- Lifecycle bootstrap/restart reject unmanaged sockets instead of taking them
  over.
- Backend readiness includes the captured stderr tail.

## Verification

```text
go test ./appserverdaemon -run 'UpdaterSocket|UpdaterReadiness|LifecycleRunnerBootstrap|LifecycleRunnerWaitUntilReady' -count=1
```

Passed on 2026-10-18.
