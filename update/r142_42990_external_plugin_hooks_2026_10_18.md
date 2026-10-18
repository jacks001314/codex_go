# #42990 Refresh session hooks after external plugin updates

## Upstream change

Rust retained plugin hook sources and load warnings inside the loaded hook
runtime. When a plugin store was updated by another process, the next turn
compared the newly loaded sources/warnings with the retained snapshot and
rebuilt hooks on a mismatch.

## Go architecture

Go does not retain a loaded hook runtime across turns:

- `turnHookAdapter` calls `hooksForCWD` for each turn.
- `hooksForCWD` calls `configureHookDiscoveryForThread`.
- `configureHookDiscoveryForThread` re-derives
  `PluginService.EnabledHookSources()` and creates a new discovery service.
- Plugin hook discovery rereads each `hooks/hooks.json`, so updated bundle
  contents are visible without a server notification.

## Regression

`TestLoadedSessionRefreshesExternallyUpdatedPluginHooksLikeRust` creates one
loaded router/plugin service, rewrites the plugin hook command to `1.0.0`,
`2.0.0`, and `0.9.0` without calling any refresh API, and asserts that each
successive turn adapter observes the new command.

## Verification

- `go test ./appserver -run 'LoadedSessionRefreshesExternallyUpdatedPluginHooksLikeRust|HookDiscovery|HooksList|PluginHook' -count=1`
