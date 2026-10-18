# Round 736: TUI command-loop and embedded-input audits

## `#42408` Harden embedded composer input handling

Rust 依据：`93053c7f5ddc1c26e649e9e7ffc0d9e853c633cf`。

Go 没有复用 Rust `ChatComposerConfig::plain_text` 的共享 embedded composer。
`CustomPromptView` 是独立编辑器，直接保留 `!`、`/`、`?` 等前缀文本，已有
独立 paste-burst Enter 策略和原地空提交行为，因此没有可切换的 shell-mode
或共享 footer-flash 载体。

验证：

- `go test ./tui/bottom_pane -run 'TestCustomPromptPasteBurstNewlineDoesNotSubmitShortFirstLine|TestCustomPromptDelayedEnterAfterTypingSubmits|TestCustomPromptEmptySubmitCancelPasteAndRows' -count=1`

## `#43298` Defer managed worktree transitions

Rust 依据：`52e12e0cb506e7bb2c9e406fc84d922e274a0e40`。

Go 的 agents overview worktree 创建通过 `newAgentsOverviewWorktreeCmd` 返回
Bubble Tea command，结果在后续 `Update` 中应用；不存在 Rust 递归 event
handler 中需要延迟到 fresh loop iteration 的 attach/transition 栈风险。

验证：

- `go test ./tui/tea -run 'TestModelAgentsDashboardNewWorktree' -count=1`

## `#45108` Cancel pending thread title generation after manual renames

Rust 依据：`b4c864dd6497ae764e6a826300b34f7ca77ba965`。

Go TUI 目前没有异步模型生成的 thread-title 请求；首轮标题由
`interactiveAutoThreadTitle` 同步派生，手动 rename 直接持久化。因此不存在
需要取消的 pending title generation。

## `#46077` Keep the composer responsive during Command Center session creation

Rust 依据：`1e9564fb859c7543404ede96ba5d92f1ef7faefc`。

Go 的 `newAgentsOverviewSessionCmd` 将 session start 放入异步 command，
设置 `agentsOverviewBusy`，并在后续 `Update` 中应用结果；事件循环与 composer
不会在 session creation 期间被同步阻塞。失败路径保留并报告草稿。

验证：

- `go test ./tui/tea -run 'TestModelAgentsDashboardNewSessionOpensBlankSession' -count=1`
