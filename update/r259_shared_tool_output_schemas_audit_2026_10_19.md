# Shared tool output schemas audit

Rust upstream: `f3803587c9 Share tool output schemas and defer MCP envelope construction (#45439)`.

## Verdict

Complete. Go tool specs carry shared output-schema values for built-in and MCP
tools; Code Mode forwards compact output-schema raw bytes to its nested surface;
and MCP result envelopes are built when a call executes rather than retained in
catalog entries.

## Verification

```text
go test ./tool -run 'Handler|CodeMode|OutputSchema' -count=1
go test ./mcp -run 'Tool|Catalog|Runtime' -count=1
```
