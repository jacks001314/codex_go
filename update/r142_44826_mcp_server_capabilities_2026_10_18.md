# Rust #44826: expose advertised MCP server capabilities

Upstream commit: `7a6f469dcff1337786aae046df2d899accea59ab`

## Result

Implemented.

- `MCPServerStatus.ServerCapabilities` carries the initialized server's
  advertised capabilities object.
- Status responses preserve it, or report null/empty when unavailable.
- A failed connection attempt clears the capabilities; retained tool-list state
  keeps the latest advertised object.

## Verification

```text
go test ./mcp -run 'ServerCapabilities|ToolList' -count=1
```

Passed on 2026-10-18.
