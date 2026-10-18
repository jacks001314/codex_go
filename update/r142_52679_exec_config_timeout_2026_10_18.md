# Rust #52679: timeout-aware exec-server configuration reads

Upstream commit: `6e093c6081223bf264890656ae33886225d09280`

## Result

Equivalent implementation was already present.

- `ReadEnvironmentConfig` retains the unbounded/local behavior.
- `ReadEnvironmentConfigWithTimeout` bounds both sending and receiving after
  connection establishment through `callWithTimeoutRecovery`.
- Timeouts return typed `RPCTimedOutError` and classify as retryable.
- Reconnectable transports retire the probed connection and request recovery.
- Stdio transports with reconnect disabled remain open after a timeout, so
  valid in-flight reads can finish and late responses do not poison later reads.
- Environment-info requests already use the same timeout/recovery helper.

## Verification

```text
go test ./execserver -run EnvironmentConfigReadTimeoutKeepsStdioConnectionUsable -count=1
```

Passed on 2026-10-18.
