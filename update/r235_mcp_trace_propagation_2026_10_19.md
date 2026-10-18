# MCP request trace context across transports

Rust upstream: `94174e44cb Preserve MCP request trace context across transport workers (#47212)`.

## Implementation

- `MCPToolCallParams` carries the caller `context.Context` into the MCP HTTP
  transport without serializing it.
- The active telemetry span is stored as a narrow trace carrier in the context
  by `telemetry.WithSpan`, so `mcp` can propagate W3C headers without importing
  `telemetry` and creating a package cycle.
- HTTP `tools/call` requests inject `traceparent` and `tracestate`.
- Response-stream processing keeps the original context when replying to nested
  server requests (for example SSE elicitation), so the worker's response POST
  stays in the same trace.

## Verification

```text
go test ./mcp -count=1
go test ./turn -run 'MCP|Tool' -count=1
go test ./appserver -run MCP -count=1
go vet ./mcp ./model ./telemetry
go build ./...
```
