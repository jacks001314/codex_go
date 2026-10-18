# Rust #45987: centralize Guardian action preparation

Upstream commit: `fd346b8dbaa24573a0244bc917811849d27c4cf4`

## Result

Equivalent implementation was already present.

- Go's typed action projection omits only host-provided top-level
  `tool_description` and `connector_description` fields for MCP actions.
- The same descriptions are rendered separately as untrusted Guardian tool
  description evidence.
- The `arguments` JSON remains unchanged, including nested fields named
  `description`, `tool_description`, or `connector_description`.

Added regression coverage for the nested-argument boundary.

## Verification

```text
go test ./state -run 'GuardianMCPActionReviewPreservesDescriptionArguments|GuardianActionReviewLimit' -count=1
```

Passed on 2026-10-18.
