# Rust #53990: keep host MCP and plugin configuration local with remote environments

Upstream commit: `0b73618146d55a2565589bf4e2d5e40e8f298d9e`

## Result

Equivalent implementation was already present.

- Host MCP/plugin configuration is built from the local environment selection.
- A remote primary executor does not replace the host project config, cwd, or
  workspace roots.
- Host-relative stdio servers use the local host path; explicitly configured or
  executor-specific servers retain their own cwd/environment carrier.
- Workspace-root changes participate in the MCP runtime fingerprint.

## Verification

```text
go test ./appserver -run 'HostMCP|MCPConfigForThread|MCPEnvironmentFingerprint' -count=1
```

Passed on 2026-10-18.
