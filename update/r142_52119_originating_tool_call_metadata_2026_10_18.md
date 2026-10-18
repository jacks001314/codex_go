# Rust #52119: expose originating tool-call metadata in command environments

Upstream commit: `9b738582b13c2cdbeff54af0afd04c50c3e7ba09`

## Result

Equivalent implementation was already present.

- Command environments expose the protected `CODEX_TOOL_CALL_ID` metadata.
- Explicit execution metadata takes precedence over shell policy and overlay
  values.
- Unified exec preserves the originating call ID through write_stdin and remote
  executor processes.
- Shell snapshot capture strips and restores the protected metadata rather than
  leaking or overwriting inherited values.
- Missing/stale metadata, empty values, and NUL-invalid values are handled
  consistently.

## Verification

```text
go test ./tool -run OriginatingToolCallID -count=1
go test ./execserver -run ChildEnvAppliesExecutionMetadataLikeRust -count=1
```

Passed on 2026-10-18.
