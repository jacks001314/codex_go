# Rust #42147: skip Guardian reviews in Full Access

Upstream commit: `e5769939113536eb72752660bf7d1903f799d198`

## Result

Equivalent implementation was already present.

- Full Access requires approval policy `never` and every selected environment to
  resolve to unrestricted danger-full-access permissions.
- Pending, failed, or restricted selected environments do not qualify.
- Guardian returns `approved` with reason `full access` without sampling a
  reviewer, including strict/sensitive MCP confirmation paths.
- Switching back to `on-request` restores normal Guardian review behavior.

## Verification

```text
go test ./appserver -run 'Guardian.*FullAccess|FullAccess.*Guardian|GuardianDecisionDuration' -count=1
```

Passed on 2026-10-18.
