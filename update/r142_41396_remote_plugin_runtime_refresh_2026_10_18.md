# #41396 Refresh runtimes for remote plugin state changes

## Upstream change

Rust refreshes affected runtime categories when remote plugin state changes,
including MCP servers, skills, and hooks, and reports the changed capabilities.

## Go carriers

- There is no persistent remote-plugin runtime cache to invalidate.
- `plugin/reconcile` compares `pluginReconcileSignature` values covering hooks,
  skills, MCP/apps, versions, enablement, availability, auth policy, install
  policy, disabled reason, and install suggestion.
- `RuntimeRouter.refreshPluginCachesAndMCPRuntimes` rebuilds MCP bindings, skill
  caches, hook discovery, and plugin contributions.
- Reconcile, install, and config-mutation paths call the refresh; hook
  discovery is additionally re-derived for every turn.

## Verification

- `go test ./appserver -run 'PluginReconcile|PluginInstallRefresh|RemoteReconciliation|PluginRefresh' -count=1`
