# Rust #47142: standalone web search honors system proxy settings

Upstream commit: `b045757052064f36d64805fbe56ddd426ef91e9b`

## Result

Equivalent implementation was already present.

- Standalone web search constructs its HTTP client from the effective
  `respect_system_proxy` setting.
- When enabled, the transport keeps the host proxy resolver. When disabled, the
  proxy function is cleared.
- Dedicated appserver and network tests cover the search route and both proxy
  modes.

## Verification

```text
go test ./appserver -run 'StandaloneWebSearchHonorsSystemProxySetting' -count=1
go test ./network -run 'SystemProxy' -count=1
```

Passed on 2026-10-18.
