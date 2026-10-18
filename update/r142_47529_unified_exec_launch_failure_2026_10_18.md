# Rust #47529: command lifecycle for unified exec launch failures

Upstream commit: `a69d757cd8ef8310001186865911b69e4b4175e5`

## Result

Equivalent implementation was already present.

- Local pipe, local PTY, and remote unified-exec launches report `Begin` and
  terminal `End` events when process creation fails before a session exists.
- The terminal item has status `failed`, exit code `-1`, duration `0`, the
  launch diagnostic as aggregated output, and no process ID.
- Approval rejection remains a separate `declined` lifecycle and never enters
  the launch-failure helper. Sandbox denial retry behavior is unchanged.
- App-server conversion and analytics consume the same terminal item, including
  plugin attribution and command source.

## Verification

```text
go test ./tool -run 'UnifiedExec.*Launch|LaunchFailure' -count=1
go test ./appserver -run 'UnifiedExecLaunchFailure|CommandExecutionNotifications' -count=1
```

Passed on 2026-10-18.
