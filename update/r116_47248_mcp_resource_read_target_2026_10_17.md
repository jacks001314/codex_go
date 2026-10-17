# Round 116 · #47248 explicit app/account targets for MCP resource reads

Rust source: `3b50349e1a`.

## Landed

- Added `mcp.MCPResourceReadParams.Target` and `mcp.MCPResourceReadTarget`
  (`connectorId`, required-but-nullable `linkId`). `LinkIDSet` tracks wire
  presence so a missing `linkId` is rejected instead of being treated as an
  explicit no-auth request; marshaling always emits `linkId` (`null` for
  no-auth).
- `validateMCPResourceTarget` mirrors Rust: direct targets require the
  `codex_apps` server, a non-empty connector id, and a real link id (or null).
  Empty and `synthetic_link::` ids are rejected, and an `originCallId` scope
  makes the target ignored rather than invalid.
- Resource reads now forward `_meta` to the MCP server: the legacy connector id
  as `x-codex-turn-metadata.mcp_request_meta.selected_connector_ids` (Rust
  #39244), and `connector_id`/`link_id` for direct targets (Rust #47248). Both
  HTTP and stdio transports share the request builder; omitted targets keep the
  previous discovery behavior.
- The resource cache key now includes the originating call scope and the direct
  target identity, so the same URI under two accounts cannot reuse cached
  contents. Cache-key normalization preserves both fields.

## Verification

```text
go test ./mcp -run 'TestMCPResourceReadTarget|TestMCPResourceReadForwardsTargetMeta' -count=1  # ok
go test ./mcp -count=1  # ok
go test ./appserver -run 'Schema|Mcp|MCP' -count=1  # ok
go vet ./mcp  # ok
go build ./...  # ok
```

Coverage: validation table (no target, account link, no-auth, foreign server,
empty connector, missing/empty/synthetic link, origin scope), wire round-trip,
MCP-server `_meta` forwarding, and target-scoped cache isolation.
