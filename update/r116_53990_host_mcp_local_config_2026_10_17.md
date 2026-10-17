# Round 116 · #53990 host MCP configuration with remote environments

Rust source: `0b73618146`.

## Landed

- Host MCP configuration loading now uses the selected local environment's cwd.
  With no local selection it falls back to the persisted session host cwd, then
  the router default cwd. A remote primary no longer drives host project-config
  discovery.
- Local stdio MCP servers inherit that host cwd when their command is relative
  or their `cwd` is omitted. Explicit per-server `cwd` values remain unchanged,
  and remote-owned servers never inherit the host cwd.
- Remote environment selection cwds are published as
  `RuntimeConfig.EnvironmentCWDs` for sandbox metadata.
- Repository skill discovery's local sandbox context now uses the local
  selection cwd and local workspace roots instead of the remote primary cwd.
- MCP runtime refresh already compares the complete captured environment
  snapshot; added coverage proving runtime workspace-root changes alter the
  fingerprint and therefore refresh the published MCP runtime.

## Verification

```text
go test ./appserver -run 'HostMCPPaths|MCPEnvironmentFingerprint|EffectiveMCPConfigForThreadLoadsHost' -count=1  # ok
go test ./appserver -run 'HostMCPPaths|ExecutorSkillLocalContext|MCP' -count=1                                  # ok
go vet ./appserver                                                                                              # ok
go build ./...                                                                                                  # ok
```

The tests cover remote-primary plus populated local selection, remote-only
fallback to the session host cwd, explicit server cwd preservation, relative
local server cwd assignment, remote server isolation, host project-config
selection, local skill sandbox context selection, and workspace-root fingerprint
invalidation.
