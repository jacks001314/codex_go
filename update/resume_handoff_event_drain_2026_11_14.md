# 恢复切换时丢弃旧线程的迟到事件（#61216）

Rust #61216 在切换线程或 backend 前先 `drain_outgoing_thread_events`，再把新会话装进 TUI；否则旧线程已经排队的 `thread/started`、`turn/completed` 或 item 事件会在切换之后到达，重新切回刚离开的会话或把旧回合状态画到新会话里。Go 的 tea 模型只能在 Update 里逐条处理消息，没有可同步排空的通道，所以本轮在模型层补一道“旧线程迟到事件”闸门。

实现：

- 新增 `Model.outgoingThreadID`：`applyResumeResponseWithCWD`（覆盖恢复、backend 切换）、`applyForkSummary` 与任务面板 agent 切换在安装新线程前调用 `noteThreadHandoff(previous, next)` 记录刚离开的线程；再次附加该线程时会把 outgoing 更新为本次离开的线程。
- `applyThreadEvent` 先检查 `dropLateOutgoingEvent(event)`：事件带有 outgoing 线程 ID 时直接丢弃，旧线程的 `thread/started` 不再把视图切回去，`turn.completed` 也不会结束新会话的运行状态；下一次附加该线程时会话历史由服务端快照重建，因此丢弃迟到通知不会丢持久内容。
- 远程 TUI 的 item started/completed、agent delta、plan delta 事件补写 `event.ThreadID = payload.ThreadID`，让闸门同样覆盖这些原本不带线程身份的普通事件；后台线程仍走 `ThreadScopedEventMsg` 缓冲路径，不受影响。

回归 `TestResumeHandoffDropsLateOutgoingThreadEventsLikeRust` 覆盖：恢复后迟到的 `thread/started` 不会切回、旧线程 `turn/completed` 不会结束新线程、旧 item 不进入新 transcript、当前线程 item 正常显示、重新附加旧线程后事件恢复生效。

验证：`go vet ./tui/tea ./app` 退出 0；`go test ./tui/tea -count=1` 通过（47.5s）；`go test ./app -run "Notification|Remote" -count=1` 通过；`go test ./app -count=1` 全包通过（39.4s）。日志见 `resume_handoff_event_drain_2026_11_14.log`。

仍未完成：跨 backend 身份校验与 Cloud 会话拒绝 `!` shell 命令仍待接线；TUI 尚未发出 `thread/shellCommand`。
