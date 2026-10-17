# Round 116 · #52679 timeout-aware exec-server configuration reads

Rust source: `6e093c6081` (`Add timeout-aware exec-server configuration reads`).

## Landed

- `execserver.Client.ReadEnvironmentConfig` preserves the unbounded read.
- `execserver.Client.ReadEnvironmentConfigWithTimeout` bounds sending and
  receiving after connection establishment.
- Shared `callWithTimeoutRecovery` now serves both `environmentConfig/read` and
  the existing `environment/info` probe.
- `RPCTimedOutError` exposes method + timeout and is retryable for read-only
  recovery preparation.
- A timed-out reconnectable transport is retired. Non-reconnectable stdio keeps
  the connection open, so valid in-flight work and later reads survive.

## Verification

```text
go test ./execserver -run 'EnvironmentConfigReadTimeout|EnvironmentInfoTimeout|EnvironmentInfoKeepsCaller' -count=1
# ok

go test ./execserver -count=1
# only the two known Windows junction / PowerShell-fallback baseline failures

go vet ./execserver  # ok
go build ./...      # ok
```

RC: removing the `reconnectDisabled` guard retires the stdio connection on an
RPC timeout and fails `TestEnvironmentConfigReadTimeoutKeepsStdioConnectionUsableLikeRust`.
