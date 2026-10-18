# Rust #39958: stop advertising shell snapshots from local exec servers

Upstream commit: `4e6ef3b4181931e97aefea1f510ea821b2333a27`

## Implemented

- Local `EnvironmentInfo.Capabilities.ShellSnapshotV2` is now `false` on every
  platform.
- The capability test now locks the false value instead of platform-dependent
  Go runtime detection.

This prevents clients from treating a local exec-server as an in-memory
snapshot-capable executor.

```text
go test ./execserver -run ShellSnapshot -count=1
go vet ./execserver
```

Passed on 2026-10-18.
