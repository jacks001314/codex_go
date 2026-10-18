# 流式 rate-limit 分类与 Responses Lite 稳定前缀 ID

本轮关闭：

- `e0c727de04` Classify streaming rate-limit errors (#40931)
- `e77773085c` Give Responses Lite prefix items stable IDs (#40962)

## 结论

- Responses 流式错误已区分可重试的 `rate_limit_exceeded` / `slow_down` 与终止性的
  `usage_limit_reached`，并保留 retry-after 和 rate-limit snapshot 证据。
- Responses Lite 的 `additional_tools` 与 base-instruction 前缀项使用由 thread ID
  和序列化 payload 派生的确定性 ID；同一请求/恢复保持稳定，thread 或 payload
  变化时更新。

## 验证

- `go test ./model -run 'ResponsesLitePrefixItemsUseStable|UsageLimit|RateLimit' -count=1`
- `go test ./appserver -run 'RateLimit|ErrorInfo' -count=1`
