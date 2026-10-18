# `#41912` durable token usage rollout records

- Rust 依据：`5f79a92e3936274318d2122ae3244e5edd80dd1f`。
- 新增 `rollout.TokenUsageRecord`，字段与 Rust 对齐：thread/turn/session/
  root-turn/response attribution，`usage`、`turn_token_usage`、
  `thread_token_usage`。
- turn 结束时按 `result.Responses` 顺序写入每个 response 的记录；turn total
  为 running sum，thread total 由 turn baseline 加 running turn total 得到。
- reader 提供 `TokenUsageRecords` 与 `LatestTokenUsageRecord`。
- compaction checkpoint payload 现在直接内嵌 `compaction_response_id` 与
  `latest_token_usage_record`；缺失时输出显式 `null`，与 Rust wire 对齐。
- resume 继续通过 `token_usage_info` 恢复累计 usage；fork 测试覆盖 child
  active totals 隔离与 restored-display 顺序。

验证：

- `go test ./appserver -run 'TokenUsageRecordLikeRust|TestRuntimeRouterThreadForkRestoredTokenUsagePrecedesStarted' -count=1`
- `go test ./rollout -run 'CompactionCheckpointCarries|TokenUsage' -count=1`
- `go test ./rollout -count=1`
