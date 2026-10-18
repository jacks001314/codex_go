# Round 750: agents overview background task image attachments

Rust 依据：`2ce38ae6d84a0aa96768742a5ac888a02ff622c7`（`#44027`）。

Go 的 Command Center 新任务采用空白 session + 主 composer 架构。本轮补上：

- 启动新任务前捕获 `agentsOverviewAttachments`；
- session start 失败时保留附件供重试；
- 成功附着新 session 后把附件恢复到主 composer，继续走普通图片提交路径。

验证：

- `go test ./tui/tea -run 'TestAgentsDashboardNewSessionPreservesImageAttachmentsLikeRust|TestModelAgentsDashboardNewSessionOpensBlankSession' -count=1`
