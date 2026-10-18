# Rust #41912: persist response token usage in rollout history

Upstream commit: `5f79a92e3936274318d2122ae3244e5edd80dd1f`

## Result

Partially covered by Go's thread-metadata accounting.

Already present:

- Go accumulates turn/thread usage into the thread record
  (`token_usage_info`, `total_token_usage`, `last_token_usage`) and restores it
  on resume.
- Forks emit the restored token-usage notification before `thread/started`
  (`TestRuntimeRouterThreadForkRestoredTokenUsagePrecedesStarted`).

Still missing for full parity:

- A durable `token_usage_record` rollout item with response, turn, session, and
  root-turn attribution (`rg TokenUsageRecord` finds no Go carrier).
- Compaction checkpoint snapshots of `compaction_response_id` and
  `latest_token_usage_record`.
- Reconciling the fork's restored-display usage with the child's fresh active
  totals: clearing metadata outright regresses the existing fork
  notification, so this needs a dedicated baseline reset rather than dropping
  the restored snapshot.

## Verification

```text
go test ./appserver -run 'TestRuntimeRouterThreadForkRestoredTokenUsagePrecedesStarted' -count=1
```

Passed on 2026-10-18.
