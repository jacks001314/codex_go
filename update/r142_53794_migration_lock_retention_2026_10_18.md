# Rust #53794: retain rollout migration locks across worker cancellation

Upstream commit: `a373281e9ef021ffb1b340123ce96c75bac869fb`

## Result

Equivalent implementation was already present.

- Migration apply acquires the process-scoped rollout maintenance flock before
  scanning or mutating rollouts.
- The lock is held until the migration call returns; cancellation does not
  perform an ownership-aware early unlock.
- Compression and migration share the same lock.
- A competing maintenance job records `skipped_busy` rather than racing a
  replacement rename.

## Verification

```text
go test ./rollout -run 'MaintenanceLock|MigrateRolloutsApplyReportsBusy' -count=1
```

Passed on 2026-10-18.
