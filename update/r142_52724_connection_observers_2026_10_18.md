# Rust #52724: observe initial exec-server connection attempts

Upstream commit: `7efd184c2410c02a9d0feb4896855f0f18a8f47e`

## Result

Equivalent implementation was already present.

- `EnvironmentManager.ObserveConnectionAttempts` installs a first-wins,
  one-shot observer for an environment's initial exec-server connections.
- Each attempt reports elapsed time and `success`, `failure`, or `cancelled`.
- Failed startup attempts leave the observer armed for a later retry; successful
  attempts and later reconnections are not reported.
- Environment replacement or removal cancels in-flight observed attempts and
  forgets the observer exactly once.
- Observing an attempt that already happened is ignored.

## Verification

```text
go test ./appserver -run ObserveConnectionAttempts -count=1
```

Passed on 2026-10-18.
