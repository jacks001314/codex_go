# Rust #44548: MIME-filtered resource listing for Codex Apps

Upstream commit: `94697375cb9d2aa8ae74d61957c6b396819bec94`

## Result

Implemented.

- Added `mcp.CodexAppsResourceListParams` with an optional cursor and required
  `mimeType`, and `mcp.MCPResourcePage` with resources plus the next cursor.
- `MCPService.ListCodexAppsResources` sends `resources/list` through the
  host-owned Codex Apps connection with a top-level `mimeType` on every page,
  including continuation requests.
- A registration named `codex_apps` that is not host-owned (for example an
  extension collision) is rejected before any resource request is sent.

## Verification

```text
go test ./mcp -run 'CodexAppsResource' -count=1
```

The test asserts the MIME filter on both pages, returned resources/cursors, and
the zero-request extension-collision rejection.

Passed on 2026-10-18.
