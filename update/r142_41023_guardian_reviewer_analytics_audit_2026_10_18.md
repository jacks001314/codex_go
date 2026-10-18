# `#41023` Guardian reviewer analytics audit

- Rust 依据：`307ce6cda900be1669c271a7124d002870fcb435`。
- Go 已记录 review-level count、duration、TTFT、token 指标，以及 Guardian
  V2 classification / fast-decision 事件。
- `guardianSessionRunner` 直接调用进程内 `model.AgentRunner`，没有独立
  app-server session、turn event stream 或 canonical tool-item lifecycle；
  Rust 的 `track_guardian_session_event` 与 `tracked_tool_item_id` 桥接没有
  Go 载体，也不存在 listener-backed 事件重复上报风险。
- 空 patch 审计：review metrics 覆盖 private reviewer 的可观测工作。

验证：

- `go test ./appserver -run 'Guardian.*Metrics|GuardianReviewCompletedEmitsReviewAnalytics' -count=1`
