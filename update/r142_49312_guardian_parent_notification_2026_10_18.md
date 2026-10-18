# Rust #49312: notify parent agents when Guardian stops a subagent

Upstream commit: `63475131ce5f6a479ffdf01163bd1d95873536a2`

## Result

Equivalent implementation was already present.

- A strict Guardian circuit-break interruption delivers an agent message from
  the child path to its parent path.
- The message begins with `Agent interrupted by Guardian:`, includes a bounded
  denial reason, and appends the fixed guidance not to resume or retry the
  blocked work without explicit user confirmation.
- Delivery uses the parent's active-turn mailbox when busy and the idle agent
  message wake path otherwise.
- Default circuit-break mode and non-Multi-Agent-V2 children do not notify.

## Verification

```text
go test ./appserver -run 'GuardianSubagentNotification|GuardianExtensionInterruption|DenialLimitMetric|Guardian.*Parent' -count=1
```

Passed on 2026-10-18.
