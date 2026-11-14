# Explicit MCP restriction projection regression

Added TestMCPExplicitDisabledPluginsOverrideThreadProjection using two installed local plugin fixtures with actual .mcp.json declarations. Calls runtimeMCPConfigForThread and checks resulting server registrations, not only the filter helper. Cases: inherited thread list blocks alpha; explicit context blocks beta instead; explicit empty context restores both; unrelated thread retains both. Shared PluginService contributions remain unchanged.

Final targeted go test -count=1 -timeout 3m ./appserver passed 0.135s; go vet ./appserver exited 0. Initial test literal compilation error was corrected before final validation. Test and final log synced to Master.

#61283 remains partial: host-only contribution context without live discovery, originator/session-source propagation and selected/pending environment integration remain. #61257 and overall synchronization also remain incomplete. No commit/push.
