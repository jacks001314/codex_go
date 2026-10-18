# Environment workspace roots、history-notes 加密/截断、async 消息描述

本轮关闭：

- `7625bd5665` Honor environment-resolved workspace roots (#40912)
- `57e2edc6e9` Encrypt sensitive history and notes tool arguments (#41041)
- `4cb8d8679c` Forward truncation policies to the history notes backend (#41062)
- `218a3e50ae` Clarify when to send asynchronous user messages (#41070)

## 结论与修复

- turn 环境配置已记录所选环境的 resolved workspace roots，sandbox/MCP/turn
  初始化读取该值。
- historynotes 只将 query 与 note text 标记为 `encrypted`，并为对应 backend
  路由设置 `x-openai-encrypted-tool-arguments`。
- `ToolTruncationPolicy` 通过
  `x-openai-tool-output-truncation-policy` 转发，nil 时不发送。
- `send_user_message_async` 的旧描述已更新为上游文案：覆盖 missing
  information、blockers/findings、进行中回复、commentary 分工和醒目格式。

## 验证

- `go test ./appserver -run 'ThreadEnvironmentConfig|WorkspaceRoots' -count=1`
- `go test ./historynotes -count=1`
- `go test ./tool -run 'SendUserMessageAsync|SendMessageToUserAsync' -count=1`
