# Round 744: startup-draft handoff audit

Rust 依据：`66c4e5a20d211f329de32b0452b35a9aac66b3c5`（`#48121`）。

Rust 的 StartupDraftPump 在 loading frame 与 session attach 之间保留初始
composer，并延迟 terminal clear。Go 的 Command Center 没有该双层 startup-draft
结构：`newAgentsOverviewSessionCmd` 通过 Bubble Tea command 异步启动 session，
后续 `Update` 再附着结果；线程草稿由 `agentsOverviewDrafts` /
`agentsOverviewPendingDraft` 显式保存和恢复。因此没有 deferred thread-switch
clear 或 startup-draft surface 可对齐，记为 `not_applicable`。

验证：

- `go test ./tui/tea -run 'AgentsDashboard|Draft|NewSession' -count=1`
