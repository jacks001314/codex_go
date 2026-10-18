# Rust #47887: warn when SQLite diagnostic log writes fail

Upstream commit: `3d6428510fee6f52d8993ebacf4818bb42ba0cde`

## Result

Implemented.

`state.LogDBHandler` now retains the latest batch-flush diagnostic, exposes
`HasWriteFailure`/`LastWriteFailure`, and supports attaching a
`LogWriteFailureReporter` after writes have already failed.

The app server installs a reporter when its notification sink becomes
available. The reporter:

- emits one `warning` notification per router;
- uses the `/feedback` guidance only when feedback is enabled;
- replays a failure that happened before the sink was attached;
- keeps later failures from duplicating the warning while the diagnostic remains
  available in memory.

## Verification

```text
go test ./state -run 'LogDB|LogHandler|InstallLogDB' -count=1
go test ./appserver -run 'LogDBWrite|Warning' -count=1
go vet ./state ./appserver
```

Passed on 2026-10-18.
