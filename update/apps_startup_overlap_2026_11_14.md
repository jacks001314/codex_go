# Apps startup listing overlap (#61220)

## Behavior

- `mcp/apps_startup_listings.go` shares only overlapping successful `tools/list` calls in one complete discovery scope.
- Each participant initializes and keeps its own MCP connection. Shared results are cloned before use.
- A waiting startup respects its own deadline, retries with its own connection after failure or cancellation, and does not share across scopes or servers that opt out of catalog sharing.

## Regression

`TestAppsStartupServicesShareSuccessfulOverlap` drives two `MCPService` instances through the production `discoverToolCatalogForStatus` entry point. It synchronizes on the in-flight waiter count, publishes one listing, and asserts both services receive the catalog while retaining distinct clients and exactly one `tools/list` request.

`TestAppsStartupServicesHonorInitializeOptOut` covers the waiting timeout and server opt-out paths.

## Evidence

- `go test ./mcp -run 'TestAppsStartupServices' -count=3` passed.
- Log: `update/apps_startup_overlap_verify_2026_11_14.log`.
