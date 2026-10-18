# Rust #53622: preserve remote exec output until it is collected

Upstream commit: `b043762f9e3eb65dd5164ff5880f139bc6d084c7`

## Result

Equivalent implementation was already present.

- Exec-server processes retain buffered or delayed output after exit.
- The final collection poll consumes the open stream and exit state before the
  background session is removed.
- The delayed output and exit code are returned to the caller.
- Cleanup does not leave a stale background session.

## Verification

```text
go test ./tool -run ConsumesPushedRemoteEventsAndRecoversReplayLikeRust -count=1
```

Passed on 2026-10-18.
