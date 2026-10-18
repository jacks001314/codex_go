# Rust #44655: honor thread-level plugin exclusions

Upstream commit: `8570091e14dc794b256402e27a7773e8de132f3e`

## Result

Implemented.

- `disabled_plugin_ids` are persisted on the thread record and settings
  snapshot, including an explicit empty list.
- Agent spawning inherits the parent selection by default, accepts an explicit
  replacement, and carries the selection into child turn params.
- A settings change becomes the active selection on the next task; the running
  turn keeps its admitted selection.
- Runtime capability and connector filtering consume the effective per-thread
  selection.

## Verification

```text
go test ./appserver -run 'DisabledPluginIDs|SpawnCapabilities' -count=1
```

Passed on 2026-10-18.
