# Rust #43597: trusted tool metadata in shared Guardian context

Upstream commit: `255423956117c82ca558b87173eb76379c7b0664`

## Result

Equivalent implementation.

- `context.GuardianToolDescriptions` renders trusted host and connector metadata
  as the shared `<guardian_tool_descriptions>` block.
- `state.renderGuardianToolDescriptions` appends that block for every review.

## Verification

```text
go test ./context ./state -run 'GuardianToolDescriptions' -count=1
```

Passed on 2026-10-18.
