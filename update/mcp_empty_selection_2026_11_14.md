# Empty MCP environment selection parity correction

Rust core/src/mcp.rs McpEnvironmentScope::Selected([]) retains selection scope: the default environment stays unrestricted; other environments permit selected-plugin registrations only. HostOnly (no snapshot) is unrestricted. Go incorrectly set Scoped based on len(selections)>0 and its existing unit test asserted that mismatch.

Changed all non-nil selection snapshots to Scoped=true and corrected the old assertion. Added runtime projection test distinguishing absent selection (remote enabled) from explicit empty selection (remote disabled), with default environment enabled in both. Shared authority logic preserves selected-plugin exceptions.

Targeted MCP environment/authority/host tests pass: mcp 0.046s, appserver 1.373s. go vet ./appserver ./mcp exits 0. Started a fresh full mcp run after this correction (mcp_full_empty_selection_2026_11_14.log). Previous complete appserver/mcp session39822 is still running and predates this correction, so its eventual result alone does not certify this change. Overall sync remains incomplete.
