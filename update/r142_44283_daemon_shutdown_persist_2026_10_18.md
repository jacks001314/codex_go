# Rust #44283: persist loaded threads before daemon shutdown

Upstream commit: `c1840dc55e3cbb7ef27ccc1fb20b38cdd0e9ef68`

## Result

Implemented.

- `RuntimeRouter.close` captures the daemon recovery snapshot while active
  turns still exist, before `BeginShutdown` cancels them.
- After cancellation/drain, `ThreadRouter.Close` calls `CloseLiveThreads`,
  which closes each managed live thread's persistence and flushes accumulated
  rollout state before process exit.

## Verification

```text
go test ./appserver -run 'DaemonRecoverySnapshot|DaemonRecoveryRestore|DaemonSnapshot' -count=1
```

Passed on 2026-10-18.
