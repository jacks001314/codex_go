# Rust #51525: preserve CLI MXC preference in executor config reads

Upstream commit: `ed59a6c1cdf5e6fc96351fd46dfc1ef8a16db385`

## Result

Equivalent implementation was already present.

- The exec-server host reader accepts only the startup
  `features.prefer_mxc` preference for environment config projection.
- It inserts that value as a `session-flags` layer above project configuration
  and below legacy managed config, matching upstream precedence.
- Other startup overrides, including model or secret-adjacent values, are not
  retained in the executor read.
- Without an explicit preference, no session-flags layer is added.

## Verification

```text
go test ./execserver/hostconfig ./config -run 'PreferMXC|MXC|SessionFlags|CLI' -count=1
```

Passed on 2026-10-18.
