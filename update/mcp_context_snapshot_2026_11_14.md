# Explicit MCP plugin restriction context

Implemented the first part of #61283: MCPServerContributionContext.WithDisabledPluginIDs captures a copied explicit restriction list; DisabledPluginIDs returns a copy and presence bit. Explicit empty lists clear inherited restrictions. runtimeMCPConfigForThread now uses this supplied list for plugin contributions, falling back to live thread settings only when no explicit snapshot was supplied. Shared filtering preserves existing thread-only behavior.

Tests validate input/getter mutation isolation and explicit-empty semantics; existing per-thread contribution filtering regression passes. go test -count=1 ./mcp ./appserver -run 'TestContributionContextDisabledPluginsSnapshot|TestRuntimeRouterDisabledPluginMCPContributionsFilteredLikeRust' passed. go vet ./mcp ./appserver exited 0.

Remaining #61283: actual config projection integration test with explicit override, host-only context without live discovery, originator/session-source propagation, selected/pending environment restrictions. #61257 scoped registration is also outstanding. This is partial delivery; overall sync and commit/push are not complete.
