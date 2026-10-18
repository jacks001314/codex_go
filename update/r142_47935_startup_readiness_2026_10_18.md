# Rust #47935: cached catalogs satisfy MCP startup readiness

Upstream commit: `b62fcd3c4b4126e8dc619ab5334f5658c4d84657`

## Implemented

- Added `mcp_servers.<name>.startup_readiness`, defaulting to `connection`;
  `catalog` opts a server into cached-catalog startup.
- Added strict config validation and `codex mcp` config round-tripping. The
  default value remains omitted from serialized config.
- Required-server validation now accepts a cached catalog only under `catalog`
  readiness. Explicit turn-scoped server requirements and selected-plugin
  requirements continue to wait for the live connection.
- Cached startup checks the per-server enabled/disabled tool filter and Rust's
  `_meta.ui.visibility` model-visibility rule.
- Binding capture records filtered cached tools and their tool-list revision;
  a later notification invalidates the binding.
- Tool execution is unchanged and still requires the live transport. A cached
  catalog only affects startup readiness.
- Startup readiness is excluded from the connection cache identity, so changing
  only this policy does not discard a reusable MCP connection.

## Verification

```text
go test ./mcp -count=1
go test ./config -count=1
go test ./app -run MCP -count=1
go vet ./mcp ./config ./app
```

All commands passed on 2026-10-18.
