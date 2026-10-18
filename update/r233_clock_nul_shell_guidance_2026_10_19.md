# Clock control tools、NUL stdin、executor shell guidance 与 one-shot exec

本轮关闭：

- `#41331` clock tools 作为 built-in control tools 分类。
- `#41354` reviewed terminal input 中的 NUL byte 拒绝。
- `#41368` shell guidance 使用 executor PlatformOS。
- `#41380` proactive multi-agent guidance 文案。
- `#41393` unified exec 关闭时保留 one-shot exec 语义。
- `#41570` proactive multi-agent 语法修正。
- `#41413` 标 N/A：Rust 专用 history lookup 性能重构。
- `#41449` 标 N/A：Rust Seatbelt profile 文件重命名，Go 用代码构造策略。

## 验证

- `go test ./appserver -run 'ClockToolsAreBuiltinControlTools|Stdin.*NUL|StdinReview|ProactiveMultiAgent|MultiAgentMode' -count=1`
- `go test ./tool -run 'OneShotExec|ModelVisiblePlatformOS|PlatformOS' -count=1`
