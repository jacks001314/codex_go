# Explicit MCP environment restrictions

runtimeMCPConfigForThread now applies explicit SelectedEnvironments authority after overlay merging and retains it in RuntimeConfig. Disallowed registrations remain present for diagnostics but are disabled, so ConfiguredServers cannot expose them. Existing disable reasons are preserved; implicit Apps exemption uses the shared EnvironmentAuthority implementation.

Added actual host projection tests for ready, pending and failed remote environments, checking both registration Enabled and Manager.ConfiguredServers output. Existing host/explicit/prediction tests passed 0.170s. Broader MCP environment/authority/selection/projection regressions across appserver and mcp also exited 0; go vet ./appserver ./mcp exited 0. Logs: mcp_pending_context_2026_11_14.log, mcp_environment_regression_2026_11_14.log.

Remaining #61283: actual HTTP originator verification and session-source consumer audit, broader complete-package regression. #61257 registration-scoped identity remains separate outstanding work. Overall synchronization and commit/push remain incomplete.
