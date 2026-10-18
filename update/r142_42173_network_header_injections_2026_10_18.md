# Rust #42173: network requirement header injections

Upstream commit: `671d5d1b3531d7251d6c59709b8d29fb7927e685`

## Result

Core implementation already present.

- Requirements parsing accepts `experimental_network.header_injections`
  entries with `host`, `methods`, `path_prefixes`, and a header map.
- `NetworkHeaderInjection` is exported, carried through `NetworkConstraints`,
  and deep-cloned with the rest of the requirements.
- `network.ApplyProxyHeaderInjections` applies matching rules to proxied
  requests without changing whether non-matching requests are allowed.
- The upstream TUI debug-configuration summary lives in the TUI lane.

## Verification

```text
go test ./config ./network -run 'NetworkHeaderInjections|ApplyProxyHeaderInjections' -count=1
```

Passed on 2026-10-18.
