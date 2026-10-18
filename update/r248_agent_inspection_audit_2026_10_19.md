# Agent inspection without restoring unloaded runtimes

Rust upstream: `7d99ee82d7 Add agent inspection without restoring unloaded runtimes (#47010)`.

## Verdict

Complete. Go's `thread/read` and stored thread records provide inspection without
resuming an unloaded agent runtime. Resuming an unloaded multi-agent v2 child
through its parent is rejected with the Rust guidance to use `thread/read`,
while parent-owned resume still works for loaded children.

## Verification

```text
go test ./appserver -run 'ParentOwnedV2Resume|Unloaded|AgentInspection|Fork' -count=1
```
