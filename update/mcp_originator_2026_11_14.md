# MCP explicit originator projection

Added WithOriginator/Originator to contribution context. Active-turn MCP configuration passes TurnStartParams.Originator; runtimeMCPConfigForThread applies the explicit value to implicit compatibility Apps registration after merging contributions. It replaces case-insensitive duplicate originator headers and leaves external/plugin registration headers unchanged. Empty global identity adds no header.

Actual runtime-config tests cover codex_work_desktop, guardian, empty identity, an external service with its own configured originator, and the previous disabled-plugin override cases. Targeted appserver/mcp tests passed (0.098s/0.040s); go vet ./appserver ./mcp exited 0. These test configured HTTP headers, not a live network request.

#61283 remains partial: host-only constructor/session-source and no-live-state projection, pending environment integration and actual request-header verification remain. Overall synchronization and commit/push are incomplete.
