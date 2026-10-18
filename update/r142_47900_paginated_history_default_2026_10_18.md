# `#47900` Default local threads to paginated history

- Rust 依据：`12cb14f7b70f57aaf643d10ca072c44b555992d1`。
- `NewDefaultRuntimeRouterWithOptions` 创建本地 thread store 后安装 paginated
  默认；`Router` 保留 legacy 兼容默认，供低层嵌入/测试直接使用。
- 显式 `historyMode: "legacy"` 仍写入 legacy record；in-memory ephemeral
  线程允许显式 paginated 创建。
- `thread/revert` 在加载与 direct-input 检查之后、读取历史之前拒绝
  ephemeral 线程，错误文本与 Rust 一致。
- Rust 的 `fork_legacy_thread` / `resume_legacy_thread_from_rollout` /
  `spawn_legacy_subagent` 等是内部 API 重命名，Go 没有同名载体。
- Guardian reviewer 在 Go 中是进程内 session，没有 persisted thread-store
  history-mode 入口；TUI 的 ephemeral replay/attach 防护属于 TUI lane。

验证：

- `go test ./appserver -run 'DefaultsLocalThreadsToPaginated|RejectsEphemeralThreadRevert' -count=1`
- `go test ./appserver -run 'ThreadResume|ThreadFork|AgentSpawn|Subagent|Guardian' -count=1`
- `go build ./...`

已知基线：`TestRuntimeRouterThreadStartProjectlessDoesNotPersistProjectTrustLikeRust`
与 `TestRuntimeRouterThreadStartProjectTrustWriteGuards` 在 clean HEAD 上同样失败
（本机全局/project trust 环境导致），与本次改动无关。
