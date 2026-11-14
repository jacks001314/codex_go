# Host-only live inventory isolation

Extended actual MCP projection regression with a registered active turn carrying a complete empty V2 executor inventory. The normal step projection correctly suppresses host plugin fallback; a host-only projection for the same parent still returns both host plugins. Shared plugin inventory remains unchanged. This proves the no-live-inventory branch with a distinguishable production input.

Targeted TestMCPExplicit tests pass 0.109s; go vet ./appserver exits 0. Files/log synced to Master.

Next gap identified: runtimeMCPConfigForThread attaches SelectedEnvironments, but does not apply its Authority to returned registrations. Existing EnvironmentAuthority.AllowsRegistration is available for reuse; pending/failed restrictions need to apply after all overlays, with tests for ready/pending and explicit empty semantics. Actual HTTP originator validation and session-source consumption remain unverified. #61283 and overall synchronization are incomplete.
