# Round 116 · #42990 external plugin hook refreshes (not applicable)

Rust source: `2bd71f96d4`.

## Why the cache invalidation has no Go carrier

Rust compares the plugin hook sources and load warnings with the ones retained
by a session's hook runtime and refreshes when they differ, because that runtime
is kept for the session's lifetime.

Go has no persistent loaded hook runtime to go stale:

- `RuntimeRouter.configureHookDiscovery` builds a fresh `HookDiscoveryService`
  for every turn and every `hook/list` call, taking the current
  `Plugins.EnabledHookSources()` and disabled-plugin filter at that moment.
- `refreshPluginHookRuntimes` documents this explicitly: hook runtimes are
  re-derived per turn/call, so there is no loaded-runtime cache to invalidate;
  it only preserves the Rust refresh gate.

External plugin updates are therefore observed by the next turn without any
source/warning comparison, and `matches_plugin_hooks` has nothing to compare
against. The row is closed as `not_applicable`; it should be revisited if Go
introduces a session-owned hook runtime.

## Verification

```text
rg -n 'configureHookDiscovery|refreshPluginHookRuntimes' appserver/runtime_router.go  # per-turn rebuild
go test ./appserver -run 'Hook|Plugin' -count=1  # ok
```
