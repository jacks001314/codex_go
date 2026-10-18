# Step-scoped MCP request metadata

Rust upstream: `04907ab95a186d176021abffccac3b627ff1e32e` -
`Use step settings for MCP request metadata (#40807)`.

## Alignment

Go's MCP metadata provider runs per call and reads the active step's
`RunConfig`. It refreshes:

- model;
- effective reasoning effort;
- Node REPL disabled state carried by the turn metadata;
- per-turn user-input-requested state.

The provider then derives `x-codex-turn-metadata` from the issuing step rather
than reusing a stale turn-start snapshot. Mid-turn settings changes therefore do
not leak the previous step's model or effort into later MCP calls.

## Verification

```text
go test ./appserver -run 'MCPTurnMetadataProvider|MCPRequestMetadata|StepSettings' -count=1
go test ./turn -run 'MCPTurnMetadata' -count=1
go test ./parity -count=1
```
