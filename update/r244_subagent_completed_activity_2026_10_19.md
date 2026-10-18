# Completed sub-agent activity

Rust upstream: `b705b6b076c64e5d66d6b340a32b07ae32d20486` -
`Report completed sub-agent activity on parent turns (#40437)`.

## Alignment

Go emits a `subAgentActivity` item with `kind=completed` on the parent or
initiating turn after a delegated child finishes:

- the item carries the child thread ID and canonical agent path;
- its ID is stable as `subagent-completed-<child-turn>`;
- the item is persisted and delivered through `item/completed`;
- analytics count completed activity without treating it as another subagent
  tool call;
- triggered-communication and direct-parent paths are both covered.

## Verification

```text
go test ./appserver -run 'SubAgentActivity|SubagentCompleted|SubAgent' -count=1
go test ./parity -count=1
```
