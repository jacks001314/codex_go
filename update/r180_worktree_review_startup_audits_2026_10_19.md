# Round 737: worktree default and review-during-MCP audits

## `#44870` Enable worktrees by default

Rust 依据：`68bc5369ba0e694909b61603554d6622009b0cab`。

Go 已将 `worktrees` 注册为 stable/default-on，并从 `/experimental` 隐藏；
managed worktrees 对本地 session 保持可用。Rust 旧本地 daemon 的
`thread/backgroundTerminals/list` 兼容提示在 Go 本地 TUI 中没有载体，因为
Go 直接维护 in-session background process 状态。

验证：

- `go test ./features -run Worktree -count=1`
- `go test ./tui/tea -run 'Worktree|Experimental' -count=1`

## `#46493` Allow `/review` during MCP startup

Rust 依据：`35b746a3e3fb54f612e38bfef314b496bfde5b5e`。

Go TUI 在 MCP startup active 时仍可打开 review picker 或提交 inline
`/review`。前景 turn/review、queued follow-up 与 pending steer 仍阻止 review，
并保留 draft 与 attachments。

验证：

- `go test ./tui/tea -run 'TestReviewPickerOpensDuringMCPStartupLikeRust|TestInlineReviewSubmitsDuringMCPStartupLikeRust|TestReviewBlockedDuringForegroundWorkPreservesDraftLikeRust|TestReviewBlockedByQueuedFollowUpPreservesDraftLikeRust' -count=1`
