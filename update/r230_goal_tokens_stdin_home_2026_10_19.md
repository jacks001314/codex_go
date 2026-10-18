# Subagent goal token 汇总、stdin review 指标与 executor home

本轮关闭：

- `#41183` Account subagent token usage toward root goals：Go 已将 child/grandchild
  用量滚入 root goal，覆盖 active/idle accounting、goal replacement 和并发
  checkpoint。
- `#41189` Instrument stdin review size checks：已有
  `codex.unified_exec.stdin_review.size_check`，带 result/input_kind 标签。
- `#41204` Propagate executor home directories into sandbox contexts：环境信息与
  `FileSystemSandboxContext.UserHomeDir` 已传播 executor home。

## 验证

- `go test ./appserver -run 'DescendantToken' -count=1`
- `go test ./appserver -run 'StdinReview|SizeCheck' -count=1`
- `go test ./execserver ./appserver -run 'UserHomeDir|EnvironmentInfo' -count=1`
