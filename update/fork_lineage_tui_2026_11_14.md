# TUI 会话 fork 父线程展示（#61183）

Rust `ThreadSessionState::set_fork_parent` 在 `thread/read` 重建和 `ThreadStarted` 通知里都使用目标线程自己的 `forked_from_id`，父线程变化或缺失时清掉缓存的 `fork_parent_title`；挂载会话时 `session_flow` 用这两个字段输出 fork 行。Go 的 `tui/app` 重建层已有同样的 setter 与测试，但 tea TUI 的会话摘要没有携带父线程，恢复/分叉时不会显示 fork 行。

本轮把摘要链路补齐：

- `codextui.SessionSummary` 新增 `ForkedFromID` / `ForkParentTitle`；`sessionSummaryFromRecord`、`sessionSummaryFromAppServerThread` 与 `remoteTUISessionSummaryFromThread` 都从目标线程自身读取父线程，不会沿用其它线程的缓存。
- 远程恢复/只读回退在摘要带父线程时额外 `thread/read` 父线程一次，缓存其名称；本地恢复从 session store 读取父 record 的标题；父线程不可读时标题留空，fork 行只显示 id。
- tea `applyResumeResponseWithCWD` 在重建 transcript 后、session header 之前插入 `chatwidget.ForkedThreadEventLine`；`applyForkSummary` 在切换到 fork 后同样插入。恢复无父线程的会话时 transcript 由该会话自己的摘要重建，旧 fork 行不会残留。

回归：

- `tui/session_source_lineage_like_rust_test.go`：record 与 app-server thread 都带出自己的 `ForkedFromID`，无父线程为空。
- `tui/tea/fork_lineage_like_rust_test.go`：恢复 fork 会话显示 `• Thread forked from Parent chat (thread-parent)`；切到无父线程的会话后 fork 行消失；父线程不可读时显示 id-only 行。

验证：`go vet ./tui ./tui/tea ./app ./appserver` 退出 0；`go test ./tui -count=1`（10.0s）、`go test ./tui/tea -count=1`（47.4s）、`go test ./app -count=1`（40.4s）通过。日志见 `fork_lineage_tui_2026_11_14.log`。

仍未完成：仅通过 `thread/started` 通知发现、没有经过恢复/分叉摘要的会话不会插入 fork 行；这不再造成父指针污染（tea 模型不缓存跨线程的父字段），但外部 fork 的展示仍待接线。
