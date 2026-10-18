# Rust #46010: app tool result metadata with analytics controls

Upstream commit: `3a589370a49ddf197de8e5ae03a92615bcad58bf`

## Result

Equivalent implementation was already present.

- The MCP tool executor captures raw result `_meta` only when result metadata
  capture is analytics-enabled.
- Captured metadata is associated with the originating tool call and retained
  window so it cannot follow a different call.
- App-server exposes host-owned app result metadata under the same analytics
  gate; disabled capture leaves no model-visible metadata.

## Verification

```text
go test ./mcp -run 'ToolExecutorResultMetadata|ResultMetadata' -count=1
go test ./appserver -run 'MCPResultMetadataFollowsAnalytics' -count=1
```

Passed on 2026-10-18.
