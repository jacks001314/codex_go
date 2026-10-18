# Guardian checkpoint migration evidence audits

Rust upstream commits:

- `#45789` Preserve Guardian evidence during checkpoint migration
- `#45782` Preserve Guardian authorization evidence across checkpoint migration

## Verdicts

Both complete. Go rollout checkpoints carry `guardian_history`; app-server
rebuilds retained user/tool/assistant evidence independently from the current
record, skips compaction replacement when reconstructing Guardian evidence,
keeps subagent authorization isolated, and truncates the independent history on
revert/rollback.

## Verification

```text
go test ./appserver -run 'Guardian.*Compaction|Guardian.*Checkpoint|Compaction.*Guardian' -count=1
go test ./rollout -run Guardian -count=1
```
