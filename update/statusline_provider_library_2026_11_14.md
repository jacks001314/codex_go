# Statusline MCP provider library (2026-11-14)

## Scope

This ports the staged statusline provider subsystem introduced by Rust #61248,
#61249, #61250 and #61272. The current upstream tree also exposes these
functions for tests but does not wire them into the TUI yet; this Go package
follows the same staged boundary.

## Behavior

- `ProviderFromTool` reads `_meta["openai/ui"].entrypoints`, accepts exactly one
  valid `ui://` statusline entrypoint, falls back from title to tool name, and
  keeps the saved server/tool identity independent of title and URI changes.
- `globalPages` requests `mcpServerStatus/list` with `toolsAndAuthOnly`, rejects
  repeated cursors, and keeps only local servers whose `httpOrigin` is explicitly
  null. Plugin IDs are parsed and invalid identities are skipped.
- `inventoryServer` verifies a targeted answer contains at most one server with
  the requested name.
- `Lookup` resolves a saved or discovered provider within five seconds. It
  rejects project-owned `mcp_servers`, `plugins`, or `marketplaces` definitions,
  requires the global and live plugin owner to match, and requires the live
  server to be connected.
- `Resolved.Read` sends the opaque statusline snapshot under
  `com.openai/codex-statusline-state` on a thread-scoped `mcpServer/resource/read`.
- `Refresh` streams inventory results while probing known names after a two
  second head start. It bounds probes to four, cancels a probe when inventory
  supplies the same server, and reports per-server timeout/error updates at the
  five second deadline.
- Every statusline inventory and resource-read request carries
  `environmentId: null` explicitly.

## Evidence

- `go test ./statuslineprovider -count=1` passed.
- `go test -race ./statuslineprovider -count=1` passed.
- `go vet ./statuslineprovider` exited 0.
- Logs: `update/statusline_provider_library_2026_11_14.log`,
  `update/statusline_provider_vet_2026_11_14.log`.

## Remaining boundary

Config selection (`tui.status_line_provider`) and the eventual TUI picker call
sites are separate items and remain tracked in the plan.
