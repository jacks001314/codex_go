# Rust #40645: bind settings operations to their committed snapshot

Upstream commit: `4e883ce36e4b69aabdc9d56bf19586d2e40a0756`

## Result

Equivalent implementation was already present.

- `ThreadExtraService.UpdateSettingsWithCommit` returns a deep-cloned snapshot
  from the same critical section that performed the write.
- Standalone settings notifications format that returned snapshot instead of
  re-reading live settings after post-commit work.
- Turn-start settings application carries the committed snapshot through
  notification emission while the turn uses the corresponding committed
  configuration.
- A later settings write cannot retroactively alter the earlier operation's
  notification or turn-start settings.

## Verification

```text
go test ./appserver -run 'UpdateSettingsWithCommit|SettingsCommit|ThreadSettings' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
