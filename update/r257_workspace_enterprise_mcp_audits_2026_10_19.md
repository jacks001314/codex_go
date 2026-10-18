# Workspace routing and enterprise MCP catalog audits

Rust upstream commits:

- `#45812` Add workspace routing support for Responses requests
- `#45459` Resolve enterprise-managed MCP registrations in the catalog

## Verdicts

- `#45812` complete: workspace routing discovery resolves the selected
  workspace backend origin and account routing override, applies it to the
  provider/agent identity, preserves forced workspace ids, and exposes the
  result through account/read with safe diagnostics.
- `#45459` complete: enterprise-managed MCP registrations require trusted
  profile authority, merge into the runtime catalog with requirements, and are
  retired when changed, revoked, or reload fails.

## Verification

```text
go test ./model -run WorkspaceRouting -count=1
go test ./appserver -run 'WorkspaceRouting|Enterprise|MCP.*Catalog|MCPConfig' -count=1
go test ./mcp -run 'Enterprise|EMA|Catalog' -count=1
```
