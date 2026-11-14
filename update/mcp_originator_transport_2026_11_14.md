# MCP originator request construction

Added TestMCPExplicitOriginatorReachesHTTPTransport. A host context is projected through runtimeMCPConfigForThread, installed in a real MCPService, and invoked through CallTool. A capture RoundTripper observes the actual constructed request headers then intentionally returns an error before external network I/O. Every observed request carries codex_work_desktop originator. No credential is configured or read by the fixture.

Targeted test passed 1.334s; go vet ./appserver ./mcp exited 0. This proves HTTP transport-boundary propagation, not a remote MCP server response.

Started complete uncached serial appserver/mcp regression with 12m package timeout; log mcp_context_full_2026_11_14.log. Completion still pending. Session-source consumption and remaining #61283 scope audit must finish; #61257 scoped registry and other plan work remain. Overall sync and commit/push are not complete.
