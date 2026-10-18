# Rust #40982: Guardian trusted context for configured MCP tools

Upstream commit: `d61ba72f2f8af9adb694b8701aa64ed96cd80760`

## Result

Not applicable by architecture.

- The commit adds `GuardianTrustedToolFragment`, `trusted_tool_context`, and
  related sampling plumbing exclusively inside
  `ext/guardian-v2/src/async_scorer`.
- Go has no Guardian V2 async scorer.
- Go's synchronous reviewer already carries the invoked MCP tool's trusted
  host and connector descriptions through
  `state.renderGuardianToolDescriptions` and renders them as the
  `<guardian_tool_descriptions>` evidence block.

## Verification

```text
go test ./state -run 'GuardianToolDescriptions|GuardianActionReviewPreservesDescriptionArguments' -count=1
```

Passed on 2026-10-18.
